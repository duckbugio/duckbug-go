package nethttp

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	duckbug "github.com/duckbugio/duckbug-go"
	"github.com/duckbugio/duckbug-go/pond"
)

func observerTestDuck(provider *captureProvider) *duckbug.Duck {
	return duckbug.NewDuck(duckbug.Config{
		Providers: []duckbug.Provider{provider},
		Pond:      pond.New(pond.Config{EnvProvider: func() map[string]string { return nil }}),
	})
}

func observerCapturedEvents(provider *captureProvider) []duckbug.Event {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]duckbug.Event(nil), provider.events...)
}

func assertObserverTransactionStatus(t *testing.T, provider *captureProvider, status int) {
	t.Helper()
	transactions := 0
	for _, event := range observerCapturedEvents(provider) {
		if event.Type != duckbug.EventTypeTransaction {
			continue
		}
		transactions++
		if status == 0 {
			if value := event.Payload["measurements"]; value != nil {
				if _, exists := asMap(t, value)["http.response.status_code"]; exists {
					t.Fatal("uncommitted response fabricated a status measurement")
				}
			}
			if event.Payload["status"] != "unknown_error" {
				t.Fatalf("unobserved status was reported as %v", event.Payload["status"])
			}
			continue
		}
		measurements := asMap(t, event.Payload["measurements"])
		measurement := asMap(t, measurements["http.response.status_code"])
		if measurement["value"] != float64(status) {
			t.Fatalf("expected status %d, got %v", status, measurement["value"])
		}
	}
	if transactions != 1 {
		t.Fatalf("expected one transaction, got %d", transactions)
	}
}

func TestMiddlewarePanicPreservesObservedResponse(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, repanic := range []bool{false, true} {
			provider := &captureProvider{}
			duck := observerTestDuck(provider)
			p := newResponseProbe()
			handler := Middleware(duck, WithRepanic(repanic), WithCaptureTransactions(true))(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if committed {
						_, _ = w.Write([]byte("accepted"))
					}
					panic("fixture panic")
				}))
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				handler.ServeHTTP(p, httptest.NewRequest(http.MethodGet, "http://fixture.test/", nil))
			}()
			if repanic != (recovered != nil) {
				t.Fatalf("repanic=%t recovered=%v", repanic, recovered)
			}
			status := http.StatusInternalServerError
			if committed {
				status = http.StatusOK
				if p.body.String() != "accepted" {
					t.Fatal("panic recovery appended a second response after commitment")
				}
			} else if repanic {
				status = 0
				if p.body.Len() != 0 || p.status != 0 {
					t.Fatal("repanic fabricated an outer recovery response")
				}
			}
			assertObserverTransactionStatus(t, provider, status)
			panics := 0
			for _, event := range observerCapturedEvents(provider) {
				if event.Type == duckbug.EventTypeError && event.Payload["mechanism"] == "nethttp_middleware" {
					panics++
				}
			}
			if panics != 1 {
				t.Fatalf("expected one panic incident, got %d", panics)
			}
		}
	}
}

func TestMiddlewareCapturesOnlyAcceptedResponsePrefix(t *testing.T) {
	for _, cap := range []int{0, 32} {
		provider := &captureProvider{}
		duck := observerTestDuck(provider)
		p := newResponseProbe()
		sentinel := errors.New("fixture write failed")
		p.limit, p.err = 3, sentinel
		handler := Middleware(duck, WithCaptureHandled5xx(true), WithCaptureTransactions(true), WithHandled5xxMaxBodyBytes(cap))(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				n, err := w.Write([]byte("abc-unaccepted-private"))
				if n != 3 || err != sentinel {
					t.Fatal("observer changed the original write result")
				}
			}))
		handler.ServeHTTP(p, httptest.NewRequest(http.MethodGet, "http://fixture.test/", nil))
		assertObserverTransactionStatus(t, provider, 500)
		handled := 0
		for _, event := range observerCapturedEvents(provider) {
			if event.Type != duckbug.EventTypeError {
				continue
			}
			handled++
			context := asMap(t, event.Payload["context"])
			if context["status"] != float64(500) || context["responseAcceptedBytes"] != float64(3) {
				t.Fatal("handled event disagrees with accepted response")
			}
			if strings.Contains(asString(context["responseBody"]), "unaccepted-private") {
				t.Fatal("attempted but unaccepted bytes entered the incident")
			}
			if cap == 0 {
				if context["responseBody"] != nil || context["responseMessage"] != nil {
					t.Fatal("disabled response capture copied a payload")
				}
			} else if context["responseBody"] != "abc" {
				t.Fatalf("expected accepted prefix, got %v", context["responseBody"])
			}
		}
		if handled != 1 {
			t.Fatalf("expected one handled failure, got %d", handled)
		}
	}
}

func asString(value any) string {
	result, _ := value.(string)
	return result
}

func TestMiddlewareDoesNotReportIgnoredFinalStatusAsFailure(t *testing.T) {
	provider := &captureProvider{}
	duck := observerTestDuck(provider)
	p := newResponseProbe()
	handler := Middleware(duck, WithCaptureHandled5xx(true), WithCaptureTransactions(true))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.WriteHeader(http.StatusInternalServerError)
		}))
	handler.ServeHTTP(p, httptest.NewRequest(http.MethodGet, "http://fixture.test/", nil))
	assertObserverTransactionStatus(t, provider, 200)
	for _, event := range observerCapturedEvents(provider) {
		if event.Type == duckbug.EventTypeError {
			t.Fatal("an ignored status attempt created a false handled error")
		}
	}
}

type unwrapOnlyResponseWriter struct{ http.ResponseWriter }

func (w unwrapOnlyResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestMiddlewareObservesControllerThroughInnerUnwrapWriter(t *testing.T) {
	provider := &captureProvider{}
	duck := observerTestDuck(provider)
	p := &capableResponseProbe{responseProbe: newResponseProbe()}
	handler := Middleware(duck, WithCaptureHandled5xx(true), WithCaptureTransactions(true))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			controller := http.NewResponseController(unwrapOnlyResponseWriter{w})
			if err := controller.Flush(); err != nil {
				t.Fatalf("flush: %v", err)
			}
			w.WriteHeader(http.StatusInternalServerError)
		}))
	handler.ServeHTTP(p, httptest.NewRequest(http.MethodGet, "http://fixture.test/", nil))
	if p.status != http.StatusOK || p.flushes != 1 {
		t.Fatalf("wire status=%d flushes=%d", p.status, p.flushes)
	}
	assertObserverTransactionStatus(t, provider, http.StatusOK)
	for _, event := range observerCapturedEvents(provider) {
		if event.Type == duckbug.EventTypeError {
			t.Fatal("controller flush created a false handled error")
		}
	}
}

func TestMiddlewarePanicAfterInnerControllerHijack(t *testing.T) {
	for _, repanic := range []bool{false, true} {
		provider := &captureProvider{}
		duck := observerTestDuck(provider)
		conn, peer := net.Pipe()
		buffer := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		p := &capableResponseProbe{responseProbe: newResponseProbe(), conn: conn, buffer: buffer}
		handler := Middleware(duck, WithRepanic(repanic), WithCaptureTransactions(true))(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				gotConn, gotBuffer, err := http.NewResponseController(unwrapOnlyResponseWriter{w}).Hijack()
				if err != nil || gotConn != conn || gotBuffer != buffer {
					t.Fatalf("hijack changed the connection, buffer or error: %v", err)
				}
				panic("fixture panic after hijack")
			}))
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			handler.ServeHTTP(p, httptest.NewRequest(http.MethodGet, "http://fixture.test/", nil))
		}()
		_ = conn.Close()
		_ = peer.Close()
		if repanic != (recovered != nil) || !p.wasHijacked || p.status != 0 || p.body.Len() != 0 {
			t.Fatalf("repanic=%t recovered=%v hijacked=%t status=%d bytes=%d", repanic, recovered, p.wasHijacked, p.status, p.body.Len())
		}
		assertObserverTransactionStatus(t, provider, 0)
	}
}

func TestMiddlewarePanicAfterInformationalResponse(t *testing.T) {
	for _, repanic := range []bool{false, true} {
		provider := &captureProvider{}
		duck := observerTestDuck(provider)
		p := newResponseProbe()
		handler := Middleware(duck, WithRepanic(repanic), WithCaptureTransactions(true))(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusEarlyHints)
				panic("fixture panic after early hints")
			}))
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			handler.ServeHTTP(p, httptest.NewRequest(http.MethodGet, "http://fixture.test/", nil))
		}()
		if repanic != (recovered != nil) || len(p.codes) == 0 || p.codes[0] != http.StatusEarlyHints {
			t.Fatal("informational response or panic propagation changed")
		}
		status := http.StatusInternalServerError
		if repanic {
			status = 0
		}
		if p.status != status {
			t.Fatalf("expected final status %d, got %d", status, p.status)
		}
		assertObserverTransactionStatus(t, provider, status)
	}
}
