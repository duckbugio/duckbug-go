package nethttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	duckbug "github.com/duckbugio/duckbug-go"
	"github.com/duckbugio/duckbug-go/pond"
)

type captureProvider struct {
	mu     sync.Mutex
	events []duckbug.Event
}

func (p *captureProvider) CaptureEvent(_ context.Context, event duckbug.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cloned := duckbug.Event{Type: event.Type}
	raw, _ := json.Marshal(event.Payload)
	_ = json.Unmarshal(raw, &cloned.Payload)
	p.events = append(p.events, cloned)
}

func TestMiddlewareAttachesRequestContext(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
		Pond: pond.New(pond.Config{
			EnvProvider: func() map[string]string { return nil },
		}),
	})

	handler := Middleware(duck, WithReadBody(true))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		duck.LogContext(r.Context(), "info", "request received", map[string]any{
			"password": "123456",
		})
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://example.com/checkout?token=abc", bytes.NewBufferString(`{"api_key":"secret","amount":42}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "cookie-value"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 response, got %d", rec.Code)
	}
	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}

	payload := provider.events[0].Payload
	if payload["method"] != "POST" {
		t.Fatalf("expected POST method, got %#v", payload["method"])
	}
	headers := asMap(t, payload["headers"])
	if headers["Authorization"] != "***" {
		t.Fatalf("expected Authorization to be masked, got %#v", headers["Authorization"])
	}
	queryParams := asMap(t, payload["queryParams"])
	if queryParams["token"] != "***" {
		t.Fatalf("expected token query param to be masked, got %#v", queryParams["token"])
	}
	bodyParams := asMap(t, payload["bodyParams"])
	if bodyParams["api_key"] != "***" {
		t.Fatalf("expected api_key body field to be masked, got %#v", bodyParams["api_key"])
	}
	contextMap := asMap(t, payload["context"])
	if contextMap["password"] != "***" {
		t.Fatalf("expected custom context password to be masked, got %#v", contextMap["password"])
	}
}

func TestMiddlewareCapturesTransactionWhenEnabled(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
	})

	handler := Middleware(
		duck,
		WithCaptureTransactions(true),
		WithTransactionSampleRate(1),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://example.com/checkout", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}
	if provider.events[0].Type != duckbug.EventTypeTransaction {
		t.Fatalf("expected transaction event, got %s", provider.events[0].Type)
	}
	if provider.events[0].Payload["transaction"] != "POST /checkout" {
		t.Fatalf("unexpected transaction name: %#v", provider.events[0].Payload["transaction"])
	}
}

func TestMiddlewareSkipsIgnoredPaths(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
	})

	handler := Middleware(
		duck,
		WithCaptureTransactions(true),
		WithTransactionSampleRate(1),
		WithIgnoredPaths("/health"),
		WithIgnoredPathPrefixes("/ingest/"),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{"/health", "/ingest/project:key/errors"} {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if len(provider.events) != 0 {
		t.Fatalf("expected ignored paths to produce no events, got %d", len(provider.events))
	}
}

func TestMiddlewareSkipsInternalSDKRequests(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
	})

	handler := Middleware(
		duck,
		WithCaptureTransactions(true),
		WithCaptureHandled5xx(true),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://example.com/ingest/project:key/logs", nil)
	req.Header.Set("X-DuckBug-Internal", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(provider.events) != 0 {
		t.Fatalf("expected internal SDK request to be ignored, got %d events", len(provider.events))
	}
}

func TestMiddlewareCapturesHandled5xxWhenEnabled(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
	})

	handler := Middleware(
		duck,
		WithCaptureHandled5xx(true),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"db exploded"}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "https://example.com/checkout", nil)
	req.Header.Set("User-Agent", "duckbug-test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 response, got %d", rec.Code)
	}
	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}
	event := provider.events[0]
	if event.Type != duckbug.EventTypeError {
		t.Fatalf("expected error event, got %s", event.Type)
	}
	if got := event.Payload["message"]; got != "http 500: db exploded" {
		t.Fatalf("unexpected message: %#v", got)
	}
	if got := event.Payload["mechanism"]; got != "nethttp_response_5xx" {
		t.Fatalf("unexpected mechanism: %#v", got)
	}
	if got := event.Payload["handled"]; got != true {
		t.Fatalf("expected handled=true, got %#v", got)
	}
	contextMap := asMap(t, event.Payload["context"])
	if got := contextMap["path"]; got != "/checkout" {
		t.Fatalf("unexpected path: %#v", got)
	}
	if got := contextMap["responseMessage"]; got != "db exploded" {
		t.Fatalf("unexpected response message: %#v", got)
	}
}

func TestMiddlewareProductionDefaultsCaptureErrorAndTransactionOn5xx(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
	})

	handler := Middleware(
		duck,
		WithProductionDefaults(),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "https://example.com/orders", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 response, got %d", rec.Code)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.events) != 2 {
		t.Fatalf("expected two captured events, got %d", len(provider.events))
	}
	if provider.events[0].Type != duckbug.EventTypeError {
		t.Fatalf("expected first event to be error, got %s", provider.events[0].Type)
	}
	if provider.events[1].Type != duckbug.EventTypeTransaction {
		t.Fatalf("expected second event to be transaction, got %s", provider.events[1].Type)
	}
}

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	mapped, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", value)
	}
	return mapped
}
