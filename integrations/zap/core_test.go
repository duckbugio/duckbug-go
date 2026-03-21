package zapduckbug

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	duckbug "github.com/duckbugio/duckbug-go"
	"github.com/duckbugio/duckbug-go/pond"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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

func TestCoreCapturesZapEntry(t *testing.T) {
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

	observedCore, observedLogs := observer.New(zapcore.DebugLevel)
	logger := zap.New(NewCore(duck, observedCore, WithMinLevel(zapcore.InfoLevel))).With(
		zap.String("service", "api"),
	)
	logger.Info("hello", zap.Int("count", 2))

	if observedLogs.Len() != 1 {
		t.Fatalf("expected downstream zap core to receive one log, got %d", observedLogs.Len())
	}
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
		t.Fatalf("expected inherited zap field, got %#v", contextMap["service"])
	}
	if contextMap["count"] != float64(2) {
		t.Fatalf("expected count=2, got %#v", contextMap["count"])
	}
}

func TestCoreSkipsBelowMinLevelByDefault(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
	})

	observedCore, observedLogs := observer.New(zapcore.DebugLevel)
	logger := zap.New(NewCore(duck, observedCore))
	logger.Info("hello")

	if observedLogs.Len() != 1 {
		t.Fatalf("expected downstream zap core to receive one log, got %d", observedLogs.Len())
	}
	if len(provider.events) != 0 {
		t.Fatalf("expected no captured events below default min level, got %d", len(provider.events))
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
