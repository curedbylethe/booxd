package fetch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const stateTTL = 24 * time.Hour

type hostRecord struct {
	RetryUntil time.Time `json:"retry_until"`
	Streak     int       `json:"streak"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type retryState struct {
	Hosts map[string]hostRecord `json:"hosts"`
}

// StatePath returns the retry state path under cacheDir without creating it.
// An empty cacheDir uses the platform user cache directory.
func StatePath(cacheDir string) (string, error) {
	if cacheDir == "" {
		var err error
		cacheDir, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(cacheDir, "booxd", "fetch.json"), nil
}

// WithStatePath enables durable per-host 429 state. An empty path disables persistence.
func WithStatePath(path string) Option { return func(c *Client) { c.statePath = path } }

func (c *Client) loadRetryState() retryState {
	s := retryState{Hosts: make(map[string]hostRecord)}
	if c.statePath == "" {
		return s
	}
	data, err := os.ReadFile(c.statePath)
	if err != nil || json.Unmarshal(data, &s) != nil || s.Hosts == nil {
		return retryState{Hosts: make(map[string]hostRecord)}
	}
	for host, record := range s.Hosts {
		if record.UpdatedAt.IsZero() || record.UpdatedAt.Add(stateTTL).Before(c.clock.Now()) || record == (hostRecord{}) {
			delete(s.Hosts, host)
			continue
		}
		if record.Streak < 0 {
			record.Streak = 0
			s.Hosts[host] = record
		}
	}
	return s
}

func (c *Client) saveRetryState(host string, record hostRecord) {
	if c.statePath == "" {
		return
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	s := c.loadRetryState()
	if record == (hostRecord{}) {
		delete(s.Hosts, host)
	} else {
		s.Hosts[host] = record
	}
	dir := filepath.Dir(c.statePath)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".fetch-*.tmp")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if json.NewEncoder(tmp).Encode(s) != nil {
		_ = tmp.Close()
		return
	}
	if tmp.Close() != nil {
		return
	}
	_ = os.Rename(tmp.Name(), c.statePath)
}
