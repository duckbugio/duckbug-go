package slogduckbug

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

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

func TestHandlerCapturesSlogRecord(t *testing.T) {
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
	})

	logger := slog.New(NewHandler(duck, nil)).With("service", "api")
	logger.Info("hello", "count", 2)

	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}

	payload := provider.events[0].Payload
	if payload["message"] != "hello" {
		t.Fatalf("unexpected message: %#v", payload["message"])
	}
	if payload["level"] != "INFO" {
		t.Fatalf("unexpected level: %#v", payload["level"])
	}
	contextMap := asMap(t, payload["context"])
	if contextMap["service"] != "api" {
		t.Fatalf("expected inherited slog attr, got %#v", contextMap["service"])
	}
	if contextMap["count"] != float64(2) {
		t.Fatalf("expected count=2, got %#v", contextMap["count"])
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
