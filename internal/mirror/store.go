package mirror

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	stateTTL = 24 * time.Hour
	cooldown = time.Hour
)

type state struct {
	BaseURL   string               `json:"base_url"`
	CheckedAt time.Time            `json:"checked_at"`
	DeadUntil map[string]time.Time `json:"dead_until"`
}

// StatePath returns the default mirror cache file without creating it.
func StatePath(cacheDir string) (string, error) {
	if cacheDir == "" {
		var err error
		cacheDir, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(cacheDir, "booxd", "mirror.json"), nil
}

func loadState(path string) state {
	var s state
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &s) != nil {
		s = state{}
	}
	if s.DeadUntil == nil {
		s.DeadUntil = make(map[string]time.Time)
	}
	return s
}

func saveState(path string, s state) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mirror-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := json.NewEncoder(tmp).Encode(s); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
