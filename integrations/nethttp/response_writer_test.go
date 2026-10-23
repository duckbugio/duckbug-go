package nethttp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
	"time"
)

type responseProbe struct {
	header http.Header
	codes  []int
	status int
	body   bytes.Buffer
	limit  int
	err    error
}

func newResponseProbe() *responseProbe       { return &responseProbe{header: make(http.Header), limit: -1} }
func (p *responseProbe) Header() http.Header { return p.header }
func (p *responseProbe) WriteHeader(code int) {
	if p.status != 0 {
		return
	}
	if code < 100 || code > 999 {
		panic("invalid fixture HTTP status")
	}
	p.codes = append(p.codes, code)
	if code >= 200 || code == 101 {
		p.status = code
	}
}
func (p *responseProbe) Write(data []byte) (int, error) {
	if p.status == 0 {
		p.WriteHeader(200)
	}
	n := len(data)
	if p.limit >= 0 {
		n = min(n, p.limit)
	}
	_, _ = p.body.Write(data[:n])
	return n, p.err
}

type capableResponseProbe struct {
	*responseProbe
	flushes, pushes, copies, strings int
	flushErr, hijackErr              error
	conn                             net.Conn
	buffer                           *bufio.ReadWriter
	readDeadline, writeDeadline      time.Time
	duplex                           bool
	wasHijacked                      bool
}

func (p *capableResponseProbe) Write(data []byte) (int, error) {
	if p.wasHijacked {
		return 0, http.ErrHijacked
	}
	return p.responseProbe.Write(data)
}

func (p *capableResponseProbe) Flush() { p.WriteHeader(200); p.flushes++ }
func (p *capableResponseProbe) FlushError() error {
	if !errors.Is(p.flushErr, http.ErrNotSupported) {
		p.Flush()
	}
	return p.flushErr
}
func (p *capableResponseProbe) Push(string, *http.PushOptions) error { p.pushes++; return nil }
func (p *capableResponseProbe) ReadFrom(src io.Reader) (int64, error) {
	p.copies++
	return io.Copy(p.responseProbe, src)
}
func (p *capableResponseProbe) WriteString(s string) (int, error) {
	p.strings++
	return p.Write([]byte(s))
}
func (p *capableResponseProbe) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if p.hijackErr == nil {
		p.wasHijacked = true
	}
	return p.conn, p.buffer, p.hijackErr
}
func (p *capableResponseProbe) SetReadDeadline(value time.Time) error {
	p.readDeadline = value
	return nil
}
func (p *capableResponseProbe) SetWriteDeadline(value time.Time) error {
	p.writeDeadline = value
	return nil
}
func (p *capableResponseProbe) EnableFullDuplex() error { p.duplex = true; return nil }

func observeProbe(p http.ResponseWriter, cap int) *responseWriterTracker {
	return newResponseWriterTracker(p, Config{CaptureHandled5xx: true, Handled5xxMaxBodyBytes: cap})
}

func TestResponseObserverFirstFinalStatus(t *testing.T) {
	for _, test := range []struct {
		name   string
		run    func(http.ResponseWriter)
		codes  []int
		status int
	}{
		{"explicit final", func(w http.ResponseWriter) { w.WriteHeader(200); w.WriteHeader(500) }, []int{200}, 200},
		{"implicit write", func(w http.ResponseWriter) { _, _ = w.Write([]byte("ok")); w.WriteHeader(500) }, []int{200}, 200},
		{"empty write commits", func(w http.ResponseWriter) { _, _ = w.Write(nil); w.WriteHeader(500) }, []int{200}, 200},
		{"informational", func(w http.ResponseWriter) { w.WriteHeader(100); w.WriteHeader(103); w.WriteHeader(201) }, []int{100, 103, 201}, 201},
		{"switching is terminal", func(w http.ResponseWriter) { w.WriteHeader(101); w.WriteHeader(500) }, []int{101}, 101},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := newResponseProbe()
			observer := observeProbe(p, 32)
			test.run(observer.writer)
			if p.status != test.status || observer.responseStatus(false) != test.status || !reflect.DeepEqual(p.codes, test.codes) {
				t.Fatalf("wire=%d observer=%d codes=%v", p.status, observer.statusCode, p.codes)
			}
		})
	}
	t.Run("invalid header does not commit", func(t *testing.T) {
		observer := observeProbe(newResponseProbe(), 32)
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected invalid-header panic")
				}
			}()
			observer.writer.WriteHeader(42)
		}()
		if observer.wroteHeader || observer.responseStatus(false) != 0 {
			t.Fatal("panicking header became an observed commitment")
		}
	})
}

func TestResponseObserverAcceptedPrefixAndCopyPaths(t *testing.T) {
	sentinel := errors.New("fixture write failed")
	for _, useString := range []bool{false, true} {
		for _, writeErr := range []error{nil, sentinel} {
			p := &capableResponseProbe{responseProbe: newResponseProbe()}
			p.limit, p.err = 2, writeErr
			observer := observeProbe(p, 32)
			var n int
			var err error
			if useString {
				n, err = io.WriteString(observer.writer, "abcdef")
			} else {
				n, err = observer.writer.Write([]byte("abcdef"))
			}
			expected := writeErr
			if expected == nil {
				expected = io.ErrShortWrite
			}
			if n != 2 || err != expected || observer.acceptedBytes != 2 || observer.responseSnippet() != "ab" || p.body.String() != "ab" {
				t.Fatalf("string=%t n=%d err=%v accepted=%d captured=%q", useString, n, err, observer.acceptedBytes, observer.responseSnippet())
			}
			if useString && p.strings != 1 {
				t.Fatal("StringWriter observation bypassed the supported underlying method")
			}
		}
	}
	t.Run("capture cap across writes", func(t *testing.T) {
		observer := observeProbe(newResponseProbe(), 5)
		_, _ = observer.writer.Write([]byte("abc"))
		_, _ = observer.writer.Write([]byte("defgh"))
		if observer.acceptedBytes != 8 || observer.responseSnippet() != "abcde...(truncated)" {
			t.Fatal("capture cap changed accepted byte accounting")
		}
	})
	for _, name := range []string{"empty", "source failure", "partial write"} {
		t.Run("ReaderFrom "+name, func(t *testing.T) {
			p := &capableResponseProbe{responseProbe: newResponseProbe()}
			observer := observeProbe(p, 32)
			var src io.Reader = struct{ io.Reader }{strings.NewReader("")}
			if name == "source failure" {
				src = observerReaderFunc(func([]byte) (int, error) { return 0, sentinel })
			}
			if name == "partial write" {
				src = struct{ io.Reader }{strings.NewReader("abcdef")}
				p.limit, p.err = 2, sentinel
			}
			n, err := io.Copy(observer.writer, src)
			if p.copies != 0 {
				t.Fatal("underlying ReaderFrom bypassed observing writes")
			}
			if name == "partial write" {
				if n != 2 || err != sentinel || observer.responseSnippet() != "ab" || observer.acceptedBytes != 2 {
					t.Fatalf("n=%d err=%v captured=%q", n, err, observer.responseSnippet())
				}
				return
			}
			if n != 0 || observer.wroteHeader || observer.responseStatus(false) != 0 || observer.responseStatus(true) != 200 {
				t.Fatal("empty/failed source committed a response")
			}
			if name == "source failure" && err != sentinel {
				t.Fatal("source error identity changed")
			}
		})
	}
}

type observerReaderFunc func([]byte) (int, error)

func (f observerReaderFunc) Read(p []byte) (int, error) { return f(p) }

func TestResponseObserverOptionalInterfacesAndHijack(t *testing.T) {
	minimal := observeProbe(newResponseProbe(), 32)
	if _, ok := minimal.writer.(http.Flusher); ok {
		t.Fatal("invented Flusher")
	}
	if _, ok := minimal.writer.(http.Hijacker); ok {
		t.Fatal("invented Hijacker")
	}
	if _, ok := minimal.writer.(http.Pusher); ok {
		t.Fatal("invented Pusher")
	}
	if _, ok := minimal.writer.(io.ReaderFrom); ok {
		t.Fatal("invented ReaderFrom")
	}
	if _, ok := minimal.writer.(io.StringWriter); ok {
		t.Fatal("invented StringWriter")
	}
	if unwrapped, ok := minimal.writer.(interface{ Unwrap() http.ResponseWriter }); !ok || unwrapped.Unwrap() == nil {
		t.Fatal("underlying writer cannot be unwrapped")
	}
	recorder := httptest.NewRecorder()
	flusher := observeProbe(recorder, 32)
	flusher.writer.(http.Flusher).Flush()
	flusher.writer.WriteHeader(500)
	if recorder.Code != 200 || flusher.statusCode != 200 {
		t.Fatal("Flusher-only commitment was lost")
	}
	if err := http.NewResponseController(minimal.writer).Flush(); !errors.Is(err, http.ErrNotSupported) || minimal.wroteHeader {
		t.Fatal("unsupported flush appeared successful")
	}
	p := &capableResponseProbe{responseProbe: newResponseProbe()}
	observer := observeProbe(p, 32)
	if err := observer.writer.(http.Pusher).Push("/asset", nil); err != nil || observer.wroteHeader {
		t.Fatal("push committed its parent")
	}
	controller := http.NewResponseController(observer.writer)
	deadline := time.Now().Add(time.Minute)
	if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline) != nil || controller.EnableFullDuplex() != nil || !p.duplex || p.readDeadline != deadline || p.writeDeadline != deadline {
		t.Fatal("controller capabilities were lost")
	}
	if err := controller.Flush(); err != nil || p.flushes != 1 || observer.responseStatus(false) != 200 {
		t.Fatal("FlushError did not establish implicit status")
	}
	observer.writer.WriteHeader(500)
	if p.status != 200 || observer.statusCode != 200 {
		t.Fatal("late status overwrote flush commitment")
	}
	unsupported := &capableResponseProbe{responseProbe: newResponseProbe(), flushErr: http.ErrNotSupported}
	noFlush := observeProbe(unsupported, 32)
	if err := http.NewResponseController(noFlush.writer).Flush(); !errors.Is(err, http.ErrNotSupported) || noFlush.wroteHeader {
		t.Fatal("unsupported FlushError committed")
	}
	t.Run("successful hijack", func(t *testing.T) {
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		p := &capableResponseProbe{responseProbe: newResponseProbe(), conn: left, buffer: bufio.NewReadWriter(bufio.NewReader(left), bufio.NewWriter(left))}
		observer := observeProbe(p, 32)
		conn, buffer, err := observer.writer.(http.Hijacker).Hijack()
		if err != nil || conn != left || buffer != p.buffer || !observer.hijacked || observer.responseStatus(true) != 0 {
			t.Fatal("hijack identity/status changed")
		}
		if n, err := observer.writer.Write([]byte("rejected")); n != 0 || err != http.ErrHijacked || observer.wroteHeader || observer.acceptedBytes != 0 || observer.responseSnippet() != "" {
			t.Fatal("rejected write after hijack fabricated HTTP commitment or bytes")
		}
	})
	t.Run("failed hijack", func(t *testing.T) {
		sentinel := errors.New("hijack failed")
		p := &capableResponseProbe{responseProbe: newResponseProbe(), hijackErr: sentinel}
		observer := observeProbe(p, 32)
		_, _, err := observer.writer.(http.Hijacker).Hijack()
		if err != sentinel || observer.hijacked {
			t.Fatal("failed hijack changed state")
		}
		observer.writer.WriteHeader(201)
		if observer.statusCode != 201 {
			t.Fatal("failed hijack prevented response")
		}
	})
}

func TestMiddlewareInformationalResponseOnRealWire(t *testing.T) {
	provider := &captureProvider{}
	duck := observerTestDuck(provider)
	server := httptest.NewServer(Middleware(duck, WithCaptureTransactions(true), WithCaptureHandled5xx(true))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(103)
		w.WriteHeader(201)
		_, _ = io.WriteString(w, "created")
	})))
	defer server.Close()
	var informational []int
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error { informational = append(informational, code); return nil }}))
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 201 || string(body) != "created" || !reflect.DeepEqual(informational, []int{103}) {
		t.Fatalf("status=%d body=%q informational=%v", response.StatusCode, body, informational)
	}
	assertObserverTransactionStatus(t, provider, 201)
}
