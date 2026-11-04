package mirror

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
)

const (
	probeTimeout = 5 * time.Second
	probePath    = "/search?q=test"
	passMarker   = "<title>Search results"
)

type outcome int

const (
	fail outcome = iota
	pass
	dead
)

func probe(ctx context.Context, c *fetch.Client, base string, timeout time.Duration) outcome {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.Get(ctx, strings.TrimRight(base, "/")+probePath)
	if err != nil {
		var timeoutErr interface{ Timeout() bool }
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			return dead
		}
		return fail
	}
	if resp.StatusCode == 403 || resp.StatusCode == 404 {
		return dead
	}
	if resp.StatusCode == 200 && bytes.Contains(resp.Body, []byte(passMarker)) {
		return pass
	}
	return fail
}
