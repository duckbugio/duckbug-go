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

// shouldRetry reports whether repeating the identical request can end
// differently. This predicate is the shared one: duckbug-js and duckbug-php
// answer exactly the same question the same way, and a change here has to land
// in all three.
//
// The rule is "transient unless proven final": a request that never produced a
// response, 408, 429 and every 5xx are worth another attempt; 501 is the single
// carve-out; everything else is the final answer.
//
// It is written as a rule with one hole rather than as a list of retriable
// codes on purpose. This client does not only talk to DuckBug's ingest - a
// DuckBug installation sits behind whatever edge the customer runs, and that
// edge invents statuses of its own. An allow list turns every code it has not
// been taught about into a silently dropped event, which is the one failure an
// error tracker must not have, and widening it means shipping a new SDK into
// every consumer's dependency tree. Being wrong the other way costs at most
// MaxRetries extra requests with bounded backoff, and a retry of a request
// that did arrive cannot create a second event: ingest deduplicates on the
// event id with a Postgres primary key and ON CONFLICT DO NOTHING, with no
// expiry, on the single and the batch route alike.
//
// That guarantee is only as strong as the id. It has to be supplied by the
// caller: when a payload reaches ingest without an "eventId", the server mints
// a fresh one per request and a retry does store the event twice. Duck sets a
// UUIDv4 on every event it builds, so the normal path is safe; a caller
// driving this provider directly through CaptureEvent owns that field itself.
// The server validates it as uuid4, so a non-UUID idempotency key is rejected
// with 400 rather than honoured.
//
// 408 is retried because it is the edge timing out the request body
// (nginx client_body_timeout and friends), never a verdict on the payload;
// RFC 9110 states outright that such a request may be repeated unchanged.
//
// 501 is the hole: it is DuckBug stating that the capability is not configured
// in this installation, and only an operator can change that. The backend
// reaches for 501 over 503 in exactly that case so clients stop retrying,
// because 503 would promise that waiting helps. A 501 from an intermediary
// means the same thing one layer out, so the answer is the same either way.
//
// Do not widen this carve-out to 503. On the ingest path a 503 is the edge
// during a redeploy - the transient case this predicate exists for.
//
// Deliberate differences from the other two SDKs, both outside this function:
//   - a request that never reached a response arrives here as ErrorMessage,
//     because that is how net/http reports a dial, TLS, timeout or cancelled
//     context failure. duckbug-php sees the same case as a cURL errno and
//     duckbug-js as a rejected fetch promise; all three retry it.
//   - retries are opt-in in this SDK (Config.MaxRetries defaults to 0) while
//     duckbug-php and duckbug-js default to 2, because this transport can run
//     inline in the caller's request path. The decision below is shared; the
//     budget spent on it is not.
//
// The 429 that DuckBug's rate limiter returns carries Retry-After, which this
// transport does not read - backoffDelay decides on its own. Honouring it is a
// separate change and has to land in all three SDKs together.
func shouldRetry(result core.TransportResult) bool {
	if result.ErrorMessage != "" {
		return true
	}

	switch result.StatusCode {
	case http.StatusNotImplemented:
		return false
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}

	return result.StatusCode >= http.StatusInternalServerError
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
