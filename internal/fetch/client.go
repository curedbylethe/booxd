package fetch

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
)

const userAgent = "booxd/0.1"

// Client sends requests through a shared transport, one at a time per host.
type Client struct {
	http  *http.Client
	hosts sync.Map
}

// New returns the shared HTTP client.
func New() *Client {
	return &Client{http: &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
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

	lock, _ := c.hosts.LoadOrStore(u.Host, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

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
