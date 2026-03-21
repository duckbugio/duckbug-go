package duckbugprovider

import (
	"context"
	"sync"
	"testing"

	"github.com/duckbugio/duckbug-go/core"
)

type fakeTransport struct {
	mu             sync.Mutex
	sendCalls      int
	sendBatchCalls int
	lastPayload    map[string]any
	lastBatch      []map[string]any
	sendResult     core.TransportResult
	batchResult    core.TransportResult
}

func (t *fakeTransport) Send(_ context.Context, _ string, _ core.EventType, data map[string]any) core.TransportResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sendCalls++
	t.lastPayload = cloneMap(data)
	return t.sendResult
}

func (t *fakeTransport) SendBatch(_ context.Context, _ string, _ core.EventType, items []map[string]any) core.TransportResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sendBatchCalls++
	t.lastBatch = append([]map[string]any(nil), items...)
	return t.batchResult
}

func TestProviderFlushesBatchOnBatchSize(t *testing.T) {
	t.Parallel()

	transport := &fakeTransport{batchResult: core.TransportResult{StatusCode: 201}}
	provider := New(
		"https://duckbug.local/api/ingest/project:key",
		WithTransport(transport),
		WithBatchSize(2),
	)

	provider.CaptureEvent(context.Background(), core.NewEvent(core.EventTypeLog, map[string]any{
		"eventId": "a",
		"time":    1,
		"level":   "INFO",
		"message": "first",
	}))
	provider.CaptureEvent(context.Background(), core.NewEvent(core.EventTypeLog, map[string]any{
		"eventId": "b",
		"time":    2,
		"level":   "INFO",
		"message": "second",
	}))

	if transport.sendCalls != 0 {
		t.Fatalf("expected no single sends, got %d", transport.sendCalls)
	}
	if transport.sendBatchCalls != 1 {
		t.Fatalf("expected one batch send, got %d", transport.sendBatchCalls)
	}
	if len(transport.lastBatch) != 2 {
		t.Fatalf("expected two batch items, got %d", len(transport.lastBatch))
	}
}

func TestProviderSendsTransactionImmediatelyEvenWhenBatchEnabled(t *testing.T) {
	t.Parallel()

	transport := &fakeTransport{sendResult: core.TransportResult{StatusCode: 201}}
	provider := New(
		"https://duckbug.local/api/ingest/project:key",
		WithTransport(transport),
		WithBatchSize(10),
	)

	provider.CaptureEvent(context.Background(), core.NewEvent(core.EventTypeTransaction, map[string]any{
		"eventId":     "a",
		"traceId":     "trace-1",
		"spanId":      "span-1",
		"transaction": "GET /checkout",
		"op":          "http.server",
		"startTime":   1,
		"endTime":     2,
		"duration":    1,
	}))

	if transport.sendCalls != 1 {
		t.Fatalf("expected one single send for transaction, got %d", transport.sendCalls)
	}
	if transport.sendBatchCalls != 0 {
		t.Fatalf("expected no batch send for transaction, got %d", transport.sendBatchCalls)
	}
}

func TestProviderAppliesPrivacyAndBeforeSend(t *testing.T) {
	t.Parallel()

	transport := &fakeTransport{sendResult: core.TransportResult{StatusCode: 201}}
	provider := New(
		"https://duckbug.local/api/ingest/project:key",
		WithTransport(transport),
		WithPrivacy(PrivacyOptions{
			CaptureRequestContext: true,
			CaptureHeaders:        false,
			CaptureBody:           false,
			CaptureSession:        false,
			CaptureCookies:        false,
			CaptureFiles:          false,
			CaptureEnv:            false,
		}),
		WithBeforeSend(func(eventType core.EventType, payload map[string]any) (map[string]any, bool) {
			payload["sdk"] = map[string]any{"name": "duckbug-go"}
			return payload, true
		}),
	)

	provider.CaptureEvent(context.Background(), core.NewEvent(core.EventTypeLog, map[string]any{
		"eventId":     "a",
		"time":        1,
		"level":       "INFO",
		"message":     "first",
		"headers":     map[string]any{"Authorization": "***"},
		"bodyParams":  map[string]any{"token": "***"},
		"cookies":     map[string]any{"sid": "1"},
		"session":     map[string]any{"user": "42"},
		"files":       map[string]any{"avatar": "a.png"},
		"env":         map[string]any{"API_KEY": "***"},
		"queryParams": map[string]any{"page": 1},
	}))

	if transport.sendCalls != 1 {
		t.Fatalf("expected one single send, got %d", transport.sendCalls)
	}
	if _, ok := transport.lastPayload["headers"]; ok {
		t.Fatal("expected headers to be removed by privacy config")
	}
	if _, ok := transport.lastPayload["bodyParams"]; ok {
		t.Fatal("expected bodyParams to be removed by privacy config")
	}
	if _, ok := transport.lastPayload["env"]; ok {
		t.Fatal("expected env to be removed by privacy config")
	}
	if _, ok := transport.lastPayload["queryParams"]; !ok {
		t.Fatal("expected queryParams to be preserved")
	}
	if sdk, ok := transport.lastPayload["sdk"].(map[string]any); !ok || sdk["name"] != "duckbug-go" {
		t.Fatalf("expected beforeSend mutation to survive, got %#v", transport.lastPayload["sdk"])
	}
}

func TestProviderDuplicateConflictDoesNotCallFailureHandler(t *testing.T) {
	t.Parallel()

	transport := &fakeTransport{
		sendResult: core.TransportResult{
			StatusCode: 409,
			Duplicate:  true,
		},
	}
	failures := 0
	provider := New(
		"https://duckbug.local/api/ingest/project:key",
		WithTransport(transport),
		WithTransportFailureHandler(func(info core.FailureInfo) {
			failures++
		}),
	)

	provider.CaptureEvent(context.Background(), core.NewEvent(core.EventTypeError, map[string]any{
		"eventId":    "a",
		"time":       1,
		"message":    "boom",
		"stacktrace": []any{},
		"file":       "main.go",
		"line":       1,
	}))

	if failures != 0 {
		t.Fatalf("expected duplicate conflict to be treated as success, got %d failures", failures)
	}
}

func TestProviderCallsFailureHandlerOnTransportError(t *testing.T) {
	t.Parallel()

	transport := &fakeTransport{
		sendResult: core.TransportResult{
			StatusCode:   500,
			ResponseBody: "backend failed",
			Attempts:     1,
		},
	}
	var captured core.FailureInfo
	provider := New(
		"https://duckbug.local/api/ingest/project:key",
		WithTransport(transport),
		WithTransportFailureHandler(func(info core.FailureInfo) {
			captured = info
		}),
	)

	provider.CaptureEvent(context.Background(), core.NewEvent(core.EventTypeError, map[string]any{
		"eventId":    "a",
		"time":       1,
		"message":    "boom",
		"stacktrace": []any{},
		"file":       "main.go",
		"line":       1,
	}))

	if captured.Type != core.EventTypeError {
		t.Fatalf("expected failure info for error event, got %#v", captured.Type)
	}
	if captured.Result.StatusCode != 500 {
		t.Fatalf("expected status 500, got %d", captured.Result.StatusCode)
	}
	if captured.Message == "" {
		t.Fatal("expected non-empty failure message")
	}
}
