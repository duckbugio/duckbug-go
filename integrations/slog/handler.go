package slogduckbug

import (
	"context"
	"log/slog"
	"time"

	"github.com/duckbugio/duckbug-go"
)

type Handler struct {
	duck   *duckbug.Duck
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

func NewHandler(duck *duckbug.Duck, next slog.Handler) *Handler {
	return &Handler{
		duck: duck,
		next: next,
	}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.next != nil {
		return h.next.Enabled(ctx, level)
	}
	return true
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if h.duck != nil {
		payload := make(map[string]any)
		for _, attr := range h.attrs {
			addAttr(payload, h.groups, attr)
		}
		record.Attrs(func(attr slog.Attr) bool {
			addAttr(payload, h.groups, attr)
			return true
		})
		var details any
		if len(payload) > 0 {
			details = payload
		}
		h.duck.CaptureLogContextAt(ctx, record.Time, record.Level, record.Message, details)
	}

	if h.next != nil {
		return h.next.Handle(ctx, record)
	}

	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := h.clone()
	cloned.attrs = append(cloned.attrs, attrs...)
	if h.next != nil {
		cloned.next = h.next.WithAttrs(attrs)
	}
	return cloned
}

func (h *Handler) WithGroup(name string) slog.Handler {
	cloned := h.clone()
	cloned.groups = append(cloned.groups, name)
	if h.next != nil {
		cloned.next = h.next.WithGroup(name)
	}
	return cloned
}

func (h *Handler) clone() *Handler {
	if h == nil {
		return &Handler{}
	}

	cloned := &Handler{
		duck: h.duck,
		next: h.next,
	}
	if len(h.attrs) > 0 {
		cloned.attrs = append([]slog.Attr(nil), h.attrs...)
	}
	if len(h.groups) > 0 {
		cloned.groups = append([]string(nil), h.groups...)
	}
	return cloned
}

func addAttr(target map[string]any, groups []string, attr slog.Attr) {
	if target == nil {
		return
	}

	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	current := target
	for _, group := range groups {
		if group == "" {
			continue
		}
		next, ok := current[group].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[group] = next
		}
		current = next
	}

	assignAttr(current, attr)
}

func assignAttr(target map[string]any, attr slog.Attr) {
	if attr.Key == "" && attr.Value.Kind() != slog.KindGroup {
		return
	}

	if attr.Value.Kind() == slog.KindGroup {
		groupTarget := target
		if attr.Key != "" {
			next, ok := target[attr.Key].(map[string]any)
			if !ok {
				next = make(map[string]any)
				target[attr.Key] = next
			}
			groupTarget = next
		}
		for _, item := range attr.Value.Group() {
			assignAttr(groupTarget, item)
		}
		return
	}

	target[attr.Key] = valueToAny(attr.Value)
}

func valueToAny(value slog.Value) any {
	switch value.Kind() {
	case slog.KindAny:
		return value.Any()
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time().UTC().Format(time.RFC3339Nano)
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindLogValuer:
		return valueToAny(value.Resolve())
	case slog.KindGroup:
		group := make(map[string]any)
		for _, item := range value.Group() {
			assignAttr(group, item)
		}
		return group
	default:
		return value.Any()
	}
}
