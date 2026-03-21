package core

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/duckbugio/duckbug-go/pond"
)

type Duck struct {
	providersMu      sync.RWMutex
	providers        []Provider
	scope            *scope
	pond             *pond.Pond
	now              func() time.Time
	eventIDGenerator func() string
}

func NewDuck(config Config) *Duck {
	p := config.Pond
	if p == nil {
		p = pond.Ripple(nil)
	}

	nowFn := config.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	eventIDGenerator := config.EventIDGenerator
	if eventIDGenerator == nil {
		eventIDGenerator = defaultEventID
	}

	platform := normalizeString(config.Platform)
	if platform == "" {
		platform = "go"
	}

	sdk := cloneMapStringAny(config.SDK)
	if len(sdk) == 0 {
		sdk = map[string]any{"name": "duckbug-go"}
	} else if _, ok := sdk["name"]; !ok {
		sdk["name"] = "duckbug-go"
	}

	runtimeInfo := cloneMapStringAny(config.Runtime)
	if len(runtimeInfo) == 0 {
		runtimeInfo = map[string]any{
			"language": "go",
			"version":  runtime.Version(),
		}
	}

	return &Duck{
		providers:        append([]Provider(nil), config.Providers...),
		scope:            newScope(scopeSnapshot{SDK: sdk, Runtime: runtimeInfo, Platform: platform}),
		pond:             p,
		now:              nowFn,
		eventIDGenerator: eventIDGenerator,
	}
}

func (d *Duck) RegisterProvider(provider Provider) {
	if d == nil || provider == nil {
		return
	}

	d.providersMu.Lock()
	defer d.providersMu.Unlock()
	d.providers = append(d.providers, provider)
}

func (d *Duck) Pond() *pond.Pond {
	if d == nil {
		return nil
	}
	return d.pond
}

func (d *Duck) Quack(err error) {
	d.QuackContext(context.Background(), err)
}

func (d *Duck) QuackContext(ctx context.Context, err error) {
	d.captureErrorAt(ctx, err, nil, true, "manual", time.Time{}, "")
}

func (d *Duck) CaptureError(err error) {
	d.Quack(err)
}

func (d *Duck) CaptureErrorContext(ctx context.Context, err error) {
	d.QuackContext(ctx, err)
}

func (d *Duck) CaptureErrorDetails(err error, details any, handled bool, mechanism string) {
	d.CaptureErrorContextDetails(context.Background(), err, details, handled, mechanism)
}

func (d *Duck) CaptureErrorContextDetails(ctx context.Context, err error, details any, handled bool, mechanism string) {
	d.captureErrorAt(ctx, err, details, handled, mechanism, time.Time{}, "")
}

func (d *Duck) CaptureRecoveredPanicContext(ctx context.Context, recovered any, mechanism string) {
	if recovered == nil {
		return
	}
	if mechanism == "" {
		mechanism = "panic_recover"
	}

	d.captureErrorAt(
		ctx,
		fmt.Errorf("panic: %v", recovered),
		map[string]any{"panic": normalizeValue(recovered)},
		false,
		mechanism,
		time.Time{},
		string(debug.Stack()),
	)
}

func (d *Duck) CaptureLog(level any, message string, details any) {
	d.CaptureLogContextAt(context.Background(), time.Time{}, level, message, details)
}

func (d *Duck) CaptureLogContext(ctx context.Context, level any, message string, details any) {
	d.CaptureLogContextAt(ctx, time.Time{}, level, message, details)
}

func (d *Duck) CaptureLogContextAt(ctx context.Context, at time.Time, level any, message string, details any) {
	if d == nil {
		return
	}

	eventTime := at
	if eventTime.IsZero() {
		eventTime = d.now()
	}

	payload := d.buildCommonPayload(ctx, eventTime)
	payload["level"] = string(NormalizeLevel(level))
	payload["message"] = message
	if details != nil {
		payload["context"] = d.pond.SanitizeValue(normalizeValue(details))
	}

	d.dispatch(ctx, NewEvent(EventTypeLog, payload))
}

func (d *Duck) Debug(message string, details any) {
	d.CaptureLog(LevelDebug, message, details)
}

func (d *Duck) Info(message string, details any) {
	d.CaptureLog(LevelInfo, message, details)
}

func (d *Duck) Warn(message string, details any) {
	d.CaptureLog(LevelWarn, message, details)
}

func (d *Duck) Error(message string, details any) {
	d.CaptureLog(LevelError, message, details)
}

func (d *Duck) Fatal(message string, details any) {
	d.CaptureLog(LevelFatal, message, details)
}

func (d *Duck) StartTransaction(name string, op string) *Transaction {
	if d == nil {
		return nil
	}

	return newTransaction(
		name,
		op,
		defaultTraceID(),
		defaultSpanID(),
		d.now().UnixMilli(),
		defaultSpanID,
		d.now,
	)
}

func (d *Duck) CaptureTransaction(transaction *Transaction) {
	d.CaptureTransactionContext(context.Background(), transaction)
}

func (d *Duck) CaptureTransactionContext(ctx context.Context, transaction *Transaction) {
	if d == nil || transaction == nil {
		return
	}

	payload := d.buildTransactionPayload(ctx, transaction)
	d.dispatch(ctx, NewEvent(EventTypeTransaction, payload))
}

func (d *Duck) Flush(ctx context.Context) {
	if d == nil {
		return
	}

	for _, provider := range d.getProviders() {
		flushable, ok := provider.(FlushableProvider)
		if !ok {
			continue
		}
		flushable.Flush(ctx)
	}
}

func (d *Duck) SetTag(key string, value any) {
	if d == nil {
		return
	}
	d.scope.setTag(key, value)
}

func (d *Duck) SetTags(tags map[string]any) {
	if d == nil {
		return
	}
	d.scope.setTags(tags)
}

func (d *Duck) ClearTags() {
	if d == nil {
		return
	}
	d.scope.clearTags()
}

func (d *Duck) SetUser(user map[string]any) {
	if d == nil {
		return
	}
	d.scope.setUser(user)
}

func (d *Duck) ClearUser() {
	if d == nil {
		return
	}
	d.scope.clearUser()
}

func (d *Duck) SetRelease(value string) {
	if d == nil {
		return
	}
	d.scope.setRelease(value)
}

func (d *Duck) SetEnvironment(value string) {
	if d == nil {
		return
	}
	d.scope.setEnvironment(value)
}

func (d *Duck) SetDist(value string) {
	if d == nil {
		return
	}
	d.scope.setDist(value)
}

func (d *Duck) SetServerName(value string) {
	if d == nil {
		return
	}
	d.scope.setServerName(value)
}

func (d *Duck) SetService(value string) {
	if d == nil {
		return
	}
	d.scope.setService(value)
}

func (d *Duck) SetRequestID(value string) {
	if d == nil {
		return
	}
	d.scope.setRequestID(value)
}

func (d *Duck) SetTransaction(value string) {
	if d == nil {
		return
	}
	d.scope.setTransaction(value)
}

func (d *Duck) SetTrace(traceID, spanID string) {
	if d == nil {
		return
	}
	d.scope.setTrace(traceID, spanID)
}

func (d *Duck) SetFingerprint(value string) {
	if d == nil {
		return
	}
	d.scope.setFingerprint(value)
}

func (d *Duck) SetSDK(value map[string]any) {
	if d == nil {
		return
	}

	sdk := cloneMapStringAny(value)
	if len(sdk) == 0 {
		sdk = map[string]any{"name": "duckbug-go"}
	} else if _, ok := sdk["name"]; !ok {
		sdk["name"] = "duckbug-go"
	}
	d.scope.setSDK(sdk)
}

func (d *Duck) SetRuntime(value map[string]any) {
	if d == nil {
		return
	}

	runtimeInfo := cloneMapStringAny(value)
	if len(runtimeInfo) == 0 {
		runtimeInfo = map[string]any{
			"language": "go",
			"version":  runtime.Version(),
		}
	}
	d.scope.setRuntime(runtimeInfo)
}

func (d *Duck) SetExtra(value map[string]any) {
	if d == nil {
		return
	}
	d.scope.setExtra(value)
}

func (d *Duck) SetPlatform(value string) {
	if d == nil {
		return
	}
	d.scope.setPlatform(value)
}

func (d *Duck) AddBreadcrumb(breadcrumb map[string]any) {
	if d == nil {
		return
	}
	d.scope.addBreadcrumb(breadcrumb)
}

func (d *Duck) ClearBreadcrumbs() {
	if d == nil {
		return
	}
	d.scope.clearBreadcrumbs()
}

func (d *Duck) captureErrorAt(
	ctx context.Context,
	err error,
	details any,
	handled bool,
	mechanism string,
	at time.Time,
	stackOverride string,
) {
	if d == nil || err == nil {
		return
	}

	eventTime := at
	if eventTime.IsZero() {
		eventTime = d.now()
	}

	stacktrace, file, line, stacktraceAsString := buildErrorStack(4)
	if stackOverride != "" {
		stacktraceAsString = stackOverride
	}

	payload := d.buildCommonPayload(ctx, eventTime)
	payload["message"] = err.Error()
	payload["exception"] = d.pond.SanitizeValue(buildExceptionValue(err))
	payload["stacktrace"] = stacktrace
	payload["stacktraceAsString"] = stacktraceAsString
	payload["file"] = file
	payload["line"] = line
	payload["handled"] = handled
	if normalizedMechanism := normalizeString(mechanism); normalizedMechanism != "" {
		payload["mechanism"] = normalizedMechanism
	}
	if details != nil {
		payload["context"] = d.pond.SanitizeValue(normalizeValue(details))
	}

	d.dispatch(ctx, NewEvent(EventTypeError, payload))
}

func (d *Duck) buildCommonPayload(ctx context.Context, at time.Time) map[string]any {
	payload := d.buildBasePayload(ctx)
	payload["time"] = at.UnixMilli()
	return payload
}

func (d *Duck) buildBasePayload(ctx context.Context) map[string]any {
	if d == nil {
		return nil
	}

	snapshot := d.scope.snapshot()
	collected := d.pond.Collect(ctx)

	payload := map[string]any{
		"eventId": d.eventIDGenerator(),
	}

	if len(snapshot.Tags) > 0 {
		payload["dTags"] = cloneStringSlice(snapshot.Tags)
	}
	if snapshot.User != nil {
		payload["user"] = d.pond.SanitizeValue(snapshot.User)
	}
	if snapshot.Release != "" {
		payload["release"] = snapshot.Release
	}
	if snapshot.Environment != "" {
		payload["environment"] = snapshot.Environment
	}
	if snapshot.Dist != "" {
		payload["dist"] = snapshot.Dist
	}
	if snapshot.ServerName != "" {
		payload["serverName"] = snapshot.ServerName
	}
	if snapshot.Service != "" {
		payload["service"] = snapshot.Service
	}
	if snapshot.RequestID != "" {
		payload["requestId"] = snapshot.RequestID
	}
	if snapshot.Transaction != "" {
		payload["transaction"] = snapshot.Transaction
	}
	if snapshot.TraceID != "" {
		payload["traceId"] = snapshot.TraceID
	}
	if snapshot.SpanID != "" {
		payload["spanId"] = snapshot.SpanID
	}
	if snapshot.Fingerprint != "" {
		payload["fingerprint"] = snapshot.Fingerprint
	}
	if len(snapshot.Breadcrumbs) > 0 {
		payload["breadcrumbs"] = d.pond.SanitizeValue(snapshot.Breadcrumbs)
	}
	if snapshot.SDK != nil {
		payload["sdk"] = d.pond.SanitizeValue(snapshot.SDK)
	}
	if snapshot.Runtime != nil {
		payload["runtime"] = d.pond.SanitizeValue(snapshot.Runtime)
	}
	if snapshot.Extra != nil {
		payload["extra"] = d.pond.SanitizeValue(snapshot.Extra)
	}
	if snapshot.Platform != "" {
		payload["platform"] = snapshot.Platform
	}

	if collected.IP != "" {
		payload["ip"] = collected.IP
	}
	if collected.URL != "" {
		payload["url"] = collected.URL
	}
	if collected.Method != "" {
		payload["method"] = collected.Method
	}
	if collected.Headers != nil {
		payload["headers"] = collected.Headers
	}
	if collected.QueryParams != nil {
		payload["queryParams"] = collected.QueryParams
	}
	if collected.BodyParams != nil {
		payload["bodyParams"] = collected.BodyParams
	}
	if collected.Cookies != nil {
		payload["cookies"] = collected.Cookies
	}
	if collected.Session != nil {
		payload["session"] = collected.Session
	}
	if collected.Files != nil {
		payload["files"] = collected.Files
	}
	if collected.Env != nil {
		payload["env"] = collected.Env
	}

	return payload
}

func (d *Duck) buildTransactionPayload(ctx context.Context, transaction *Transaction) map[string]any {
	payload := d.buildBasePayload(ctx)
	payload["traceId"] = transaction.TraceID()
	payload["spanId"] = transaction.SpanID()
	if parentSpanID := transaction.ParentSpanID(); parentSpanID != "" {
		payload["parentSpanId"] = parentSpanID
	}
	payload["transaction"] = transaction.Name()
	payload["op"] = transaction.Op()
	if status := transaction.Status(); status != "" {
		payload["status"] = status
	}
	if contextValue := transaction.Context(); contextValue != nil {
		payload["context"] = d.pond.SanitizeValue(contextValue)
	}
	if measurements := transaction.Measurements(); len(measurements) > 0 {
		payload["measurements"] = d.pond.SanitizeValue(measurements)
	}
	if spans := transaction.Spans(); len(spans) > 0 {
		spanPayloads := make([]any, 0, len(spans))
		for _, span := range spans {
			if span == nil {
				continue
			}
			spanPayloads = append(spanPayloads, d.pond.SanitizeValue(span.payload()))
		}
		if len(spanPayloads) > 0 {
			payload["spans"] = spanPayloads
		}
	}
	payload["startTime"] = transaction.StartTimestampMs()
	if endTimestampMs := transaction.EndTimestampMs(); endTimestampMs != nil {
		payload["endTime"] = *endTimestampMs
	} else {
		payload["endTime"] = transaction.StartTimestampMs()
	}
	payload["duration"] = transaction.DurationMs()

	return payload
}

func (d *Duck) dispatch(ctx context.Context, event Event) {
	for _, provider := range d.getProviders() {
		provider.CaptureEvent(ctx, event)
	}
}

func (d *Duck) getProviders() []Provider {
	d.providersMu.RLock()
	defer d.providersMu.RUnlock()
	return append([]Provider(nil), d.providers...)
}
