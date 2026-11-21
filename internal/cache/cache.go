package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/source"
)

const (
	DefaultTTL = 24 * time.Hour
	SweepAge   = 30 * 24 * time.Hour
)

// Entry is one cached response. Key is the identity it was stored under;
// Query and Page describe the subject for debugging only. Store only public
// responses: nothing tied to a session or account belongs here.
type Entry struct {
	Key       string    `json:"key"`
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

// Key returns the filename stem for one identity string. Callers build their
// own identity; Key does not normalise.
func Key(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

// SearchKey identifies a cached search page by provider, normalised query and
// page. A future search option that changes results is added here.
func SearchKey(src source.Name, query string, page int) string {
	return Key("search\x00" + string(src) + "\x00" + Normalise(query) + "\x00" + strconv.Itoa(page))
}

// RefKey identifies a cached detail page by its provider-qualified reference.
func RefKey(ref source.Ref) string { return Key("book\x00" + ref.Key()) }

type Store struct{ dir string }

// Open returns a store rooted at dir.
func Open(dir string) *Store { return &Store{dir: dir} }

// Get returns the entry stored under key, or false when the file is missing,
// unreadable, or was stored under a different key.
func (s *Store) Get(key string) (Entry, bool) {
	var entry Entry
	data, err := os.ReadFile(filepath.Join(s.dir, key+".json"))
	if err != nil || json.Unmarshal(data, &entry) != nil || entry.Key != key {
		return Entry{}, false
	}
	return entry, true
}

// Put stores entry under entry.Key.
func (s *Store) Put(entry Entry) error {
	if entry.Key == "" {
		return errors.New("cache: entry has no key")
	}
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
	return os.Rename(tmp.Name(), filepath.Join(s.dir, entry.Key+".json"))
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
