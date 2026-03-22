package duckbugprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/duckbugio/duckbug-go/core"
	"github.com/duckbugio/duckbug-go/internal/sdkrequest"
)

type HTTPTransportConfig struct {
	Client            *http.Client
	Timeout           time.Duration
	ConnectionTimeout time.Duration
	MaxRetries        int
	RetryDelay        time.Duration
	MaxResponseBytes  int64
}

type HTTPTransport struct {
	client           *http.Client
	maxRetries       int
	retryDelay       time.Duration
	maxResponseBytes int64
}

func NewHTTPTransport(config HTTPTransportConfig) *HTTPTransport {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	connectionTimeout := config.ConnectionTimeout
	if connectionTimeout <= 0 {
		connectionTimeout = 3 * time.Second
	}
	retryDelay := config.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 100 * time.Millisecond
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = 128 << 10
	}

	client := config.Client
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{
			Timeout:   connectionTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext
		transport.TLSHandshakeTimeout = connectionTimeout
		client = &http.Client{
			Timeout:   timeout,
			Transport: transport,
		}
	}

	return &HTTPTransport{
		client:           client,
		maxRetries:       max(0, config.MaxRetries),
		retryDelay:       retryDelay,
		maxResponseBytes: maxResponseBytes,
	}
}

func (t *HTTPTransport) Send(ctx context.Context, dsn string, eventType core.EventType, data map[string]any) core.TransportResult {
	return t.request(ctx, joinURL(dsn, string(eventType)), data)
}

func (t *HTTPTransport) SendBatch(ctx context.Context, dsn string, eventType core.EventType, items []map[string]any) core.TransportResult {
	return t.request(ctx, joinURL(dsn, string(eventType), "batch"), items)
}

func (t *HTTPTransport) request(ctx context.Context, url string, payload any) core.TransportResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return core.TransportResult{
			ErrorMessage: err.Error(),
			Attempts:     1,
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}

	maxAttempts := t.maxRetries + 1
	result := core.TransportResult{}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result = t.execute(ctx, url, body, attempt)
		if !shouldRetry(result) || attempt >= maxAttempts {
			return result
		}
		if !sleepWithContext(ctx, backoffDelay(t.retryDelay, attempt)) {
			if result.ErrorMessage == "" {
				result.ErrorMessage = ctx.Err().Error()
			}
			return result
		}
	}

	return result
}

func (t *HTTPTransport) execute(ctx context.Context, url string, body []byte, attempts int) core.TransportResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return core.TransportResult{
			ErrorMessage: err.Error(),
			Attempts:     attempts,
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sdkrequest.HeaderName, sdkrequest.HeaderValue)

	resp, err := t.client.Do(req)
	if err != nil {
		return core.TransportResult{
			ErrorMessage: err.Error(),
			Attempts:     attempts,
		}
	}
	defer resp.Body.Close()

	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, t.maxResponseBytes))
	result := core.TransportResult{
		StatusCode:   resp.StatusCode,
		ResponseBody: strings.TrimSpace(string(responseBody)),
		Attempts:     attempts,
	}

	if resp.StatusCode == http.StatusConflict {
		result.Duplicate = true
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var batchResponse struct {
			Items []core.BatchItemResult `json:"items"`
		}
		if err := json.Unmarshal(responseBody, &batchResponse); err == nil && len(batchResponse.Items) > 0 {
			result.Items = batchResponse.Items
		}
	}

	return result
}

func shouldRetry(result core.TransportResult) bool {
	if result.ErrorMessage != "" {
		return true
	}
	return result.StatusCode == http.StatusTooManyRequests || result.StatusCode >= http.StatusInternalServerError
}

func joinURL(base string, segments ...string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	for _, segment := range segments {
		trimmed += "/" + strings.TrimLeft(strings.TrimSpace(segment), "/")
	}
	return trimmed
}

func backoffDelay(base time.Duration, attempt int) time.Duration {
	if attempt <= 1 {
		return base
	}
	return base * time.Duration(1<<(attempt-1))
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
