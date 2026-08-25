package rail0

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	// Token seeds the JWT sent as "Authorization: Bearer <token>" on every request.
	// It is applied after Headers (so it wins over a static Authorization header) and
	// can be replaced later with Client.SetToken — or set automatically by Auth.Login /
	// Auth.Verify, which stash the JWT they obtain on the shared client. A per-request
	// Authorization header (postWithHeaders) still overrides it. Leave empty for the
	// public endpoints or to authenticate via Headers instead.
	Token string
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
	// RetryOn429 opts in to retrying a 429 Too Many Requests response, waiting the
	// server's Retry-After (clamped by RetryAfterCap, plus jitter) and falling back to the
	// exponential backoff when the header is absent. Default: false.
	//
	// It does NOT need MaxRetries set as well. It used to — "set MaxRetries > 0 for it to
	// take effect" — which made the flag a silent no-op for anyone who set only this one,
	// since maxAttempts is MaxRetries+1 and the zero value gives a single attempt. On its
	// own it now allows one retry; with MaxRetries it uses that budget.
	//
	// A 429 is the only HTTP status retried, on any method including POST, and the reason
	// is not that it is common: the gateway rejects it in middleware, BEFORE the request
	// reaches the application, so nothing ran and nothing can run twice. That is not true
	// of a 502 or a timeout on a capture, where the broadcast may already be in flight.
	RetryOn429 bool
	// RetryAfterCap bounds how long a single Retry-After may park a request.
	// Default: DefaultRetryAfterCap (60s).
	//
	// A gateway older than rail0-gateway#201 sends its whole throttle period rather than
	// the time left in the window, so a limit hit one second in asks for the full 60. This
	// bounds that over-wait, and any hostile value from anything in between.
	RetryAfterCap time.Duration
	// Transport overrides the HTTP transport. When nil, http.DefaultTransport is used.
	// Useful for testing or for custom TLS/proxy configuration.
	Transport http.RoundTripper
}

type httpClient struct {
	baseURL       string
	headers       map[string]string
	timeout       time.Duration
	logger        Logger
	maxRetries    int
	retryDelay    time.Duration
	retryOn429    bool
	retryAfterCap time.Duration
	http          *http.Client

	// mu guards token, which may be mutated after construction (SetToken, or the
	// automatic stash by Auth.Login/Verify) while requests read it concurrently.
	mu    sync.RWMutex
	token string
}

// SetToken replaces the JWT sent as "Authorization: Bearer <token>" on subsequent
// requests. Safe to call concurrently with in-flight requests. Pass "" to clear it.
func (c *httpClient) SetToken(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

func (c *httpClient) getToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
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
	retryAfterCap := opts.RetryAfterCap
	if retryAfterCap <= 0 {
		retryAfterCap = DefaultRetryAfterCap
	}
	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range opts.Headers {
		headers[k] = v
	}
	return &httpClient{
		baseURL:       strings.TrimRight(opts.BaseURL, "/"),
		headers:       headers,
		timeout:       timeout,
		logger:        opts.Logger,
		maxRetries:    opts.MaxRetries,
		retryDelay:    retryDelay,
		retryOn429:    opts.RetryOn429,
		retryAfterCap: retryAfterCap,
		http:          &http.Client{Transport: opts.Transport},
		token:         opts.Token,
	}
}

func (c *httpClient) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// getList issues a GET for a collection endpoint and returns the response
// headers alongside decoding the body. List endpoints return a bare JSON array
// with pagination carried in X-Total-Count / X-Page / X-Per-Page headers (not a
// JSON envelope), so callers read the meta from the returned headers.
func (c *httpClient) getList(ctx context.Context, path string, out any) (http.Header, error) {
	var hdr http.Header
	err := c.doWithHeaders(ctx, http.MethodGet, path, nil, out, nil, &hdr)
	return hdr, err
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
	return c.doWithHeaders(ctx, http.MethodPost, path, body, out, headers, nil)
}

func (c *httpClient) delete(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, out)
}

func (c *httpClient) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doWithHeaders(ctx, method, path, body, out, nil, nil)
}

// doWithHeaders is the core request loop. extraHeaders are merged over the
// client's default headers for this single request only.
func (c *httpClient) doWithHeaders(ctx context.Context, method, path string, body any, out any, extraHeaders map[string]string, respHeader *http.Header) error {
	url := c.baseURL + path
	// A retry budget of at least 1 when RetryOn429 is on, so the flag works on its own:
	// requiring MaxRetries as well made it a silent no-op (see RetryOn429).
	retryBudget := c.maxRetries
	if c.retryOn429 && retryBudget < 1 {
		retryBudget = 1
	}
	maxAttempts := retryBudget + 1
	trackAttempts := retryBudget > 0
	// override carries the delay to wait before the NEXT attempt; < 0 means "use the
	// exponential backoff". A retried 429 sets it to its Retry-After so we honour the
	// server's pacing instead of the immediate network-error backoff.
	override := time.Duration(-1)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// A network error carries no instruction, so it takes the jittered exponential
			// path; a 429 sets override to its Retry-After. Both go through throttleDelay,
			// which is what applies the cap and the jitter — see backoff.go for why the
			// jitter is additive on the server's number and multiplicative on ours.
			delay := throttleDelay(override, c.retryDelay, c.retryAfterCap, attempt-1, -1)
			override = -1
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
		// Apply the stashed JWT after the static headers (so it wins over a static
		// Authorization) but before extraHeaders (so a per-request header still overrides).
		if tok := c.getToken(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
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
			// `code` is the specific condition and is what a caller branches on, and
			// it is now the only one there is: the `status`/`message`/`error` aliases
			// were removed from the wire, so the fallback chain that used to read them
			// could only ever read absent fields. The bare HTTP status is still used
			// when the body carries no text at all — never clobbering a real message.
			code := errBody.Code
			detail := errBody.Detail
			if code == "" && detail == "" {
				detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
			apiErr := &APIError{
				Status:     resp.StatusCode,
				Code:       code,
				Title:      errBody.Title,
				Detail:     detail,
				Message:    detail,
				Reason:     errBody.Reason,
				RetryAfter: parseRetryAfter(resp.Header),
			}
			// A 429 is retried only when opted in (RetryOn429) and the retry budget is
			// not exhausted — sleeping for the server's Retry-After rather than the
			// immediate backoff used for network errors.
			willRetry := resp.StatusCode == http.StatusTooManyRequests && c.retryOn429 && attempt < maxAttempts
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
					WillRetry:    willRetry,
				})
			}
			if willRetry {
				// Only a POSITIVE Retry-After overrides the backoff. parseRetryAfter
				// returns 0 for an absent, unparseable or already-elapsed header — and 0
				// is a real duration to the `delay < 0` guard above, so storing it would
				// mean time.After(0) and a burst of back-to-back requests against the
				// very limiter that asked us to slow down. Leaving override at -1 is what
				// makes the documented "falls back to the exponential backoff" true.
				if apiErr.RetryAfter > 0 {
					override = apiErr.RetryAfter
				}
				continue
			}
			return apiErr
		}

		if respHeader != nil {
			*respHeader = resp.Header
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

// parseRetryAfter reads an HTTP Retry-After header into a delay. The header comes
// in two RFC 7231 forms: a non-negative number of seconds, or an HTTP-date to wait
// until. Returns 0 when the header is absent, unparseable, or already in the past.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
