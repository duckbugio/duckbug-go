package duckbug

import (
	"strings"

	"github.com/duckbugio/duckbug-go/pond"
)

type Option func(*runtimeConfig)

type runtimeConfig struct {
	providerOptions []DuckBugProviderOption
	sensitiveFields []string
	pond            *pond.Pond
	environment     string
	release         string
	service         string
	serverName      string
}

// New creates a Duck runtime with the first-party DuckBug provider already wired.
// It is the shortest path for the common "one DSN + optional metadata" setup.
func New(dsn string, options ...Option) *Duck {
	cfg := runtimeConfig{}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}

	duckCfg := Config{
		Providers: []Provider{
			NewDuckBugProvider(strings.TrimSpace(dsn), cfg.providerOptions...),
		},
	}
	if cfg.pond != nil {
		duckCfg.Pond = cfg.pond
	} else if len(cfg.sensitiveFields) > 0 {
		duckCfg.Pond = pond.Ripple(cfg.sensitiveFields)
	}

	duck := NewDuck(duckCfg)
	if duck == nil {
		return nil
	}
	if value := strings.TrimSpace(cfg.environment); value != "" {
		duck.SetEnvironment(value)
	}
	if value := strings.TrimSpace(cfg.release); value != "" {
		duck.SetRelease(value)
	}
	if value := strings.TrimSpace(cfg.service); value != "" {
		duck.SetService(value)
	}
	if value := strings.TrimSpace(cfg.serverName); value != "" {
		duck.SetServerName(value)
	}
	return duck
}

func WithEnvironment(environment string) Option {
	return func(cfg *runtimeConfig) {
		cfg.environment = environment
	}
}

func WithRelease(release string) Option {
	return func(cfg *runtimeConfig) {
		cfg.release = release
	}
}

func WithService(service string) Option {
	return func(cfg *runtimeConfig) {
		cfg.service = service
	}
}

func WithServerName(serverName string) Option {
	return func(cfg *runtimeConfig) {
		cfg.serverName = serverName
	}
}

func WithSensitiveFields(fields ...string) Option {
	return func(cfg *runtimeConfig) {
		cfg.sensitiveFields = append(cfg.sensitiveFields, fields...)
	}
}

func WithPond(p *pond.Pond) Option {
	return func(cfg *runtimeConfig) {
		cfg.pond = p
	}
}

func WithProviderOptions(options ...DuckBugProviderOption) Option {
	return func(cfg *runtimeConfig) {
		cfg.providerOptions = append(cfg.providerOptions, options...)
	}
}
