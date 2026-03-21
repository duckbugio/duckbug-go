package nethttp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/duckbugio/duckbug-go"
	"github.com/duckbugio/duckbug-go/pond"
)

type Config struct {
	CapturePanics         bool
	Repanic               bool
	ReadBody              bool
	MaxBodyBytes          int64
	CaptureTransactions   bool
	TransactionSampleRate float64
	IgnoredPaths          []string
	IgnoredPathPrefixes   []string
	SkipRequest           func(*http.Request) bool
}

type Option func(*Config)

func Middleware(duck *duckbug.Duck, options ...Option) func(http.Handler) http.Handler {
	config := Config{
		CapturePanics:         true,
		ReadBody:              false,
		MaxBodyBytes:          64 << 10,
		CaptureTransactions:   false,
		TransactionSampleRate: 1,
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
			if shouldIgnoreRequest(r, config) {
				next.ServeHTTP(w, r)
				return
			}

			reqCtx := buildRequestContext(r, config)
			ctx := pond.WithRequestContext(r.Context(), reqCtx)
			r = r.WithContext(ctx)

			tracker := &responseWriterTracker{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}
			tx := startTransaction(duck, r, config)
			defer func() {
				if recovered := recover(); recovered != nil {
					tracker.statusCode = http.StatusInternalServerError
					if duck != nil && config.CapturePanics {
						duck.CaptureRecoveredPanicContext(r.Context(), recovered, "nethttp_middleware")
					}
					captureTransaction(duck, r, tracker.statusCode, tx, true, config)
					if config.Repanic {
						panic(recovered)
					}
					if !tracker.wroteHeader {
						http.Error(tracker, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					}
				}
			}()

			next.ServeHTTP(tracker, r)
			captureTransaction(duck, r, tracker.statusCode, tx, false, config)
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

func WithCaptureTransactions(enabled bool) Option {
	return func(config *Config) {
		config.CaptureTransactions = enabled
	}
}

func WithTransactionSampleRate(rate float64) Option {
	return func(config *Config) {
		config.TransactionSampleRate = rate
	}
}

func WithIgnoredPaths(paths ...string) Option {
	return func(config *Config) {
		config.IgnoredPaths = append(config.IgnoredPaths, paths...)
	}
}

func WithIgnoredPathPrefixes(prefixes ...string) Option {
	return func(config *Config) {
		config.IgnoredPathPrefixes = append(config.IgnoredPathPrefixes, prefixes...)
	}
}

func WithSkipRequest(fn func(*http.Request) bool) Option {
	return func(config *Config) {
		config.SkipRequest = fn
	}
}

type responseWriterTracker struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (w *responseWriterTracker) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriterTracker) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.statusCode = http.StatusOK
	}
	w.wroteHeader = true
	return w.ResponseWriter.Write(data)
}

func (w *responseWriterTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func (w *responseWriterTracker) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
}

func (w *responseWriterTracker) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *responseWriterTracker) ReadFrom(r io.Reader) (int64, error) {
	readerFrom, ok := w.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(w.ResponseWriter, r)
	}
	return readerFrom.ReadFrom(r)
}

func (w *responseWriterTracker) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func shouldIgnoreRequest(r *http.Request, config Config) bool {
	if r == nil {
		return true
	}
	if config.SkipRequest != nil && config.SkipRequest(r) {
		return true
	}

	path := strings.TrimSpace(r.URL.Path)
	for _, item := range config.IgnoredPaths {
		if path == strings.TrimSpace(item) {
			return true
		}
	}
	for _, item := range config.IgnoredPathPrefixes {
		prefix := strings.TrimSpace(item)
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func startTransaction(duck *duckbug.Duck, r *http.Request, config Config) *duckbug.Transaction {
	if duck == nil || !config.CaptureTransactions || isWebsocketRequest(r) {
		return nil
	}

	path := "/"
	if r != nil && r.URL != nil && strings.TrimSpace(r.URL.Path) != "" {
		path = strings.TrimSpace(r.URL.Path)
	}
	method := http.MethodGet
	if r != nil && strings.TrimSpace(r.Method) != "" {
		method = strings.TrimSpace(r.Method)
	}
	userAgent := ""
	if r != nil {
		userAgent = strings.TrimSpace(r.Header.Get("User-Agent"))
	}

	tx := duck.StartTransaction(method+" "+path, "http.server")
	tx.SetContext(map[string]any{
		"method":    method,
		"path":      path,
		"userAgent": userAgent,
	})
	return tx
}

func captureTransaction(
	duck *duckbug.Duck,
	r *http.Request,
	statusCode int,
	tx *duckbug.Transaction,
	force bool,
	config Config,
) {
	if duck == nil || tx == nil {
		return
	}
	if !force && !shouldCaptureTransaction(config.TransactionSampleRate, statusCode) {
		return
	}

	tx.AddMeasurement("http.response.status_code", statusCode, "code")
	tx.Finish(transactionStatusFromHTTPStatus(statusCode))
	duck.CaptureTransactionContext(r.Context(), tx)
}

func shouldCaptureTransaction(sampleRate float64, statusCode int) bool {
	if statusCode >= http.StatusInternalServerError {
		return true
	}
	switch {
	case sampleRate <= 0:
		return false
	case sampleRate >= 1:
		return true
	default:
		return rand.Float64() < sampleRate
	}
}

func transactionStatusFromHTTPStatus(statusCode int) string {
	switch {
	case statusCode >= http.StatusInternalServerError:
		return "internal_error"
	case statusCode >= http.StatusBadRequest:
		return "invalid_argument"
	default:
		return "ok"
	}
}

func isWebsocketRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
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
