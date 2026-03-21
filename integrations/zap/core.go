package zapduckbug

import (
	"context"
	"time"

	duckbug "github.com/duckbugio/duckbug-go"
	"go.uber.org/zap/zapcore"
)

type Core struct {
	duck   *duckbug.Duck
	next   zapcore.Core
	fields []zapcore.Field
	minLvl zapcore.Level
}

type Option func(*Core)

func NewCore(duck *duckbug.Duck, next zapcore.Core, options ...Option) *Core {
	core := &Core{
		duck:   duck,
		next:   next,
		minLvl: zapcore.WarnLevel,
	}
	for _, option := range options {
		if option != nil {
			option(core)
		}
	}
	return core
}

func WithMinLevel(level zapcore.Level) Option {
	return func(core *Core) {
		core.minLvl = level
	}
}

func (c *Core) Enabled(level zapcore.Level) bool {
	sdkEnabled := c.duck != nil && level >= c.minLvl
	if c.next != nil {
		return c.next.Enabled(level) || sdkEnabled
	}
	return sdkEnabled
}

func (c *Core) With(fields []zapcore.Field) zapcore.Core {
	cloned := c.clone()
	cloned.fields = append(cloned.fields, fields...)
	if cloned.next != nil {
		cloned.next = cloned.next.With(fields)
	}
	return cloned
}

func (c *Core) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}

func (c *Core) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if c.duck != nil && entry.Level >= c.minLvl {
		details := fieldsToMap(c.fields, fields, entry)
		var payload any
		if len(details) > 0 {
			payload = details
		}
		at := entry.Time
		if at.IsZero() {
			at = time.Time{}
		}
		c.duck.CaptureLogContextAt(context.Background(), at, normalizeLevel(entry.Level), entry.Message, payload)
	}

	if c.next != nil {
		return c.next.Write(entry, fields)
	}
	return nil
}

func (c *Core) Sync() error {
	if c.next != nil {
		return c.next.Sync()
	}
	return nil
}

func (c *Core) clone() *Core {
	if c == nil {
		return &Core{}
	}

	cloned := &Core{
		duck:   c.duck,
		next:   c.next,
		minLvl: c.minLvl,
	}
	if len(c.fields) > 0 {
		cloned.fields = append([]zapcore.Field(nil), c.fields...)
	}
	return cloned
}

func fieldsToMap(inherited []zapcore.Field, fields []zapcore.Field, entry zapcore.Entry) map[string]any {
	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range inherited {
		field.AddTo(encoder)
	}
	for _, field := range fields {
		field.AddTo(encoder)
	}

	if entry.LoggerName != "" {
		encoder.Fields["logger"] = entry.LoggerName
	}
	if entry.Caller.Defined {
		encoder.Fields["caller"] = entry.Caller.TrimmedPath()
	}
	if entry.Stack != "" {
		encoder.Fields["stacktrace"] = entry.Stack
	}

	if len(encoder.Fields) == 0 {
		return nil
	}
	return encoder.Fields
}

func normalizeLevel(level zapcore.Level) string {
	switch level {
	case zapcore.DebugLevel:
		return "DEBUG"
	case zapcore.InfoLevel:
		return "INFO"
	case zapcore.WarnLevel:
		return "WARN"
	case zapcore.ErrorLevel, zapcore.DPanicLevel:
		return "ERROR"
	case zapcore.PanicLevel, zapcore.FatalLevel:
		return "FATAL"
	default:
		return "INFO"
	}
}
