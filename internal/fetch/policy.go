package fetch

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRetryAfter = 60 * time.Second
	maxRetryAfter     = 15 * time.Minute
	unhealthyFor      = time.Hour
	maxAttempts       = 3
)

func retryWait(h http.Header, streak int, now time.Time) time.Duration {
	value := strings.TrimSpace(h.Get("Retry-After"))
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		if seconds >= int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return min(max(when.Sub(now), 0), maxRetryAfter)
	}
	return min(defaultRetryAfter<<min(max(streak-1, 0), 4), maxRetryAfter)
}

func challenge(resp *Response) (string, bool) {
	if resp.StatusCode != http.StatusForbidden {
		return "", false
	}
	server := strings.ToLower(resp.Header.Get("Server"))
	for _, marker := range []string{"cloudflare", "ddos-guard"} {
		if strings.Contains(server, marker) {
			return marker, true
		}
	}
	for _, marker := range []string{"Cf-Ray", "Cf-Mitigated"} {
		if resp.Header.Get(marker) != "" {
			return marker, true
		}
	}
	for _, cookie := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(strings.ToLower(cookie), "__ddg") {
			return "__ddg", true
		}
	}
	body := strings.ToLower(string(resp.Body))
	for _, marker := range []string{"ddos-guard", "just a moment"} {
		if strings.Contains(body, marker) {
			return marker, true
		}
	}
	return "", false
}
