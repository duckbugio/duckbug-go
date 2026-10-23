package nethttp

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/felixge/httpsnoop"
)

type responseWriterTracker struct {
	writer           http.ResponseWriter
	statusCode       int
	wroteHeader      bool
	hijacked         bool
	acceptedBytes    int64
	captureResponse  bool
	maxResponseBytes int
	responseBody     strings.Builder
	truncated        bool
}

func newResponseWriterTracker(w http.ResponseWriter, config Config) *responseWriterTracker {
	// shortcut: outer Unwrap-only writers can bypass hooks; add a controller bridge if arbitrary outer chains are required.
	t := &responseWriterTracker{captureResponse: config.CaptureHandled5xx, maxResponseBytes: config.Handled5xxMaxBodyBytes}
	t.writer = httpsnoop.Wrap(w, httpsnoop.Hooks{
		WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
			return func(code int) {
				if t.wroteHeader || t.hijacked {
					return
				}
				next(code)
				if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
					return
				}
				t.statusCode, t.wroteHeader = code, true
			}
		},
		Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
			return func(data []byte) (int, error) {
				n, err := next(data)
				err = t.recordWrite(n, len(data), err)
				if n > 0 && !t.hijacked {
					t.captureBody(data[:n])
				}
				return n, err
			}
		},
		WriteString: func(next httpsnoop.WriteStringFunc) httpsnoop.WriteStringFunc {
			return func(data string) (int, error) {
				n, err := next(data)
				err = t.recordWrite(n, len(data), err)
				if n > 0 && !t.hijacked && t.captureResponse && !t.truncated {
					remaining := max(0, t.maxResponseBytes-t.responseBody.Len())
					t.captureBody([]byte(data[:min(n, remaining)]))
					if n > remaining {
						t.truncated = true
					}
				}
				return n, err
			}
		},
		ReadFrom: func(httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
			return func(src io.Reader) (int64, error) {
				// Observe accepted writes; the underlying zero-copy path bypasses them.
				return io.Copy(struct{ io.Writer }{t.writer}, src)
			}
		},
		Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc {
			return func() { next(); t.commitImplicit() }
		},
		FlushError: func(next httpsnoop.FlushErrorFunc) httpsnoop.FlushErrorFunc {
			return func() error {
				err := next()
				if !errors.Is(err, http.ErrNotSupported) && !errors.Is(err, http.ErrHijacked) {
					t.commitImplicit()
				}
				return err
			}
		},
		Hijack: func(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
			return func() (net.Conn, *bufio.ReadWriter, error) {
				conn, buffer, err := next()
				if err == nil {
					t.hijacked = true
				}
				return conn, buffer, err
			}
		},
	})
	return t
}

func (t *responseWriterTracker) recordWrite(n, requested int, err error) error {
	if !errors.Is(err, http.ErrHijacked) && !t.hijacked {
		t.commitImplicit()
		t.acceptedBytes += int64(n)
	}
	if n < requested && err == nil {
		return io.ErrShortWrite
	}
	return err
}

func (t *responseWriterTracker) commitImplicit() {
	if !t.wroteHeader && !t.hijacked {
		t.statusCode, t.wroteHeader = http.StatusOK, true
	}
}

func (t *responseWriterTracker) responseStatus(finished bool) int {
	if !t.wroteHeader && finished && !t.hijacked {
		return http.StatusOK
	}
	return t.statusCode
}

func (t *responseWriterTracker) captureBody(data []byte) {
	if !t.captureResponse || len(data) == 0 || t.truncated {
		return
	}
	remaining := t.maxResponseBytes - t.responseBody.Len()
	if remaining <= 0 {
		t.truncated = true
		return
	}
	if len(data) > remaining {
		_, _ = t.responseBody.Write(data[:remaining])
		t.truncated = true
		return
	}
	_, _ = t.responseBody.Write(data)
}

func (t *responseWriterTracker) responseSnippet() string {
	body := strings.TrimSpace(t.responseBody.String())
	if body == "" {
		return ""
	}
	if t.truncated {
		return body + "...(truncated)"
	}
	return body
}
