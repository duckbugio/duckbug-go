package duckbug_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	duckbug "github.com/duckbugio/duckbug-go"
	"github.com/duckbugio/duckbug-go/pond"
	"github.com/santhosh-tekuri/jsonschema/v6"
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

func (p *captureProvider) LastEvent(t *testing.T) duckbug.Event {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) == 0 {
		t.Fatal("expected at least one captured event")
	}
	return p.events[len(p.events)-1]
}

func TestDuckBuildsLogPayloadMatchingSchema(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
		Pond: pond.New(pond.Config{
			EnvProvider: func() map[string]string {
				return map[string]string{
					"API_KEY": "super-secret",
					"APP_ENV": "test",
				}
			},
		}),
		Now: func() time.Time {
			return time.UnixMilli(1704067200000)
		},
		EventIDGenerator: func() string {
			return "550e8400-e29b-41d4-a716-446655440000"
		},
	})

	duck.SetTag("service", "checkout")
	duck.SetRelease("checkout@1.2.3")
	duck.SetEnvironment("production")
	duck.SetService("checkout")
	duck.SetRequestID("req-123")

	ctx := pond.WithRequestContext(context.Background(), pond.RequestContext{
		IP:     "203.0.113.10",
		URL:    "https://example.com/checkout?token=abc",
		Method: "POST",
		Headers: map[string]any{
			"Authorization": "Bearer super-secret",
		},
		QueryParams: map[string]any{
			"password": "123456",
		},
		BodyParams: map[string]any{
			"token":  "abc",
			"amount": 42,
		},
	})

	duck.LogContext(ctx, "warning", "Payment provider timeout", map[string]any{
		"password": "super-secret",
		"attempt":  2,
	})

	event := provider.LastEvent(t)
	if event.Type != duckbug.EventTypeLog {
		t.Fatalf("expected log event, got %s", event.Type)
	}

	if got := event.Payload["level"]; got != "WARN" {
		t.Fatalf("expected WARN level, got %#v", got)
	}

	contextMap := asMap(t, event.Payload["context"])
	if contextMap["password"] != "***" {
		t.Fatalf("expected sensitive context field to be masked, got %#v", contextMap["password"])
	}

	headers := asMap(t, event.Payload["headers"])
	if headers["Authorization"] != "***" {
		t.Fatalf("expected Authorization header to be masked, got %#v", headers["Authorization"])
	}

	queryParams := asMap(t, event.Payload["queryParams"])
	if queryParams["password"] != "***" {
		t.Fatalf("expected password query param to be masked, got %#v", queryParams["password"])
	}

	bodyParams := asMap(t, event.Payload["bodyParams"])
	if bodyParams["token"] != "***" {
		t.Fatalf("expected token body param to be masked, got %#v", bodyParams["token"])
	}

	env := asMap(t, event.Payload["env"])
	if env["API_KEY"] != "***" {
		t.Fatalf("expected API_KEY env to be masked, got %#v", env["API_KEY"])
	}
	if env["APP_ENV"] != "test" {
		t.Fatalf("expected APP_ENV env to be preserved, got %#v", env["APP_ENV"])
	}

	validateAgainstSchema(t, filepath.Join("schemas", "log-event.schema.json"), event.Payload)
}

func TestDuckBuildsErrorPayloadMatchingSchema(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
		Pond: pond.New(pond.Config{
			EnvProvider: func() map[string]string { return nil },
		}),
		Now: func() time.Time {
			return time.UnixMilli(1704067200000)
		},
		EventIDGenerator: func() string {
			return "550e8400-e29b-41d4-a716-446655440000"
		},
	})

	ctx := pond.WithRequestContext(context.Background(), pond.RequestContext{
		Headers: map[string]any{"Authorization": "Bearer super-secret"},
	})

	duck.QuackContextDetails(ctx, errors.New("division by zero"), map[string]any{
		"token": "secret-token",
	}, false, "manual_test")

	event := provider.LastEvent(t)
	if event.Type != duckbug.EventTypeError {
		t.Fatalf("expected error event, got %s", event.Type)
	}

	if got := event.Payload["message"]; got != "division by zero" {
		t.Fatalf("unexpected message: %#v", got)
	}
	if got := event.Payload["mechanism"]; got != "manual_test" {
		t.Fatalf("unexpected mechanism: %#v", got)
	}
	if _, ok := event.Payload["stacktrace"]; !ok {
		t.Fatal("expected stacktrace to be present")
	}
	if file, ok := event.Payload["file"].(string); !ok || file == "" {
		t.Fatalf("expected non-empty file, got %#v", event.Payload["file"])
	}
	if _, ok := event.Payload["line"].(float64); !ok {
		t.Fatalf("expected numeric line after JSON roundtrip, got %#v", event.Payload["line"])
	}

	details := asMap(t, event.Payload["context"])
	if details["token"] != "***" {
		t.Fatalf("expected sensitive token to be masked, got %#v", details["token"])
	}

	validateAgainstSchema(t, filepath.Join("schemas", "error-event.schema.json"), event.Payload)
}

func TestDuckBuildsTransactionPayload(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
		Pond: pond.New(pond.Config{
			EnvProvider: func() map[string]string { return nil },
		}),
		Now: func() time.Time {
			return time.UnixMilli(1704067200000)
		},
		EventIDGenerator: func() string {
			return "550e8400-e29b-41d4-a716-446655440000"
		},
	})

	transaction := duck.StartTransaction("GET /checkout", "http.server")
	if transaction == nil {
		t.Fatal("expected non-nil transaction")
	}
	transaction.SetContext(map[string]any{
		"token": "secret-token",
		"route": "/checkout",
	})
	transaction.AddMeasurement("http.response.status_code", 200, "code")
	transaction.AddMeasurement("db.rows", 3, "")
	child := transaction.StartChild("db.query", "select order")
	child.SetData(map[string]any{
		"password": "secret-password",
		"sql":      "select * from orders",
	})
	child.Finish("ok", transaction.StartTimestampMs()+11)
	transaction.Finish("ok", transaction.StartTimestampMs()+25)

	duck.CaptureTransaction(transaction)

	event := provider.LastEvent(t)
	if event.Type != duckbug.EventTypeTransaction {
		t.Fatalf("expected transaction event, got %s", event.Type)
	}
	if _, ok := event.Payload["time"]; ok {
		t.Fatalf("did not expect time field in transaction payload, got %#v", event.Payload["time"])
	}
	if event.Payload["transaction"] != "GET /checkout" {
		t.Fatalf("unexpected transaction name: %#v", event.Payload["transaction"])
	}
	if event.Payload["op"] != "http.server" {
		t.Fatalf("unexpected operation: %#v", event.Payload["op"])
	}
	if event.Payload["status"] != "ok" {
		t.Fatalf("unexpected transaction status: %#v", event.Payload["status"])
	}
	if traceID, ok := event.Payload["traceId"].(string); !ok || traceID == "" {
		t.Fatalf("expected non-empty traceId, got %#v", event.Payload["traceId"])
	}
	if spanID, ok := event.Payload["spanId"].(string); !ok || spanID == "" {
		t.Fatalf("expected non-empty spanId, got %#v", event.Payload["spanId"])
	}
	contextMap := asMap(t, event.Payload["context"])
	if contextMap["token"] != "***" {
		t.Fatalf("expected sensitive transaction context field to be masked, got %#v", contextMap["token"])
	}
	measurements := asMap(t, event.Payload["measurements"])
	statusCodeMeasurement := asMap(t, measurements["http.response.status_code"])
	if statusCodeMeasurement["value"] != float64(200) {
		t.Fatalf("unexpected measurement value: %#v", statusCodeMeasurement["value"])
	}
	if statusCodeMeasurement["unit"] != "code" {
		t.Fatalf("unexpected measurement unit: %#v", statusCodeMeasurement["unit"])
	}
	spans, ok := event.Payload["spans"].([]any)
	if !ok || len(spans) != 1 {
		t.Fatalf("expected one child span, got %#v", event.Payload["spans"])
	}
	childSpan := asMap(t, spans[0])
	data := asMap(t, childSpan["data"])
	if data["password"] != "***" {
		t.Fatalf("expected sensitive child span data to be masked, got %#v", data["password"])
	}
}

func validateAgainstSchema(t *testing.T, schemaPath string, payload map[string]any) {
	t.Helper()

	compiler := jsonschema.NewCompiler()
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema %s: %v", schemaPath, err)
	}
	if err := schema.Validate(payload); err != nil {
		t.Fatalf("payload does not match schema %s: %v", schemaPath, err)
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
