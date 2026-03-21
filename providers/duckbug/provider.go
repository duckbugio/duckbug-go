package duckbugprovider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/duckbugio/duckbug-go/core"
)

type BeforeSendFunc func(eventType core.EventType, payload map[string]any) (map[string]any, bool)

type Transport interface {
	Send(ctx context.Context, dsn string, eventType core.EventType, data map[string]any) core.TransportResult
	SendBatch(ctx context.Context, dsn string, eventType core.EventType, items []map[string]any) core.TransportResult
}

type PrivacyOptions struct {
	CaptureRequestContext bool
	CaptureHeaders        bool
	CaptureBody           bool
	CaptureSession        bool
	CaptureCookies        bool
	CaptureFiles          bool
	CaptureEnv            bool
}

func DefaultPrivacyOptions() PrivacyOptions {
	return PrivacyOptions{
		CaptureRequestContext: true,
		CaptureHeaders:        true,
		CaptureBody:           true,
		CaptureSession:        true,
		CaptureCookies:        true,
		CaptureFiles:          true,
		CaptureEnv:            false,
	}
}

type Config struct {
	DSN                     string
	Transport               Transport
	BatchSize               int
	Privacy                 PrivacyOptions
	BeforeSend              BeforeSendFunc
	TransportFailureHandler func(core.FailureInfo)
	Timeout                 time.Duration
	ConnectionTimeout       time.Duration
	MaxRetries              int
	RetryDelay              time.Duration
}

type Option func(*Config)

type Provider struct {
	dsn                     string
	transport               Transport
	batchSize               int
	privacy                 PrivacyOptions
	beforeSend              BeforeSendFunc
	transportFailureHandler func(core.FailureInfo)

	mu      sync.Mutex
	buffers map[core.EventType][]map[string]any
}

func New(dsn string, options ...Option) *Provider {
	config := Config{
		DSN:               dsn,
		BatchSize:         1,
		Privacy:           DefaultPrivacyOptions(),
		Timeout:           5 * time.Second,
		ConnectionTimeout: 3 * time.Second,
		MaxRetries:        2,
		RetryDelay:        100 * time.Millisecond,
	}

	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

	return NewWithConfig(config)
}

func NewWithConfig(config Config) *Provider {
	batchSize := config.BatchSize
	if batchSize < 1 {
		batchSize = 1
	}

	transport := config.Transport
	if transport == nil {
		transport = NewHTTPTransport(HTTPTransportConfig{
			Timeout:           config.Timeout,
			ConnectionTimeout: config.ConnectionTimeout,
			MaxRetries:        config.MaxRetries,
			RetryDelay:        config.RetryDelay,
		})
	}

	return &Provider{
		dsn:                     strings.TrimRight(strings.TrimSpace(config.DSN), "/"),
		transport:               transport,
		batchSize:               batchSize,
		privacy:                 config.Privacy,
		beforeSend:              config.BeforeSend,
		transportFailureHandler: config.TransportFailureHandler,
		buffers: map[core.EventType][]map[string]any{
			core.EventTypeError:       {},
			core.EventTypeLog:         {},
			core.EventTypeTransaction: {},
		},
	}
}

func WithBatchSize(size int) Option {
	return func(config *Config) {
		config.BatchSize = size
	}
}

func WithPrivacy(options PrivacyOptions) Option {
	return func(config *Config) {
		config.Privacy = options
	}
}

func WithBeforeSend(fn BeforeSendFunc) Option {
	return func(config *Config) {
		config.BeforeSend = fn
	}
}

func WithTransportFailureHandler(fn func(core.FailureInfo)) Option {
	return func(config *Config) {
		config.TransportFailureHandler = fn
	}
}

func WithTransport(transport Transport) Option {
	return func(config *Config) {
		config.Transport = transport
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(config *Config) {
		config.Timeout = timeout
	}
}

func WithConnectionTimeout(timeout time.Duration) Option {
	return func(config *Config) {
		config.ConnectionTimeout = timeout
	}
}

func WithMaxRetries(maxRetries int) Option {
	return func(config *Config) {
		config.MaxRetries = maxRetries
	}
}

func WithRetryDelay(delay time.Duration) Option {
	return func(config *Config) {
		config.RetryDelay = delay
	}
}

func (p *Provider) CaptureEvent(ctx context.Context, event core.Event) {
	if p == nil || p.transport == nil {
		return
	}
	if event.Type != core.EventTypeError && event.Type != core.EventTypeLog && event.Type != core.EventTypeTransaction {
		return
	}

	payload, ok := p.preparePayload(event.Type, event.Payload)
	if !ok {
		return
	}

	if p.batchSize <= 1 {
		result := p.transport.Send(ctx, p.dsn, event.Type, payload)
		p.handleTransportResult(event.Type, []map[string]any{payload}, result)
		return
	}

	if event.Type == core.EventTypeTransaction {
		result := p.transport.Send(ctx, p.dsn, event.Type, payload)
		p.handleTransportResult(event.Type, []map[string]any{payload}, result)
		return
	}

	p.mu.Lock()
	p.buffers[event.Type] = append(p.buffers[event.Type], payload)
	shouldFlush := len(p.buffers[event.Type]) >= p.batchSize
	p.mu.Unlock()

	if shouldFlush {
		p.flushType(ctx, event.Type)
	}
}

func (p *Provider) Flush(ctx context.Context) {
	if p == nil {
		return
	}
	p.flushType(ctx, core.EventTypeError)
	p.flushType(ctx, core.EventTypeLog)
}

func (p *Provider) preparePayload(eventType core.EventType, payload map[string]any) (map[string]any, bool) {
	if len(payload) == 0 {
		return nil, false
	}

	prepared := cloneMap(payload)
	applyPrivacy(prepared, p.privacy)
	prepared = stripNilValues(prepared)

	if p.beforeSend != nil {
		updated, keep := p.beforeSend(eventType, cloneMap(prepared))
		if !keep || len(updated) == 0 {
			return nil, false
		}
		prepared = stripNilValues(cloneMap(updated))
	}

	if len(prepared) == 0 {
		return nil, false
	}

	return prepared, true
}

func (p *Provider) flushType(ctx context.Context, eventType core.EventType) {
	p.mu.Lock()
	items := append([]map[string]any(nil), p.buffers[eventType]...)
	p.buffers[eventType] = nil
	p.mu.Unlock()

	if len(items) == 0 {
		return
	}

	var result core.TransportResult
	if len(items) == 1 {
		result = p.transport.Send(ctx, p.dsn, eventType, items[0])
	} else {
		result = p.transport.SendBatch(ctx, p.dsn, eventType, items)
	}

	p.handleTransportResult(eventType, items, result)
}

func (p *Provider) handleTransportResult(eventType core.EventType, items []map[string]any, result core.TransportResult) {
	if result.IsSuccess() {
		return
	}

	message := fmt.Sprintf(
		"[DuckBug] transport failed for %s (%d item(s), status=%d, attempts=%d, duplicate=%t, error=%s)",
		eventType,
		len(items),
		result.StatusCode,
		result.Attempts,
		result.Duplicate,
		emptyIfBlank(result.ErrorMessage, "unknown"),
	)
	if body := strings.TrimSpace(result.ResponseBody); body != "" {
		message += ": " + body
	}

	info := core.FailureInfo{
		Type:    eventType,
		Items:   items,
		Result:  result,
		Message: message,
	}
	if p.transportFailureHandler != nil {
		p.transportFailureHandler(info)
		return
	}

	slog.Default().Error(message)
}

func applyPrivacy(payload map[string]any, options PrivacyOptions) {
	if payload == nil {
		return
	}

	if !options.CaptureRequestContext {
		delete(payload, "ip")
		delete(payload, "url")
		delete(payload, "method")
		delete(payload, "headers")
		delete(payload, "queryParams")
		delete(payload, "bodyParams")
		delete(payload, "cookies")
		delete(payload, "session")
		delete(payload, "files")
	} else {
		if !options.CaptureHeaders {
			delete(payload, "headers")
		}
		if !options.CaptureBody {
			delete(payload, "bodyParams")
		}
		if !options.CaptureCookies {
			delete(payload, "cookies")
		}
		if !options.CaptureSession {
			delete(payload, "session")
		}
		if !options.CaptureFiles {
			delete(payload, "files")
		}
	}

	if !options.CaptureEnv {
		delete(payload, "env")
	}
}

func stripNilValues(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return nil
	}

	out := make(map[string]any, len(payload))
	for key, value := range payload {
		if value == nil {
			continue
		}

		switch typed := value.(type) {
		case map[string]any:
			if cleaned := stripNilValues(typed); len(cleaned) > 0 {
				out[key] = cleaned
			}
		case []any:
			cleaned := make([]any, 0, len(typed))
			for _, item := range typed {
				if item == nil {
					continue
				}
				if mapped, ok := item.(map[string]any); ok {
					if nested := stripNilValues(mapped); len(nested) > 0 {
						cleaned = append(cleaned, nested)
					}
					continue
				}
				cleaned = append(cleaned, item)
			}
			if len(cleaned) > 0 {
				out[key] = cleaned
			}
		default:
			out[key] = value
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}

	out := make(map[string]any, len(value))
	for key, item := range value {
		switch typed := item.(type) {
		case map[string]any:
			out[key] = cloneMap(typed)
		case []any:
			cloned := make([]any, len(typed))
			for i := range typed {
				if mapped, ok := typed[i].(map[string]any); ok {
					cloned[i] = cloneMap(mapped)
				} else {
					cloned[i] = typed[i]
				}
			}
			out[key] = cloned
		default:
			out[key] = item
		}
	}

	return out
}

func emptyIfBlank(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
