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

	logger := slog.New(NewHandler(duck, nil, WithMinLevel(slog.LevelInfo))).With("service", "api")
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

func TestHandlerSkipsBelowMinLevelByDefault(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
	})

	logger := slog.New(NewHandler(duck, nil))
	logger.Info("hello")

	if len(provider.events) != 0 {
		t.Fatalf("expected no captured events below default min level, got %d", len(provider.events))
	}
}

func TestWithUnparsedMinLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level string
		want  slog.Level
	}{
		{
			name:  "parses valid level",
			level: slog.LevelDebug.String(),
			want:  slog.LevelDebug,
		},
		{
			name:  "trims whitespace",
			level: " " + slog.LevelError.String() + " ",
			want:  slog.LevelError,
		},
		{
			name:  "defaults empty level to info",
			level: "   ",
			want:  slog.LevelInfo,
		},
		{
			name:  "defaults invalid level to info",
			level: "not-a-level",
			want:  slog.LevelInfo,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := NewHandler(nil, nil, WithMinLevelString(tt.level))

			if handler.minLvl != tt.want {
				t.Fatalf("expected min level %v, got %v", tt.want, handler.minLvl)
			}
		})
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
