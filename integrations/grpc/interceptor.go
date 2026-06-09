package grpc

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	duckbug "github.com/duckbugio/duckbug-go"
	"github.com/duckbugio/duckbug-go/pond"
)

type Config struct {
	CapturePanics         bool
	Repanic               bool
	CaptureMetadata       bool
	CaptureTransactions   bool
	TransactionSampleRate float64
	IgnoredMethods        []string
	IgnoredMethodPrefixes []string
	SkipMethod            func(fullMethod string) bool
	// ShouldCapture decides whether an RPC that finished with the given code is
	// reported as an error. When nil, server-fault codes are captured.
	ShouldCapture func(code codes.Code) bool
}

type Option func(*Config)

const defaultProductionTransactionSampleRate = 0.10

func newConfig(options ...Option) Config {
	config := Config{
		CapturePanics:         true,
		CaptureTransactions:   false,
		TransactionSampleRate: 1,
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	if config.ShouldCapture == nil {
		config.ShouldCapture = defaultShouldCapture
	}
	return config
}

func UnaryServerInterceptor(duck *duckbug.Duck, options ...Option) grpc.UnaryServerInterceptor {
	config := newConfig(options...)

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		fullMethod := ""
		if info != nil {
			fullMethod = info.FullMethod
		}
		if shouldIgnoreMethod(fullMethod, config) {
			return handler(ctx, req)
		}

		ctx = pond.WithRequestContext(ctx, buildRequestContext(ctx, fullMethod, config))
		tx := startTransaction(duck, fullMethod, config)

		defer func() {
			if recovered := recover(); recovered != nil {
				if duck != nil && config.CapturePanics {
					duck.QuackRecoveredPanicContext(ctx, recovered, "grpc_unary_interceptor")
				}
				captureTransaction(duck, ctx, codes.Internal, tx, true, config)
				if config.Repanic {
					panic(recovered)
				}
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()

		resp, err = handler(ctx, req)
		code := status.Code(err)
		captureError(duck, ctx, fullMethod, code, err, config)
		captureTransaction(duck, ctx, code, tx, false, config)
		return resp, err
	}
}

func StreamServerInterceptor(duck *duckbug.Duck, options ...Option) grpc.StreamServerInterceptor {
	config := newConfig(options...)

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		fullMethod := ""
		if info != nil {
			fullMethod = info.FullMethod
		}
		if shouldIgnoreMethod(fullMethod, config) {
			return handler(srv, ss)
		}

		ctx := pond.WithRequestContext(ss.Context(), buildRequestContext(ss.Context(), fullMethod, config))
		wrapped := &serverStreamWithContext{ServerStream: ss, ctx: ctx}
		tx := startTransaction(duck, fullMethod, config)

		defer func() {
			if recovered := recover(); recovered != nil {
				if duck != nil && config.CapturePanics {
					duck.QuackRecoveredPanicContext(ctx, recovered, "grpc_stream_interceptor")
				}
				captureTransaction(duck, ctx, codes.Internal, tx, true, config)
				if config.Repanic {
					panic(recovered)
				}
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()

		err = handler(srv, wrapped)
		code := status.Code(err)
		captureError(duck, ctx, fullMethod, code, err, config)
		captureTransaction(duck, ctx, code, tx, false, config)
		return err
	}
}

func WithCapturePanics(enabled bool) Option {
	return func(config *Config) {
		config.CapturePanics = enabled
	}
}

func WithRepanic(enabled bool) Option {
	return func(config *Config) {
		config.Repanic = enabled
	}
}

func WithCaptureMetadata(enabled bool) Option {
	return func(config *Config) {
		config.CaptureMetadata = enabled
	}
}

func WithCaptureTransactions(enabled bool) Option {
	return func(config *Config) {
		config.CaptureTransactions = enabled
	}
}

func WithTransactionSampleRate(rate float64) Option {
	return func(config *Config) {
		config.TransactionSampleRate = rate
	}
}

func WithIgnoredMethods(methods ...string) Option {
	return func(config *Config) {
		config.IgnoredMethods = append(config.IgnoredMethods, methods...)
	}
}

func WithIgnoredMethodPrefixes(prefixes ...string) Option {
	return func(config *Config) {
		config.IgnoredMethodPrefixes = append(config.IgnoredMethodPrefixes, prefixes...)
	}
}

func WithSkipMethod(fn func(fullMethod string) bool) Option {
	return func(config *Config) {
		config.SkipMethod = fn
	}
}

func WithShouldCapture(fn func(code codes.Code) bool) Option {
	return func(config *Config) {
		config.ShouldCapture = fn
	}
}

// WithProductionDefaults enables the most common gRPC server production setup:
// panic capture, metadata capture, and transaction capture with conservative sampling.
func WithProductionDefaults() Option {
	return func(config *Config) {
		config.CapturePanics = true
		config.CaptureMetadata = true
		config.CaptureTransactions = true
		config.TransactionSampleRate = defaultProductionTransactionSampleRate
	}
}

type serverStreamWithContext struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *serverStreamWithContext) Context() context.Context {
	return s.ctx
}

func defaultShouldCapture(code codes.Code) bool {
	switch code {
	case codes.Unknown,
		codes.DeadlineExceeded,
		codes.Unimplemented,
		codes.Internal,
		codes.Unavailable,
		codes.DataLoss:
		return true
	default:
		return false
	}
}

func shouldIgnoreMethod(fullMethod string, config Config) bool {
	method := strings.TrimSpace(fullMethod)
	if config.SkipMethod != nil && config.SkipMethod(fullMethod) {
		return true
	}
	for _, item := range config.IgnoredMethods {
		if method == strings.TrimSpace(item) {
			return true
		}
	}
	for _, item := range config.IgnoredMethodPrefixes {
		prefix := strings.TrimSpace(item)
		if prefix != "" && strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}

func startTransaction(duck *duckbug.Duck, fullMethod string, config Config) *duckbug.Transaction {
	if duck == nil || !config.CaptureTransactions {
		return nil
	}

	tx := duck.StartTransaction(transactionName(fullMethod), "grpc.server")
	tx.SetContext(map[string]any{
		"method": fullMethod,
	})
	return tx
}

func captureTransaction(
	duck *duckbug.Duck,
	ctx context.Context,
	code codes.Code,
	tx *duckbug.Transaction,
	force bool,
	config Config,
) {
	if duck == nil || tx == nil {
		return
	}
	if !force && !shouldCaptureTransaction(config.TransactionSampleRate, code) {
		return
	}

	tx.AddMeasurement("grpc.status_code", uint32(code), "")
	tx.Finish(transactionStatusFromCode(code))
	duck.CaptureTransactionContext(ctx, tx)
}

func captureError(
	duck *duckbug.Duck,
	ctx context.Context,
	fullMethod string,
	code codes.Code,
	err error,
	config Config,
) {
	if duck == nil || !config.ShouldCapture(code) {
		return
	}

	details := map[string]any{
		"method":   fullMethod,
		"grpcCode": code.String(),
	}
	if st, ok := status.FromError(err); ok && st != nil {
		if message := strings.TrimSpace(st.Message()); message != "" {
			details["responseMessage"] = message
		}
	}

	duck.QuackContextDetails(ctx, grpcStatusError(code, fullMethod), details, true, "grpc_status")
}

func buildRequestContext(ctx context.Context, fullMethod string, config Config) pond.RequestContext {
	reqCtx := pond.RequestContext{
		IP:     clientIP(ctx),
		URL:    strings.TrimSpace(fullMethod),
		Method: "gRPC",
	}
	if config.CaptureMetadata {
		reqCtx.Headers = metadataToMap(ctx)
	}
	return reqCtx
}

func clientIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.Addr == nil {
		return ""
	}

	addr := strings.TrimSpace(p.Addr.String())
	if host, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.String()
	}
	return addr
}

func metadataToMap(ctx context.Context) map[string]any {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md) == 0 {
		return nil
	}

	out := make(map[string]any, len(md))
	for key, values := range md {
		if len(values) == 0 {
			continue
		}
		if len(values) == 1 {
			out[key] = values[0]
			continue
		}
		items := make([]any, len(values))
		for i := range values {
			items[i] = values[i]
		}
		out[key] = items
	}
	return out
}

func transactionName(fullMethod string) string {
	method := strings.TrimSpace(fullMethod)
	if method == "" {
		return "grpc"
	}
	return method
}

func grpcStatusError(code codes.Code, fullMethod string) error {
	method := strings.TrimSpace(fullMethod)
	if method == "" {
		method = "unknown"
	}
	return fmt.Errorf("grpc %s: %s", code.String(), method)
}

func shouldCaptureTransaction(sampleRate float64, code codes.Code) bool {
	if defaultShouldCapture(code) {
		return true
	}
	switch {
	case sampleRate <= 0:
		return false
	case sampleRate >= 1:
		return true
	default:
		return rand.Float64() < sampleRate
	}
}

func transactionStatusFromCode(code codes.Code) string {
	switch code {
	case codes.OK:
		return "ok"
	case codes.Unknown, codes.Internal, codes.DataLoss, codes.Unavailable, codes.DeadlineExceeded, codes.Unimplemented:
		return "internal_error"
	default:
		return "invalid_argument"
	}
}
