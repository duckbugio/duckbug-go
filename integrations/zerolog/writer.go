package zerologduckbug

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	duckbug "github.com/duckbugio/duckbug-go"
	"github.com/rs/zerolog"
)

type Writer struct {
	duck      *duckbug.Duck
	next      io.Writer
	nextLevel zerolog.LevelWriter
	minLvl    zerolog.Level
}

type Option func(*Writer)

func NewWriter(duck *duckbug.Duck, next io.Writer, options ...Option) *Writer {
	writer := &Writer{
		duck:   duck,
		next:   next,
		minLvl: zerolog.WarnLevel,
	}
	if levelWriter, ok := next.(zerolog.LevelWriter); ok {
		writer.nextLevel = levelWriter
	}
	for _, option := range options {
		if option != nil {
			option(writer)
		}
	}
	return writer
}

func WithMinLevel(level zerolog.Level) Option {
	return func(writer *Writer) {
		writer.minLvl = level
	}
}

func (w *Writer) Write(payload []byte) (int, error) {
	w.capture(zerolog.NoLevel, payload)
	if w.next != nil {
		return w.next.Write(payload)
	}
	return len(payload), nil
}

func (w *Writer) WriteLevel(level zerolog.Level, payload []byte) (int, error) {
	w.capture(level, payload)
	if w.nextLevel != nil {
		return w.nextLevel.WriteLevel(level, payload)
	}
	if w.next != nil {
		return w.next.Write(payload)
	}
	return len(payload), nil
}

func (w *Writer) capture(level zerolog.Level, raw []byte) {
	if w == nil || w.duck == nil {
		return
	}

	message, details, payloadLevel, at, ok := decodePayload(raw)
	if !ok {
		return
	}
	if level == zerolog.NoLevel {
		level = payloadLevel
	}
	if level < w.minLvl {
		return
	}

	var contextValue any
	if len(details) > 0 {
		contextValue = details
	}
	w.duck.LogContextAt(context.Background(), at, normalizeLevel(level), message, contextValue)
}

func decodePayload(raw []byte) (message string, details map[string]any, level zerolog.Level, at time.Time, ok bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", nil, zerolog.NoLevel, time.Time{}, false
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return "", nil, zerolog.NoLevel, time.Time{}, false
	}

	if value, exists := payload[zerolog.MessageFieldName]; exists {
		message, _ = value.(string)
		delete(payload, zerolog.MessageFieldName)
	}
	if value, exists := payload[zerolog.LevelFieldName]; exists {
		level = parseLevel(value)
		delete(payload, zerolog.LevelFieldName)
	}
	if value, exists := payload[zerolog.TimestampFieldName]; exists {
		at = parseTime(value)
		delete(payload, zerolog.TimestampFieldName)
	}
	if len(payload) > 0 {
		details = payload
	}
	return message, details, level, at, true
}

func parseLevel(value any) zerolog.Level {
	levelText, _ := value.(string)
	switch strings.ToLower(strings.TrimSpace(levelText)) {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	default:
		return zerolog.NoLevel
	}
}

func parseTime(value any) time.Time {
	switch typed := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return parsed
		}
	case float64:
		return unixFromNumber(int64(typed))
	case int64:
		return unixFromNumber(typed)
	case int:
		return unixFromNumber(int64(typed))
	}
	return time.Time{}
}

func unixFromNumber(value int64) time.Time {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000_000_000_000_000:
		return time.Unix(0, value).UTC()
	case abs >= 1_000_000_000_000_000:
		return time.UnixMicro(value).UTC()
	case abs >= 1_000_000_000_000:
		return time.UnixMilli(value).UTC()
	default:
		return time.Unix(value, 0).UTC()
	}
}

func normalizeLevel(level zerolog.Level) string {
	switch level {
	case zerolog.TraceLevel, zerolog.DebugLevel:
		return "DEBUG"
	case zerolog.InfoLevel:
		return "INFO"
	case zerolog.WarnLevel:
		return "WARN"
	case zerolog.ErrorLevel:
		return "ERROR"
	case zerolog.FatalLevel, zerolog.PanicLevel:
		return "FATAL"
	default:
		return "INFO"
	}
}
