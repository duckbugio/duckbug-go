package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/duckbugio/duckbug-go/pond"
)

type EventType string

const (
	EventTypeError       EventType = "errors"
	EventTypeLog         EventType = "logs"
	EventTypeTransaction EventType = "transactions"
)

type Level string

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
	LevelFatal Level = "FATAL"
)

func NormalizeLevel(level any) Level {
	switch typed := level.(type) {
	case Level:
		if typed == "" {
			return LevelInfo
		}
		return typed
	case slog.Level:
		switch {
		case typed <= slog.LevelDebug:
			return LevelDebug
		case typed < slog.LevelWarn:
			return LevelInfo
		case typed < slog.LevelError:
			return LevelWarn
		case typed >= 12:
			return LevelFatal
		default:
			return LevelError
		}
	case int:
		return NormalizeLevel(slog.Level(typed))
	case int8:
		return NormalizeLevel(slog.Level(typed))
	case int16:
		return NormalizeLevel(slog.Level(typed))
	case int32:
		return NormalizeLevel(slog.Level(typed))
	case int64:
		return NormalizeLevel(slog.Level(typed))
	case uint:
		return NormalizeLevel(slog.Level(typed))
	case uint8:
		return NormalizeLevel(slog.Level(typed))
	case uint16:
		return NormalizeLevel(slog.Level(typed))
	case uint32:
		return NormalizeLevel(slog.Level(typed))
	case uint64:
		return NormalizeLevel(slog.Level(typed))
	}

	raw := strings.ToUpper(strings.TrimSpace(fmt.Sprint(level)))
	switch raw {
	case "DEBUG":
		return LevelDebug
	case "INFO", "NOTICE":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	case "FATAL", "CRITICAL", "ALERT", "EMERGENCY":
		return LevelFatal
	default:
		return LevelInfo
	}
}

type Event struct {
	Type    EventType
	Payload map[string]any
}

func NewEvent(eventType EventType, payload map[string]any) Event {
	return Event{
		Type:    eventType,
		Payload: cloneMapStringAny(payload),
	}
}

type Provider interface {
	CaptureEvent(ctx context.Context, event Event)
}

type FlushableProvider interface {
	Provider
	Flush(ctx context.Context)
}

type BatchItemResult struct {
	ID      string `json:"id,omitempty"`
	EventID string `json:"eventId,omitempty"`
	GroupID string `json:"groupId,omitempty"`
	Status  string `json:"status,omitempty"`
}

type TransportResult struct {
	StatusCode   int
	ResponseBody string
	ErrorMessage string
	Attempts     int
	Duplicate    bool
	Items        []BatchItemResult
}

func (r TransportResult) IsSuccess() bool {
	if r.ErrorMessage != "" {
		return false
	}

	return (r.StatusCode >= 200 && r.StatusCode < 300) || r.Duplicate
}

type FailureInfo struct {
	Type    EventType
	Items   []map[string]any
	Result  TransportResult
	Message string
}

type Config struct {
	Providers        []Provider
	Pond             *pond.Pond
	Now              func() time.Time
	EventIDGenerator func() string
	SDK              map[string]any
	Runtime          map[string]any
	Platform         string
}
