package fetch

import (
	"context"
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

// WithInterval sets the minimum spacing between request starts.
func WithInterval(d time.Duration) Option { return func(c *Client) { c.interval = d } }

// WithClock sets the clock used for request spacing.
func WithClock(clock Clock) Option { return func(c *Client) { c.clock = clock } }

type hostState struct {
	mu   sync.Mutex
	next time.Time
}

// Client sends requests through a shared transport, one at a time per host.
type Client struct {
	http     *http.Client
	hosts    sync.Map
	clock    Clock
	interval time.Duration
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

// Response contains the unmodified HTTP status and body.
type Response struct {
	StatusCode int
	Body       []byte
}

// Get reads and closes a response before releasing the host's request slot.
func (c *Client) Get(ctx context.Context, rawURL string) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
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
	if d := st.next.Sub(c.clock.Now()); d > 0 {
		if err := c.clock.Wait(ctx, d); err != nil {
			return nil, err
		}
	}
	st.next = c.clock.Now().Add(c.interval)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{StatusCode: resp.StatusCode, Body: body}, nil
}
