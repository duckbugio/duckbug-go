package duckbugprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
