package duckbugprovider

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
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
	DisableAsync            bool
	QueueSize               int
	FlushInterval           time.Duration
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
	async                   bool
	queueSize               int
	flushInterval           time.Duration
	privacy                 PrivacyOptions
	beforeSend              BeforeSendFunc
	transportFailureHandler func(core.FailureInfo)

	mu      sync.Mutex
	buffers map[core.EventType][]map[string]any
	queue   chan queuedEvent
	pending sync.WaitGroup
	dropped atomic.Uint64
}

type queuedEvent struct {
	ctx       context.Context
	eventType core.EventType
	payload   map[string]any
}

func New(dsn string, options ...Option) *Provider {
	config := Config{
		DSN:               dsn,
		BatchSize:         1,
		QueueSize:         1024,
		FlushInterval:     2 * time.Second,
		Privacy:           DefaultPrivacyOptions(),
		Timeout:           2 * time.Second,
		ConnectionTimeout: 300 * time.Millisecond,
		MaxRetries:        0,
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
	queueSize := config.QueueSize
	if queueSize < 1 {
		queueSize = 1024
	}
	flushInterval := config.FlushInterval
	if flushInterval <= 0 {
		flushInterval = 2 * time.Second
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

	provider := &Provider{
		dsn:                     strings.TrimRight(strings.TrimSpace(config.DSN), "/"),
		transport:               transport,
		batchSize:               batchSize,
		async:                   !config.DisableAsync,
		queueSize:               queueSize,
		flushInterval:           flushInterval,
		privacy:                 config.Privacy,
		beforeSend:              config.BeforeSend,
		transportFailureHandler: config.TransportFailureHandler,
		buffers: map[core.EventType][]map[string]any{
			core.EventTypeError:       {},
			core.EventTypeLog:         {},
			core.EventTypeTransaction: {},
		},
	}
	if provider.async {
		provider.queue = make(chan queuedEvent, provider.queueSize)
		go provider.run()
	}
	return provider
}

func WithBatchSize(size int) Option {
	return func(config *Config) {
		config.BatchSize = size
	}
}

func WithAsync(enabled bool) Option {
	return func(config *Config) {
		config.DisableAsync = !enabled
	}
}

func WithQueueSize(size int) Option {
	return func(config *Config) {
		config.QueueSize = size
	}
}

func WithFlushInterval(interval time.Duration) Option {
	return func(config *Config) {
		config.FlushInterval = interval
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

	if p.async {
		p.enqueue(ctx, event.Type, payload)
		return
	}

	p.capturePrepared(ctx, event.Type, payload)
}

func (p *Provider) Flush(ctx context.Context) {
	if p == nil {
		return
	}
	if p.async {
		if ctx == nil {
			ctx = context.Background()
		}
		if !p.wait(ctx) {
			return
		}
	}
	p.flushType(ctx, core.EventTypeError)
	p.flushType(ctx, core.EventTypeLog)
}

func (p *Provider) run() {
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case item := <-p.queue:
			p.capturePrepared(item.ctx, item.eventType, item.payload)
			p.pending.Done()
		case <-ticker.C:
			p.flushType(context.Background(), core.EventTypeError)
			p.flushType(context.Background(), core.EventTypeLog)
		}
	}
}

func (p *Provider) enqueue(ctx context.Context, eventType core.EventType, payload map[string]any) {
	if ctx == nil {
		ctx = context.Background()
	}
	item := queuedEvent{
		ctx:       context.WithoutCancel(ctx),
		eventType: eventType,
		payload:   payload,
	}

	p.pending.Add(1)
	select {
	case p.queue <- item:
	default:
		p.pending.Done()
		p.handleDroppedEvent(eventType, payload)
	}
}

func (p *Provider) wait(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		p.pending.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *Provider) capturePrepared(ctx context.Context, eventType core.EventType, payload map[string]any) {
	if p.batchSize <= 1 || eventType == core.EventTypeTransaction {
		result := p.transport.Send(ctx, p.dsn, eventType, payload)
		p.handleTransportResult(eventType, []map[string]any{payload}, result)
		return
	}

	p.mu.Lock()
	p.buffers[eventType] = append(p.buffers[eventType], payload)
	shouldFlush := len(p.buffers[eventType]) >= p.batchSize
	p.mu.Unlock()

	if shouldFlush {
		p.flushType(ctx, eventType)
	}
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

	log.Printf("%s", message)
}

func (p *Provider) handleDroppedEvent(eventType core.EventType, payload map[string]any) {
	dropped := p.dropped.Add(1)
	result := core.TransportResult{
		ErrorMessage: "provider queue is full",
	}
	message := fmt.Sprintf(
		"[DuckBug] dropped %s event because provider queue is full (dropped=%d)",
		eventType,
		dropped,
	)
	info := core.FailureInfo{
		Type:    eventType,
		Items:   []map[string]any{payload},
		Result:  result,
		Message: message,
	}
	if p.transportFailureHandler != nil {
		p.transportFailureHandler(info)
		return
	}
	log.Printf("%s", message)
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
