package pond

import (
	"context"
	"os"
	"strings"
)

const DefaultMask = "***"

var defaultSensitiveFields = []string{
	"password",
	"token",
	"api_key",
	"authorization",
	"cookie",
	"session",
	"secret",
}

var defaultSensitiveHeaderNames = []string{
	"authorization",
	"cookie",
	"set-cookie",
	"x-api-key",
	"x-auth-token",
}

type RequestContext struct {
	IP          string
	URL         string
	Method      string
	Headers     map[string]any
	QueryParams map[string]any
	BodyParams  map[string]any
	Cookies     map[string]any
	Session     map[string]any
	Files       map[string]any
}

type CollectedContext struct {
	IP          string
	URL         string
	Method      string
	Headers     map[string]any
	QueryParams map[string]any
	BodyParams  map[string]any
	Cookies     map[string]any
	Session     map[string]any
	Files       map[string]any
	Env         map[string]any
}

type Config struct {
	SensitiveFields      []string
	SensitiveHeaderNames []string
	Mask                 string
	EnvProvider          func() map[string]string
}

type Pond struct {
	sensitiveFields      map[string]struct{}
	sensitiveHeaderNames map[string]struct{}
	mask                 string
	envProvider          func() map[string]string
}

type requestContextKey struct{}

func Ripple(sensitiveFields []string) *Pond {
	return New(Config{
		SensitiveFields: sensitiveFields,
	})
}

func New(config Config) *Pond {
	mask := strings.TrimSpace(config.Mask)
	if mask == "" {
		mask = DefaultMask
	}

	fields := make(map[string]struct{}, len(defaultSensitiveFields)+len(config.SensitiveFields))
	for _, item := range defaultSensitiveFields {
		fields[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	for _, item := range config.SensitiveFields {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized != "" {
			fields[normalized] = struct{}{}
		}
	}

	headers := make(map[string]struct{}, len(defaultSensitiveHeaderNames)+len(config.SensitiveHeaderNames))
	for _, item := range defaultSensitiveHeaderNames {
		headers[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	for _, item := range config.SensitiveHeaderNames {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized != "" {
			headers[normalized] = struct{}{}
		}
	}

	envProvider := config.EnvProvider
	if envProvider == nil {
		envProvider = defaultEnvProvider
	}

	return &Pond{
		sensitiveFields:      fields,
		sensitiveHeaderNames: headers,
		mask:                 mask,
		envProvider:          envProvider,
	}
}

func WithRequestContext(ctx context.Context, req RequestContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestContextKey{}, req)
}

func RequestContextFromContext(ctx context.Context) (RequestContext, bool) {
	if ctx == nil {
		return RequestContext{}, false
	}
	req, ok := ctx.Value(requestContextKey{}).(RequestContext)
	return req, ok
}

func (p *Pond) Collect(ctx context.Context) CollectedContext {
	if p == nil {
		p = Ripple(nil)
	}

	var collected CollectedContext
	if req, ok := RequestContextFromContext(ctx); ok {
		collected = CollectedContext{
			IP:          strings.TrimSpace(req.IP),
			URL:         strings.TrimSpace(req.URL),
			Method:      strings.TrimSpace(req.Method),
			Headers:     p.SanitizeHeaders(req.Headers),
			QueryParams: p.SanitizeMap(req.QueryParams),
			BodyParams:  p.SanitizeMap(req.BodyParams),
			Cookies:     p.SanitizeMap(req.Cookies),
			Session:     p.SanitizeMap(req.Session),
			Files:       p.SanitizeMap(req.Files),
		}
	}

	collected.Env = p.SanitizeMap(stringMapToAnyMap(p.envProvider()))

	return collected
}

func (p *Pond) SanitizeHeaders(headers map[string]any) map[string]any {
	return p.sanitizeMap(headers, true)
}

func (p *Pond) SanitizeMap(values map[string]any) map[string]any {
	return p.sanitizeMap(values, false)
}

func (p *Pond) SanitizeValue(value any) any {
	if p == nil {
		p = Ripple(nil)
	}
	return p.sanitizeValue(value, false)
}

func (p *Pond) sanitizeMap(values map[string]any, headerMode bool) map[string]any {
	if p == nil {
		p = Ripple(nil)
	}
	if len(values) == 0 {
		return nil
	}

	out := make(map[string]any, len(values))
	for key, value := range values {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}

		if p.shouldMaskKey(trimmedKey, headerMode) {
			out[trimmedKey] = p.mask
			continue
		}

		out[trimmedKey] = p.sanitizeValue(value, false)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func (p *Pond) sanitizeValue(value any, headerMode bool) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return p.sanitizeMap(typed, headerMode)
	case map[string]string:
		return p.sanitizeMap(stringMapToAnyMap(typed), headerMode)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = p.sanitizeValue(typed[i], false)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = typed[i]
		}
		return out
	default:
		return typed
	}
}

func (p *Pond) shouldMaskKey(key string, headerMode bool) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}

	if headerMode {
		if _, ok := p.sensitiveHeaderNames[normalized]; ok {
			return true
		}
	}

	_, ok := p.sensitiveFields[normalized]
	return ok
}

func defaultEnvProvider() map[string]string {
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	return values
}

func stringMapToAnyMap(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
