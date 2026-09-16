package duckbugprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duckbugio/duckbug-go/core"
	"github.com/duckbugio/duckbug-go/internal/sdkrequest"
)

func TestHTTPTransportMarksInternalRequests(t *testing.T) {
	t.Parallel()

	gotHeader := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(sdkrequest.HeaderName)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPTransportConfig{
		Client: server.Client(),
	})

	result := transport.Send(context.Background(), server.URL+"/api/ingest/project:key", core.EventTypeLog, map[string]any{
		"eventId": "evt-1",
		"time":    1,
		"level":   "INFO",
		"message": "hello",
	})
	if result.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 status, got %d", result.StatusCode)
	}
	if gotHeader != sdkrequest.HeaderValue {
		t.Fatalf("expected internal header value %q, got %q", sdkrequest.HeaderValue, gotHeader)
	}
}

func TestHTTPTransportRetriesOnlyRecoverableStatuses(t *testing.T) {
	t.Parallel()

	const maxRetries = 2

	cases := []struct {
		name             string
		status           int
		expectedRequests int32
	}{
		{name: "too many requests", status: http.StatusTooManyRequests, expectedRequests: maxRetries + 1},
		{name: "internal server error", status: http.StatusInternalServerError, expectedRequests: maxRetries + 1},
		{name: "bad gateway", status: http.StatusBadGateway, expectedRequests: maxRetries + 1},
		{name: "service unavailable", status: http.StatusServiceUnavailable, expectedRequests: maxRetries + 1},
		{name: "gateway timeout", status: http.StatusGatewayTimeout, expectedRequests: maxRetries + 1},
		// 501 means the capability is not implemented in this installation:
		// repeating the request cannot change the answer.
		{name: "not implemented", status: http.StatusNotImplemented, expectedRequests: 1},
		{name: "bad request", status: http.StatusBadRequest, expectedRequests: 1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(testCase.status)
			}))
			defer server.Close()

			transport := NewHTTPTransport(HTTPTransportConfig{
				Client:     server.Client(),
				MaxRetries: maxRetries,
				RetryDelay: time.Millisecond,
			})

			result := transport.Send(context.Background(), server.URL+"/api/ingest/project:key", core.EventTypeLog, map[string]any{
				"eventId": "evt-1",
				"time":    1,
				"level":   "INFO",
				"message": "hello",
			})

			if result.StatusCode != testCase.status {
				t.Fatalf("expected status %d, got %d", testCase.status, result.StatusCode)
			}
			if got := requests.Load(); got != testCase.expectedRequests {
				t.Fatalf("expected %d request(s) for status %d, got %d", testCase.expectedRequests, testCase.status, got)
			}
			if int32(result.Attempts) != testCase.expectedRequests {
				t.Fatalf("expected %d reported attempt(s) for status %d, got %d", testCase.expectedRequests, testCase.status, result.Attempts)
			}
		})
	}
}
