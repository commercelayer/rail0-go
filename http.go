package rail0

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LogEntry holds one log record emitted per request attempt.
type LogEntry struct {
	// Method is the HTTP method (GET, POST, …).
	Method string
	// URL is the full URL including query string.
	URL string
	// RequestBody is the serialised request body, if any.
	RequestBody any
	// Status is the HTTP status code. Zero on network-level errors.
	Status int
	// DurationMs is the wall-clock time from sending to receiving, in milliseconds.
	DurationMs int64
	// ResponseBody is the parsed JSON response.
	ResponseBody any
	// Err holds a network error or *APIError for non-2xx responses.
	Err error
	// Attempt is the 1-based attempt number. Set only when MaxRetries > 0.
	Attempt int
	// WillRetry is true when a retry is scheduled after this failed attempt.
	WillRetry bool
}

// Logger is a callback that receives one LogEntry per HTTP request attempt.
// Pass DebugLogger for built-in stdout output or supply your own to route entries
// into pino, zap, zerolog, or any other observability pipeline.
type Logger func(entry LogEntry)

// DebugLogger is a built-in Logger that prints a one-line summary to stdout.
//
//	client := rail0.NewClient(rail0.ClientOptions{BaseURL: "...", Logger: rail0.DebugLogger})
func DebugLogger(e LogEntry) {
	flag := ""
	if e.Err != nil {
		flag = " ERROR"
	}
	attempt := ""
	if e.Attempt > 0 {
		retry := ""
		if e.WillRetry {
			retry = ", retrying"
		}
		attempt = fmt.Sprintf(" [attempt %d%s]", e.Attempt, retry)
	}
	status := ""
	if e.Status != 0 {
		status = fmt.Sprintf(" %d", e.Status)
	}
	fmt.Printf("[rail0]%s%s %s%s %s %dms\n", flag, attempt, e.Method, status, e.URL, e.DurationMs)
}

// ClientOptions configures the Client (and the underlying HTTP transport).
type ClientOptions struct {
	// BaseURL is the RAIL0 API base URL, e.g. "https://api.rail0.xyz". Trailing slash is stripped.
	BaseURL string
	// Headers are merged into every request. Useful for API keys or correlation IDs.
	Headers map[string]string
	// Timeout is the per-request timeout. Default: 30s.
	Timeout time.Duration
	// Logger receives one entry per request attempt. Optional.
	Logger Logger
	// MaxRetries is the number of extra attempts after the first network failure.
	// Only network errors and timeouts are retried — HTTP errors are not. Default: 0.
	MaxRetries int
	// RetryDelay is the base delay between retries; doubles each attempt (exponential backoff).
	// Default: 200ms.
	RetryDelay time.Duration
	// Transport overrides the HTTP transport. When nil, http.DefaultTransport is used.
	// Useful for testing or for custom TLS/proxy configuration.
	Transport http.RoundTripper
}

type httpClient struct {
	baseURL    string
	headers    map[string]string
	timeout    time.Duration
	logger     Logger
	maxRetries int
	retryDelay time.Duration
	http       *http.Client
}

func newHTTPClient(opts ClientOptions) *httpClient {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	retryDelay := opts.RetryDelay
	if retryDelay == 0 {
		retryDelay = 200 * time.Millisecond
	}
	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range opts.Headers {
		headers[k] = v
	}
	return &httpClient{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		headers:    headers,
		timeout:    timeout,
		logger:     opts.Logger,
		maxRetries: opts.MaxRetries,
		retryDelay: retryDelay,
		http:       &http.Client{Transport: opts.Transport},
	}
}

func (c *httpClient) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *httpClient) post(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *httpClient) put(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPut, path, body, out)
}

func (c *httpClient) patch(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPatch, path, body, out)
}

// postWithHeaders issues a POST with per-request headers merged over the client
// defaults. Used for one-off headers such as Idempotency-Key.
func (c *httpClient) postWithHeaders(ctx context.Context, path string, body any, out any, headers map[string]string) error {
	return c.doWithHeaders(ctx, http.MethodPost, path, body, out, headers)
}

func (c *httpClient) delete(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, out)
}

func (c *httpClient) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doWithHeaders(ctx, method, path, body, out, nil)
}

// doWithHeaders is the core request loop. extraHeaders are merged over the
// client's default headers for this single request only.
func (c *httpClient) doWithHeaders(ctx context.Context, method, path string, body any, out any, extraHeaders map[string]string) error {
	url := c.baseURL + path
	maxAttempts := c.maxRetries + 1
	trackAttempts := c.maxRetries > 0

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			delay := c.retryDelay * time.Duration(1<<uint(attempt-2))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		var reqBody io.Reader
		var bodySnapshot any
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("rail0: marshal request body: %w", err)
			}
			reqBody = bytes.NewReader(b)
			bodySnapshot = body
		}

		reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
		req, err := http.NewRequestWithContext(reqCtx, method, url, reqBody)
		if err != nil {
			cancel()
			return fmt.Errorf("rail0: build request: %w", err)
		}
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}

		start := time.Now()
		resp, err := c.http.Do(req)
		elapsed := time.Since(start).Milliseconds()

		if err != nil {
			cancel()
			willRetry := attempt < maxAttempts
			if c.logger != nil {
				c.logger(LogEntry{
					Method:      method,
					URL:         url,
					RequestBody: bodySnapshot,
					DurationMs:  elapsed,
					Err:         err,
					Attempt:     attemptField(trackAttempts, attempt),
					WillRetry:   willRetry,
				})
			}
			if willRetry {
				continue
			}
			return err
		}

		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var errBody APIErrorBody
			_ = json.Unmarshal(raw, &errBody)
			if errBody.Code == "" {
				errBody.Code = "UnknownError"
				errBody.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
			apiErr := &APIError{Status: resp.StatusCode, Code: errBody.Code, Message: errBody.Message}
			if c.logger != nil {
				c.logger(LogEntry{
					Method:       method,
					URL:          url,
					RequestBody:  bodySnapshot,
					Status:       resp.StatusCode,
					DurationMs:   elapsed,
					ResponseBody: errBody,
					Err:          apiErr,
					Attempt:      attemptField(trackAttempts, attempt),
				})
			}
			return apiErr
		}

		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("rail0: decode response: %w", err)
			}
		}
		if c.logger != nil {
			c.logger(LogEntry{
				Method:       method,
				URL:          url,
				RequestBody:  bodySnapshot,
				Status:       resp.StatusCode,
				DurationMs:   elapsed,
				ResponseBody: out,
				Attempt:      attemptField(trackAttempts, attempt),
			})
		}
		return nil
	}

	// Loop always executes at least once and either returns or re-enters via continue.
	// This line satisfies the Go compiler's control-flow checker.
	return fmt.Errorf("rail0: unreachable")
}

func attemptField(track bool, attempt int) int {
	if track {
		return attempt
	}
	return 0
}
