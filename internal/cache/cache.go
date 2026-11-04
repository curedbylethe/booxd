package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultTTL = 24 * time.Hour
	SweepAge   = 30 * 24 * time.Hour
)

type Entry struct {
	Query     string    `json:"query"`
	Page      int       `json:"page"`
	Status    int       `json:"status"`
	FetchedAt time.Time `json:"fetched_at"`
	Body      []byte    `json:"body"`
}

// Path returns the default query cache directory without creating it.
func Path(base string) (string, error) {
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "booxd", "cache"), nil
}

// Normalise trims, collapses whitespace, and lowercases a query.
func Normalise(query string) string {
	return strings.ToLower(strings.Join(strings.Fields(query), " "))
}

// Key returns the filename stem for a normalised query and page.
func Key(query string, page int) string {
	sum := sha256.Sum256([]byte(Normalise(query) + "\x00" + strconv.Itoa(page)))
	return hex.EncodeToString(sum[:])
}

type Store struct{ dir string }

// Open returns a store rooted at dir.
func Open(dir string) *Store { return &Store{dir: dir} }

func (s *Store) Get(query string, page int) (Entry, bool) {
	var entry Entry
	data, err := os.ReadFile(filepath.Join(s.dir, Key(query, page)+".json"))
	if err != nil || json.Unmarshal(data, &entry) != nil || Normalise(entry.Query) != Normalise(query) || entry.Page != page {
		return Entry{}, false
	}
	return entry, true
}

func (s *Store) Put(entry Entry) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".cache-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := json.NewEncoder(tmp).Encode(entry); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.dir, Key(entry.Query, entry.Page)+".json"))
}

func (s *Store) Sweep(maxAge time.Duration) error {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
	}
	return nil
}
