package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// DefaultUserAgent identifies requests sent by the client.
const DefaultUserAgent = "booxd/0.1"

// DefaultTimeout bounds the full lifetime of an HTTP request.
const DefaultTimeout = 30 * time.Second

// DefaultInterval is the minimum time between request starts to one host.
const DefaultInterval = time.Second

// Clock supplies time and cancellable waits to the client.
type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
func (systemClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Option configures a client.
type Option func(*Client)

// Lookup returns a cached response for rawURL. ok is false on a miss.
// It must not perform network I/O.
type Lookup func(ctx context.Context, rawURL string) (status int, body []byte, ok bool)

// WithLookup sets the cache lookup used before fetching.
func WithLookup(lookup Lookup) Option { return func(c *Client) { c.lookup = lookup } }

// WithInterval sets the minimum spacing between request starts.
func WithInterval(d time.Duration) Option { return func(c *Client) { c.interval = d } }

// WithUserAgent sets the request User-Agent. An empty value keeps the default.
func WithUserAgent(userAgent string) Option {
	return func(c *Client) {
		if userAgent != "" {
			c.userAgent = userAgent
		}
	}
}

// WithProxy sets the proxy used by the client. A nil URL keeps the default transport.
func WithProxy(proxy *url.URL) Option {
	return func(c *Client) {
		if proxy == nil {
			return
		}
		if transport, ok := http.DefaultTransport.(*http.Transport); ok {
			clone := transport.Clone()
			clone.Proxy = http.ProxyURL(proxy)
			c.http.Transport = clone
			return
		}
		// shortcut: test doubles cannot be cloned; use a minimal transport only for configured proxies.
		c.http.Transport = &http.Transport{Proxy: http.ProxyURL(proxy)}
	}
}

// WithTimeout sets the request timeout. A non-positive value keeps the default.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.http.Timeout = timeout
		}
	}
}

// WithVerbose enables attempt logging to w. The writer must support concurrent writes.
func WithVerbose(w io.Writer) Option { return func(c *Client) { c.verbose = w } }

// WithClock sets the clock used for request spacing.
func WithClock(clock Clock) Option { return func(c *Client) { c.clock = clock } }

type hostState struct {
	mu             sync.Mutex
	next           time.Time
	streak         int
	unhealthyUntil time.Time
}

// Client sends requests through a shared transport, one at a time per host.
type Client struct {
	http      *http.Client
	userAgent string
	hosts     sync.Map
	clock     Clock
	interval  time.Duration
	verbose   io.Writer
	lookup    Lookup
}

// New returns the shared HTTP client.
func New(opts ...Option) *Client {
	c := &Client{clock: systemClock{}, interval: DefaultInterval, userAgent: DefaultUserAgent, http: &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   DefaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ErrHostUnhealthy indicates a host returned a protection challenge.
var ErrHostUnhealthy = errors.New("fetch: host unhealthy after a 403 challenge")

// Response contains the unmodified HTTP status, headers and body.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Get reads and closes a response before releasing the host's request slot.
// An interrupted 429 backoff returns the last response rather than a context error.
func (c *Client) Get(ctx context.Context, rawURL string) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if c.lookup != nil {
		if status, body, ok := c.lookup(ctx, rawURL); ok {
			return &Response{StatusCode: status, Body: body}, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	state, _ := c.hosts.LoadOrStore(u.Host, &hostState{})
	st := state.(*hostState)
	st.mu.Lock()
	defer st.mu.Unlock()
	if c.clock.Now().Before(st.unhealthyUntil) {
		return nil, fmt.Errorf("%w: %s", ErrHostUnhealthy, u.Host)
	}
	// Hold the host slot across backoff so other requests cannot bypass it.
	status := "-"
	var waited time.Duration
	var last429 *Response
	for attempt := 1; ; attempt++ {
		if d := st.next.Sub(c.clock.Now()); d > 0 {
			if c.verbose != nil {
				_, _ = fmt.Fprintf(c.verbose, "fetch: host=%s waiting=%s\n", u.Host, d)
			}
			if err := c.clock.Wait(ctx, d); err != nil {
				if last429 != nil {
					return last429, nil
				}
				return nil, err
			}
			waited += d
		}
		if c.verbose != nil {
			_, _ = fmt.Fprintf(c.verbose, "fetch: host=%s attempt=%d status=%s wait=%s\n", u.Host, attempt, status, waited)
		}
		st.next = c.clock.Now().Add(c.interval)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		result := &Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: body}
		if result.StatusCode == http.StatusTooManyRequests {
			last429 = result
			st.streak++
			wait := retryWait(result.Header, st.streak, c.clock.Now())
			// Preserve the server's deadline even when this call ends before retrying.
			deadline := c.clock.Now().Add(wait)
			if deadline.After(st.next) {
				st.next = deadline
			}
			if attempt == maxAttempts {
				return result, nil
			}
			if c.verbose != nil {
				_, _ = fmt.Fprintf(c.verbose, "fetch: host=%s status=%d waiting=%s\n", u.Host, result.StatusCode, wait)
			}
			if err := c.clock.Wait(ctx, wait); err != nil {
				return result, nil
			}
			waited = wait
			status = fmt.Sprint(result.StatusCode)
			continue
		}
		last429 = nil
		st.streak = 0
		if result.StatusCode >= 500 && result.StatusCode <= 599 {
			if attempt < maxAttempts {
				wait := serverErrorWait(attempt)
				if c.verbose != nil {
					_, _ = fmt.Fprintf(c.verbose, "fetch: host=%s status=%d waiting=%s\n", u.Host, result.StatusCode, wait)
				}
				if err := c.clock.Wait(ctx, wait); err == nil {
					waited = wait
					status = fmt.Sprint(result.StatusCode)
					continue
				}
			}
			return nil, fmt.Errorf("fetch: %s returned %d %s after %d attempts", u.Host, result.StatusCode, http.StatusText(result.StatusCode), attempt)
		}
		if marker, ok := challenge(result); ok {
			st.unhealthyUntil = c.clock.Now().Add(unhealthyFor)
			return nil, fmt.Errorf("%w: %s (%s)", ErrHostUnhealthy, u.Host, marker)
		}
		return result, nil
	}
}
