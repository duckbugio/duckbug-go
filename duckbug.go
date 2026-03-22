package duckbug

import (
	corepkg "github.com/duckbugio/duckbug-go/core"
	duckbugprovider "github.com/duckbugio/duckbug-go/providers/duckbug"
	"time"
)

type Duck = corepkg.Duck
type Config = corepkg.Config
type Provider = corepkg.Provider
type FlushableProvider = corepkg.FlushableProvider
type Event = corepkg.Event
type EventType = corepkg.EventType
type Level = corepkg.Level
type Transaction = corepkg.Transaction
type Span = corepkg.Span
type BatchItemResult = corepkg.BatchItemResult
type TransportResult = corepkg.TransportResult
type FailureInfo = corepkg.FailureInfo

type DuckBugProvider = duckbugprovider.Provider
type DuckBugProviderConfig = duckbugprovider.Config
type DuckBugPrivacyOptions = duckbugprovider.PrivacyOptions
type DuckBugProviderOption = duckbugprovider.Option
type DuckBugBeforeSendFunc = duckbugprovider.BeforeSendFunc
type DuckBugTransport = duckbugprovider.Transport
type DuckBugHTTPTransport = duckbugprovider.HTTPTransport
type DuckBugHTTPTransportConfig = duckbugprovider.HTTPTransportConfig

const (
	EventTypeError       = corepkg.EventTypeError
	EventTypeLog         = corepkg.EventTypeLog
	EventTypeTransaction = corepkg.EventTypeTransaction

	LevelDebug = corepkg.LevelDebug
	LevelInfo  = corepkg.LevelInfo
	LevelWarn  = corepkg.LevelWarn
	LevelError = corepkg.LevelError
	LevelFatal = corepkg.LevelFatal
)

func NewDuck(config Config) *Duck {
	return corepkg.NewDuck(config)
}

func NormalizeLevel(level any) Level {
	return corepkg.NormalizeLevel(level)
}

func NewDuckBugProvider(dsn string, options ...DuckBugProviderOption) *DuckBugProvider {
	return duckbugprovider.New(dsn, options...)
}

func NewDuckBugProviderWithConfig(config DuckBugProviderConfig) *DuckBugProvider {
	return duckbugprovider.NewWithConfig(config)
}

func WithBatchSize(size int) DuckBugProviderOption {
	return duckbugprovider.WithBatchSize(size)
}

func WithAsync(enabled bool) DuckBugProviderOption {
	return duckbugprovider.WithAsync(enabled)
}

func WithQueueSize(size int) DuckBugProviderOption {
	return duckbugprovider.WithQueueSize(size)
}

func WithFlushInterval(interval time.Duration) DuckBugProviderOption {
	return duckbugprovider.WithFlushInterval(interval)
}

func WithPrivacy(options DuckBugPrivacyOptions) DuckBugProviderOption {
	return duckbugprovider.WithPrivacy(options)
}

func WithBeforeSend(fn DuckBugBeforeSendFunc) DuckBugProviderOption {
	return duckbugprovider.WithBeforeSend(fn)
}

func WithTransportFailureHandler(fn func(FailureInfo)) DuckBugProviderOption {
	return duckbugprovider.WithTransportFailureHandler(fn)
}

func WithTransport(transport DuckBugTransport) DuckBugProviderOption {
	return duckbugprovider.WithTransport(transport)
}

func WithTimeout(timeout time.Duration) DuckBugProviderOption {
	return duckbugprovider.WithTimeout(timeout)
}

func WithConnectionTimeout(timeout time.Duration) DuckBugProviderOption {
	return duckbugprovider.WithConnectionTimeout(timeout)
}

func WithMaxRetries(maxRetries int) DuckBugProviderOption {
	return duckbugprovider.WithMaxRetries(maxRetries)
}

func WithRetryDelay(delay time.Duration) DuckBugProviderOption {
	return duckbugprovider.WithRetryDelay(delay)
}

func DefaultDuckBugPrivacyOptions() DuckBugPrivacyOptions {
	return duckbugprovider.DefaultPrivacyOptions()
}

func NewDuckBugHTTPTransport(config DuckBugHTTPTransportConfig) *DuckBugHTTPTransport {
	return duckbugprovider.NewHTTPTransport(config)
}
