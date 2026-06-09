package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

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

func newDuck(provider *captureProvider) *duckbug.Duck {
	return duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
		Pond: pond.New(pond.Config{
			EnvProvider: func() map[string]string { return nil },
		}),
	})
}

func TestUnaryInterceptorCapturesServerError(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	interceptor := UnaryServerInterceptor(newDuck(provider))

	info := &grpc.UnaryServerInfo{FullMethod: "/checkout.Checkout/Pay"}
	handler := func(_ context.Context, _ any) (any, error) {
		return nil, status.Error(codes.Internal, "db exploded")
	}

	_, err := interceptor(context.Background(), nil, info, handler)
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected error to pass through, got %v", err)
	}

	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}
	event := provider.events[0]
	if event.Type != duckbug.EventTypeError {
		t.Fatalf("expected error event, got %s", event.Type)
	}
	if got := event.Payload["message"]; got != "grpc Internal: /checkout.Checkout/Pay" {
		t.Fatalf("unexpected message: %#v", got)
	}
	if got := event.Payload["mechanism"]; got != "grpc_status" {
		t.Fatalf("unexpected mechanism: %#v", got)
	}
	contextMap := asMap(t, event.Payload["context"])
	if got := contextMap["grpcCode"]; got != "Internal" {
		t.Fatalf("unexpected grpcCode: %#v", got)
	}
	if got := contextMap["responseMessage"]; got != "db exploded" {
		t.Fatalf("unexpected response message: %#v", got)
	}
}

func TestUnaryInterceptorIgnoresClientError(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	interceptor := UnaryServerInterceptor(newDuck(provider))

	info := &grpc.UnaryServerInfo{FullMethod: "/checkout.Checkout/Pay"}
	handler := func(_ context.Context, _ any) (any, error) {
		return nil, status.Error(codes.InvalidArgument, "bad input")
	}

	if _, err := interceptor(context.Background(), nil, info, handler); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected error to pass through, got %v", err)
	}
	if len(provider.events) != 0 {
		t.Fatalf("expected client errors to be ignored, got %d events", len(provider.events))
	}
}

func TestUnaryInterceptorCapturesPanic(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	interceptor := UnaryServerInterceptor(newDuck(provider))

	info := &grpc.UnaryServerInfo{FullMethod: "/checkout.Checkout/Pay"}
	handler := func(_ context.Context, _ any) (any, error) {
		panic("boom")
	}

	resp, err := interceptor(context.Background(), nil, info, handler)
	if resp != nil {
		t.Fatalf("expected nil response after panic, got %#v", resp)
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal status after panic, got %v", err)
	}
	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}
	if got := provider.events[0].Payload["handled"]; got != false {
		t.Fatalf("expected handled=false for panic, got %#v", got)
	}
}

func TestUnaryInterceptorAttachesRequestContext(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	duck := newDuck(provider)
	interceptor := UnaryServerInterceptor(duck, WithCaptureMetadata(true))

	info := &grpc.UnaryServerInfo{FullMethod: "/checkout.Checkout/Pay"}
	handler := func(ctx context.Context, _ any) (any, error) {
		duck.LogContext(ctx, "info", "rpc received", map[string]any{"password": "123456"})
		return "ok", nil
	}

	md := metadata.New(map[string]string{"authorization": "Bearer secret", "x-tenant": "acme"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	if _, err := interceptor(ctx, nil, info, handler); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}

	payload := provider.events[0].Payload
	if payload["url"] != "/checkout.Checkout/Pay" {
		t.Fatalf("unexpected url: %#v", payload["url"])
	}
	headers := asMap(t, payload["headers"])
	if headers["authorization"] != "***" {
		t.Fatalf("expected authorization metadata to be masked, got %#v", headers["authorization"])
	}
	if headers["x-tenant"] != "acme" {
		t.Fatalf("expected x-tenant metadata to be preserved, got %#v", headers["x-tenant"])
	}
	contextMap := asMap(t, payload["context"])
	if contextMap["password"] != "***" {
		t.Fatalf("expected custom log password to be masked, got %#v", contextMap["password"])
	}
}

func TestUnaryInterceptorSkipsIgnoredMethods(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	interceptor := UnaryServerInterceptor(
		newDuck(provider),
		WithIgnoredMethods("/grpc.health.v1.Health/Check"),
	)

	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	handler := func(_ context.Context, _ any) (any, error) {
		return nil, status.Error(codes.Internal, "should be ignored")
	}

	if _, err := interceptor(context.Background(), nil, info, handler); err == nil {
		t.Fatal("expected handler error to pass through")
	}
	if len(provider.events) != 0 {
		t.Fatalf("expected ignored method to produce no events, got %d", len(provider.events))
	}
}

func TestUnaryInterceptorCapturesTransaction(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	interceptor := UnaryServerInterceptor(
		newDuck(provider),
		WithCaptureTransactions(true),
		WithTransactionSampleRate(1),
	)

	info := &grpc.UnaryServerInfo{FullMethod: "/checkout.Checkout/Pay"}
	handler := func(_ context.Context, _ any) (any, error) {
		return "ok", nil
	}

	if _, err := interceptor(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.events) != 1 {
		t.Fatalf("expected one captured event, got %d", len(provider.events))
	}
	if provider.events[0].Type != duckbug.EventTypeTransaction {
		t.Fatalf("expected transaction event, got %s", provider.events[0].Type)
	}
	if provider.events[0].Payload["transaction"] != "/checkout.Checkout/Pay" {
		t.Fatalf("unexpected transaction name: %#v", provider.events[0].Payload["transaction"])
	}
}

func TestUnaryInterceptorCapturesNonStatusError(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	interceptor := UnaryServerInterceptor(newDuck(provider))

	info := &grpc.UnaryServerInfo{FullMethod: "/checkout.Checkout/Pay"}
	handler := func(_ context.Context, _ any) (any, error) {
		return nil, errors.New("plain failure")
	}

	if _, err := interceptor(context.Background(), nil, info, handler); err == nil {
		t.Fatal("expected error to pass through")
	}
	if len(provider.events) != 1 {
		t.Fatalf("expected plain error to be captured as Unknown, got %d events", len(provider.events))
	}
	if got := provider.events[0].Payload["message"]; got != "grpc Unknown: /checkout.Checkout/Pay" {
		t.Fatalf("unexpected message: %#v", got)
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
