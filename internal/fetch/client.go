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

const userAgent = "booxd/0.1"

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

// WithVerbose enables attempt logging to w.
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
	hosts     sync.Map
	clock     Clock
	interval  time.Duration
	verbose   io.Writer
	verboseMu sync.Mutex
	lookup    Lookup
}

// New returns the shared HTTP client.
func New(opts ...Option) *Client {
	c := &Client{clock: systemClock{}, interval: DefaultInterval, http: &http.Client{
		Transport: http.DefaultTransport,
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
	req.Header.Set("User-Agent", userAgent)

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
			if err := c.clock.Wait(ctx, d); err != nil {
				if last429 != nil {
					return last429, nil
				}
				return nil, err
			}
			waited += d
		}
		if c.verbose != nil {
			c.verboseMu.Lock()
			_, _ = fmt.Fprintf(c.verbose, "fetch: host=%s attempt=%d status=%s wait=%s\n", u.Host, attempt, status, waited)
			c.verboseMu.Unlock()
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
			if attempt == maxAttempts {
				return result, nil
			}
			wait := retryWait(result.Header, st.streak, c.clock.Now())
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
