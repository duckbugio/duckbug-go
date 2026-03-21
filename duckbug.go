package duckbug

import (
	corepkg "github.com/duckbugio/duckbug-go/core"
	duckbugprovider "github.com/duckbugio/duckbug-go/providers/duckbug"
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

func DefaultDuckBugPrivacyOptions() DuckBugPrivacyOptions {
	return duckbugprovider.DefaultPrivacyOptions()
}

func NewDuckBugHTTPTransport(config DuckBugHTTPTransportConfig) *DuckBugHTTPTransport {
	return duckbugprovider.NewHTTPTransport(config)
}
