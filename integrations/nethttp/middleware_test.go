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

	handler := Middleware(duck)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		duck.CaptureLogContext(r.Context(), "info", "request received", map[string]any{
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

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	mapped, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", value)
	}
	return mapped
}
