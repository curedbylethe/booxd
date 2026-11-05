package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"
)

// DefaultUserAgent identifies requests sent by the client.
const DefaultUserAgent = "booxd/0.1"

// DefaultTimeout bounds an HTTP request by default and response headers when body timeout is disabled.
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
		if transport, ok := c.http.Transport.(*http.Transport); ok {
			clone := transport.Clone()
			clone.Proxy = http.ProxyURL(proxy)
			c.http.Transport = clone
			return
		}
		// shortcut: test doubles cannot be cloned; use a minimal transport only for configured proxies.
		c.http.Transport = &http.Transport{Proxy: http.ProxyURL(proxy)}
	}
}

// WithTimeout sets the request timeout. Zero disables the whole-request timeout
// but still limits response-header waits to DefaultTimeout; negative keeps the default.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout == 0 {
			c.http.Timeout = 0
			if transport, ok := c.http.Transport.(*http.Transport); ok {
				clone := transport.Clone()
				clone.ResponseHeaderTimeout = DefaultTimeout
				c.http.Transport = clone
			}
		} else if timeout > 0 {
			c.http.Timeout = timeout
		} else {
			c.http.Timeout = DefaultTimeout
		}
	}
}

// WithJar sets the cookie jar used for requests. A nil jar keeps the default.
func WithJar(jar http.CookieJar) Option {
	return func(c *Client) {
		if jar != nil {
			c.http.Jar = jar
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
	statePath string
	stateMu   sync.Mutex
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
	if runtime.GOOS == "android" {
		if transport := androidTransport(); transport != nil {
			c.http.Transport = transport
		}
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ErrHostUnhealthy indicates a host returned a protection challenge.
var ErrHostUnhealthy = errors.New("fetch: host unhealthy after a 403 challenge")

// ErrRateLimited indicates that a 429 backoff wait was interrupted.
var ErrRateLimited = errors.New("fetch: waiting for rate limit")

// Response contains the unmodified HTTP status, headers and body.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Stream contains an HTTP response whose body remains live until closed.
type Stream struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
	err     error
}

func (b *releaseBody) Close() error {
	b.once.Do(func() {
		b.err = b.ReadCloser.Close()
		b.release()
	})
	return b.err
}

func bufferedStream(result *Response) *Stream {
	return &Stream{StatusCode: result.StatusCode, Header: result.Header, Body: io.NopCloser(bytes.NewReader(result.Body))}
}

// Open sends a request and returns its unread response body. The host slot
// stays held until Body.Close. Open never consults the cache lookup.
func (c *Client) Open(ctx context.Context, rawURL string, header http.Header) (*Stream, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if header != nil {
		req.Header = header.Clone()
	}
	req.Header.Set("User-Agent", c.userAgent)

	state, ok := c.hosts.Load(u.Host)
	if !ok {
		record := c.loadRetryState().Hosts[u.Host]
		if limit := c.clock.Now().Add(maxRetryAfter); record.RetryUntil.After(limit) {
			record.RetryUntil = limit
		}
		state, _ = c.hosts.LoadOrStore(u.Host, &hostState{next: record.RetryUntil, streak: record.Streak})
	}
	st := state.(*hostState)
	st.mu.Lock()
	locked := true
	release := func() {
		if locked {
			locked = false
			st.mu.Unlock()
		}
	}
	if c.clock.Now().Before(st.unhealthyUntil) {
		release()
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
					release()
					return bufferedStream(last429), nil
				}
				if st.streak > 0 {
					release()
					return nil, fmt.Errorf("%w: %s: %w", ErrRateLimited, u.Host, err)
				}
				release()
				return nil, err
			}
			waited += d
		}
		if c.verbose != nil {
			_, _ = fmt.Fprintf(c.verbose, "fetch: host=%s attempt=%d status=%s wait=%s\n", u.Host, attempt, status, waited)
		}
		st.next = c.clock.Now().Add(c.interval)
		started := time.Now()
		resp, err := c.http.Do(req)
		if err != nil {
			release()
			return nil, timeoutError(u.Host, "connecting or waiting for a response", started, err)
		}
		result := &Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone()}
		if result.StatusCode == http.StatusTooManyRequests {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				release()
				return nil, readErr
			}
			result.Body = body
			last429 = result
			st.streak++
			wait := retryWait(result.Header, st.streak, c.clock.Now())
			// Preserve the server's deadline even when this call ends before retrying.
			deadline := c.clock.Now().Add(wait)
			if deadline.After(st.next) {
				st.next = deadline
			}
			c.saveRetryState(u.Host, hostRecord{RetryUntil: st.next, Streak: st.streak, UpdatedAt: c.clock.Now()})
			if attempt == maxAttempts {
				release()
				return bufferedStream(result), nil
			}
			if c.verbose != nil {
				_, _ = fmt.Fprintf(c.verbose, "fetch: host=%s status=%d waiting=%s\n", u.Host, result.StatusCode, wait)
			}
			if err := c.clock.Wait(ctx, wait); err != nil {
				release()
				return bufferedStream(result), nil
			}
			waited = wait
			status = fmt.Sprint(result.StatusCode)
			continue
		}
		if st.streak != 0 {
			c.saveRetryState(u.Host, hostRecord{})
		}
		st.streak = 0
		last429 = nil
		if result.StatusCode >= 500 && result.StatusCode <= 599 {
			if attempt < maxAttempts {
				_, readErr := io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					release()
					return nil, readErr
				}
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
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			release()
			return nil, fmt.Errorf("fetch: %s returned %d %s after %d attempts", u.Host, result.StatusCode, http.StatusText(result.StatusCode), attempt)
		}
		if result.StatusCode == http.StatusForbidden {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				release()
				return nil, readErr
			}
			result.Body = body
		}
		if marker, ok := challenge(result); ok {
			st.unhealthyUntil = c.clock.Now().Add(unhealthyFor)
			release()
			return nil, fmt.Errorf("%w: %s (%s)", ErrHostUnhealthy, u.Host, marker)
		}
		if result.Body != nil {
			release()
			return bufferedStream(result), nil
		}
		return &Stream{StatusCode: result.StatusCode, Header: result.Header, Body: &releaseBody{ReadCloser: resp.Body, release: release}}, nil
	}
}

// timeoutError names the host, phase and time spent when err is a timeout, so a
// slow network reads differently from a refused one. It keeps err for errors.Is.
func timeoutError(host, phase string, started time.Time, err error) error {
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return fmt.Errorf("fetch: %s timed out %s after %s: %w", host, phase, time.Since(started).Round(100*time.Millisecond), err)
	}
	return err
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
	stream, err := c.Open(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	body, readErr := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if readErr != nil {
		return nil, timeoutError(u.Host, "reading the response", started, readErr)
	}
	return &Response{StatusCode: stream.StatusCode, Header: stream.Header, Body: body}, nil
}
