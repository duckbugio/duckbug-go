package nethttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/duckbugio/duckbug-go"
	"github.com/duckbugio/duckbug-go/pond"
)

type Config struct {
	CapturePanics bool
	Repanic       bool
	ReadBody      bool
	MaxBodyBytes  int64
}

type Option func(*Config)

func Middleware(duck *duckbug.Duck, options ...Option) func(http.Handler) http.Handler {
	config := Config{
		CapturePanics: true,
		ReadBody:      true,
		MaxBodyBytes:  64 << 10,
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

	return func(next http.Handler) http.Handler {
		if next == nil {
			next = http.NotFoundHandler()
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqCtx := buildRequestContext(r, config)
			ctx := pond.WithRequestContext(r.Context(), reqCtx)
			r = r.WithContext(ctx)

			if duck == nil || !config.CapturePanics {
				next.ServeHTTP(w, r)
				return
			}

			tracker := &responseWriterTracker{ResponseWriter: w}
			defer func() {
				if recovered := recover(); recovered != nil {
					duck.CaptureRecoveredPanicContext(r.Context(), recovered, "nethttp_middleware")
					if config.Repanic {
						panic(recovered)
					}
					if !tracker.wroteHeader {
						http.Error(tracker, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					}
				}
			}()

			next.ServeHTTP(tracker, r)
		})
	}
}

func WithCapturePanics(enabled bool) Option {
	return func(config *Config) {
		config.CapturePanics = enabled
	}
}

func WithRepanic(enabled bool) Option {
	return func(config *Config) {
		config.Repanic = enabled
	}
}

func WithReadBody(enabled bool) Option {
	return func(config *Config) {
		config.ReadBody = enabled
	}
}

func WithMaxBodyBytes(limit int64) Option {
	return func(config *Config) {
		config.MaxBodyBytes = limit
	}
}

type responseWriterTracker struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *responseWriterTracker) WriteHeader(statusCode int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriterTracker) Write(data []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(data)
}

func buildRequestContext(r *http.Request, config Config) pond.RequestContext {
	return pond.RequestContext{
		IP:          clientIP(r),
		URL:         requestURL(r),
		Method:      strings.TrimSpace(r.Method),
		Headers:     headersToMap(r.Header),
		QueryParams: valuesToMap(r.URL.Query()),
		BodyParams:  bodyParams(r, config),
		Cookies:     cookiesToMap(r.Cookies()),
	}
}

func clientIP(r *http.Request) string {
	candidates := []string{
		r.Header.Get("CF-Connecting-IP"),
		r.Header.Get("X-Forwarded-For"),
		r.Header.Get("X-Real-IP"),
		r.Header.Get("Client-IP"),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, ",") {
			candidate = strings.TrimSpace(strings.Split(candidate, ",")[0])
		}
		if ip := net.ParseIP(candidate); ip != nil {
			return ip.String()
		}
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.RemoteAddr)); ip != nil {
		return ip.String()
	}
	return ""
}

func requestURL(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}

	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	if r.Host == "" {
		return r.URL.String()
	}

	cloned := *r.URL
	cloned.Scheme = scheme
	cloned.Host = r.Host
	return cloned.String()
}

func headersToMap(headers http.Header) map[string]any {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]any, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		if len(values) == 1 {
			out[key] = values[0]
			continue
		}
		items := make([]any, len(values))
		for i := range values {
			items[i] = values[i]
		}
		out[key] = items
	}
	return out
}

func valuesToMap(values url.Values) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, items := range values {
		if len(items) == 0 {
			continue
		}
		if len(items) == 1 {
			out[key] = items[0]
			continue
		}
		normalized := make([]any, len(items))
		for i := range items {
			normalized[i] = items[i]
		}
		out[key] = normalized
	}
	return out
}

func cookiesToMap(cookies []*http.Cookie) map[string]any {
	if len(cookies) == 0 {
		return nil
	}
	out := make(map[string]any, len(cookies))
	for _, item := range cookies {
		out[item.Name] = item.Value
	}
	return out
}

func bodyParams(r *http.Request, config Config) map[string]any {
	if !config.ReadBody || r == nil || r.Body == nil {
		return nil
	}
	if config.MaxBodyBytes <= 0 {
		return nil
	}
	if r.ContentLength <= 0 || r.ContentLength > config.MaxBodyBytes {
		return nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	switch {
	case strings.Contains(contentType, "application/json"):
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			return nil
		}
		if mapped, ok := decoded.(map[string]any); ok {
			return mapped
		}
	case strings.Contains(contentType, "application/x-www-form-urlencoded"):
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return nil
		}
		return valuesToMap(values)
	}

	return nil
}
