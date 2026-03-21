package core

import "time"

type Transaction struct {
	name             string
	op               string
	traceID          string
	spanID           string
	parentSpanID     string
	startTimestampMs int64
	endTimestampMs   *int64
	status           string
	context          any
	measurements     map[string]any
	spans            []*Span
	spanIDGenerator  func() string
	now              func() time.Time
}

type Span struct {
	traceID          string
	spanID           string
	parentSpanID     string
	op               string
	description      string
	startTimestampMs int64
	endTimestampMs   *int64
	status           string
	data             map[string]any
	now              func() time.Time
}

func newTransaction(
	name string,
	op string,
	traceID string,
	spanID string,
	startTimestampMs int64,
	spanIDGenerator func() string,
	now func() time.Time,
) *Transaction {
	if normalizeString(name) == "" {
		name = "transaction"
	}
	if normalizeString(op) == "" {
		op = "custom"
	}
	if traceID == "" {
		traceID = defaultTraceID()
	}
	if spanID == "" {
		spanID = defaultSpanID()
	}
	if spanIDGenerator == nil {
		spanIDGenerator = defaultSpanID
	}
	if now == nil {
		now = time.Now
	}

	return &Transaction{
		name:             normalizeString(name),
		op:               normalizeString(op),
		traceID:          traceID,
		spanID:           spanID,
		startTimestampMs: startTimestampMs,
		spanIDGenerator:  spanIDGenerator,
		now:              now,
	}
}

func (t *Transaction) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}

func (t *Transaction) Op() string {
	if t == nil {
		return ""
	}
	return t.op
}

func (t *Transaction) TraceID() string {
	if t == nil {
		return ""
	}
	return t.traceID
}

func (t *Transaction) SpanID() string {
	if t == nil {
		return ""
	}
	return t.spanID
}

func (t *Transaction) ParentSpanID() string {
	if t == nil {
		return ""
	}
	return t.parentSpanID
}

func (t *Transaction) StartTimestampMs() int64 {
	if t == nil {
		return 0
	}
	return t.startTimestampMs
}

func (t *Transaction) EndTimestampMs() *int64 {
	if t == nil || t.endTimestampMs == nil {
		return nil
	}
	value := *t.endTimestampMs
	return &value
}

func (t *Transaction) Status() string {
	if t == nil {
		return ""
	}
	return t.status
}

func (t *Transaction) Context() any {
	if t == nil {
		return nil
	}
	return cloneNormalizedValue(normalizeValue(t.context))
}

func (t *Transaction) Measurements() map[string]any {
	if t == nil {
		return nil
	}
	return cloneMapStringAny(t.measurements)
}

func (t *Transaction) Spans() []*Span {
	if t == nil || len(t.spans) == 0 {
		return nil
	}
	spans := make([]*Span, len(t.spans))
	copy(spans, t.spans)
	return spans
}

func (t *Transaction) SetParentSpanID(value string) *Transaction {
	if t == nil {
		return nil
	}
	t.parentSpanID = normalizeString(value)
	return t
}

func (t *Transaction) SetContext(value any) *Transaction {
	if t == nil {
		return nil
	}
	t.context = normalizeValue(value)
	return t
}

func (t *Transaction) AddMeasurement(key string, value any, unit string) *Transaction {
	if t == nil {
		return nil
	}
	key = normalizeString(key)
	if key == "" {
		return t
	}
	if t.measurements == nil {
		t.measurements = make(map[string]any)
	}

	measurement := map[string]any{
		"value": normalizeValue(value),
	}
	if normalizedUnit := normalizeString(unit); normalizedUnit != "" {
		measurement["unit"] = normalizedUnit
	}

	t.measurements[key] = measurement
	return t
}

func (t *Transaction) StartChild(op string, description string) *Span {
	if t == nil {
		return nil
	}

	span := &Span{
		traceID:          t.traceID,
		spanID:           t.spanIDGenerator(),
		parentSpanID:     t.spanID,
		op:               defaultIfBlank(op, "custom"),
		description:      normalizeString(description),
		startTimestampMs: t.now().UnixMilli(),
		now:              t.now,
	}
	t.spans = append(t.spans, span)
	return span
}

func (t *Transaction) Finish(status string, endTimestampMs ...int64) *Transaction {
	if t == nil {
		return nil
	}
	if normalizedStatus := normalizeString(status); normalizedStatus != "" {
		t.status = normalizedStatus
	}

	end := t.now().UnixMilli()
	if len(endTimestampMs) > 0 {
		end = endTimestampMs[0]
	}
	t.endTimestampMs = &end
	return t
}

func (t *Transaction) DurationMs() int64 {
	if t == nil {
		return 0
	}
	end := t.startTimestampMs
	if t.endTimestampMs != nil {
		end = *t.endTimestampMs
	}
	if end < t.startTimestampMs {
		return 0
	}
	return end - t.startTimestampMs
}

func (s *Span) TraceID() string {
	if s == nil {
		return ""
	}
	return s.traceID
}

func (s *Span) SpanID() string {
	if s == nil {
		return ""
	}
	return s.spanID
}

func (s *Span) ParentSpanID() string {
	if s == nil {
		return ""
	}
	return s.parentSpanID
}

func (s *Span) SetData(data map[string]any) *Span {
	if s == nil {
		return nil
	}
	s.data = normalizeStringMap(data)
	return s
}

func (s *Span) Finish(status string, endTimestampMs ...int64) *Span {
	if s == nil {
		return nil
	}
	if normalizedStatus := normalizeString(status); normalizedStatus != "" {
		s.status = normalizedStatus
	}

	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	end := nowFn().UnixMilli()
	if len(endTimestampMs) > 0 {
		end = endTimestampMs[0]
	}
	s.endTimestampMs = &end
	return s
}

func (s *Span) payload() map[string]any {
	if s == nil {
		return nil
	}
	end := s.startTimestampMs
	if s.endTimestampMs != nil {
		end = *s.endTimestampMs
	}

	payload := map[string]any{
		"traceId":      s.traceID,
		"spanId":       s.spanID,
		"parentSpanId": s.parentSpanID,
		"op":           s.op,
		"startTime":    s.startTimestampMs,
		"endTime":      end,
		"duration":     maxInt64(0, end-s.startTimestampMs),
	}
	if s.description != "" {
		payload["description"] = s.description
	}
	if s.status != "" {
		payload["status"] = s.status
	}
	if len(s.data) > 0 {
		payload["data"] = cloneMapStringAny(s.data)
	}

	return payload
}

func defaultIfBlank(value string, fallback string) string {
	value = normalizeString(value)
	if value == "" {
		return fallback
	}
	return value
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
