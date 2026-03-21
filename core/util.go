package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func defaultEventID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err == nil {
		data[6] = (data[6] & 0x0f) | 0x40
		data[8] = (data[8] & 0x3f) | 0x80
		return fmt.Sprintf(
			"%08x-%04x-%04x-%04x-%012x",
			data[0:4],
			data[4:6],
			data[6:8],
			data[8:10],
			data[10:16],
		)
	}

	now := time.Now().UnixNano()
	return fmt.Sprintf(
		"%08x-%04x-4%03x-a%03x-%012x",
		uint32(now>>32),
		uint16(now>>16),
		uint16(now)&0x0fff,
		uint16(now>>4)&0x0fff,
		uint64(now)&0x0000ffffffffffff,
	)
}

func defaultTraceID() string {
	return defaultHexID(16)
}

func defaultSpanID() string {
	return defaultHexID(8)
}

func defaultHexID(length int) string {
	if length <= 0 {
		return ""
	}

	data := make([]byte, length)
	if _, err := rand.Read(data); err == nil {
		return hex.EncodeToString(data)
	}

	fallback := fmt.Sprintf("%0*x", length*2, time.Now().UnixNano())
	if len(fallback) > length*2 {
		fallback = fallback[len(fallback)-length*2:]
	}
	return fallback
}

func normalizeString(value string) string {
	return strings.TrimSpace(value)
}

func cloneMapStringAny(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneNormalizedValue(value)
	}

	return cloned
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	cloned := make([]string, len(values))
	copy(cloned, values)

	return cloned
}

func cloneNormalizedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMapStringAny(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneNormalizedValue(typed[i])
		}
		return out
	default:
		return typed
	}
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return typed
	case bool:
		return typed
	case int:
		return typed
	case int8:
		return typed
	case int16:
		return typed
	case int32:
		return typed
	case int64:
		return typed
	case uint:
		return typed
	case uint8:
		return typed
	case uint16:
		return typed
	case uint32:
		return typed
	case uint64:
		return typed
	case float32:
		return typed
	case float64:
		return typed
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return i
		}
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return typed.String()
	case error:
		return typed.Error()
	case fmt.Stringer:
		return typed.String()
	case []string:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = typed[i]
		}
		return out
	case []int:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = typed[i]
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = normalizeValue(typed[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeValue(item)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	case map[string]int:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil
	}

	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return normalizeValue(rv.Elem().Interface())
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = normalizeValue(iter.Value().Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = normalizeValue(rv.Index(i).Interface())
		}
		return out
	}

	if marshaled, err := json.Marshal(value); err == nil {
		var decoded any
		if err := json.Unmarshal(marshaled, &decoded); err == nil {
			return normalizeValue(decoded)
		}
	}

	return fmt.Sprint(value)
}

func normalizeStringMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}

	out := make(map[string]any, len(value))
	for key, item := range value {
		trimmedKey := normalizeString(key)
		if trimmedKey == "" {
			continue
		}
		out[trimmedKey] = normalizeValue(item)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func buildErrorStack(skip int) ([]map[string]any, string, int, string) {
	pcs := make([]uintptr, 64)
	count := runtime.Callers(skip, pcs)
	if count == 0 {
		return []map[string]any{
			{"file": "unknown", "line": 0, "function": "unknown"},
		}, "unknown", 0, "unknown"
	}

	frames := runtime.CallersFrames(pcs[:count])
	stack := make([]map[string]any, 0, count)
	var topFile string
	var topLine int
	var builder strings.Builder

	for {
		frame, more := frames.Next()
		if frame.File != "" || frame.Function != "" {
			stack = append(stack, map[string]any{
				"file":     frame.File,
				"line":     frame.Line,
				"function": frame.Function,
			})
			if topFile == "" {
				topFile = frame.File
				topLine = frame.Line
			}
			builder.WriteString(frame.Function)
			builder.WriteByte('\n')
			builder.WriteByte('\t')
			builder.WriteString(frame.File)
			builder.WriteByte(':')
			builder.WriteString(strconv.Itoa(frame.Line))
			builder.WriteByte('\n')
		}
		if !more {
			break
		}
	}

	if len(stack) == 0 {
		stack = append(stack, map[string]any{
			"file": "unknown", "line": 0, "function": "unknown",
		})
		topFile = "unknown"
		topLine = 0
	}

	return stack, topFile, topLine, strings.TrimSpace(builder.String())
}

func buildExceptionValue(err error) any {
	if err == nil {
		return nil
	}

	chain := make([]map[string]any, 0, 4)
	for current := err; current != nil; current = errors.Unwrap(current) {
		chain = append(chain, map[string]any{
			"type":    fmt.Sprintf("%T", current),
			"message": current.Error(),
		})
	}

	if len(chain) == 1 {
		return chain[0]
	}

	return chain
}
