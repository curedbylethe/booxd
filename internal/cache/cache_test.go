package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/source"
)

func TestKeys(t *testing.T) {
	anna, zlib := source.Ref{Source: source.Anna, ID: "1234"}, source.Ref{Source: source.ZLibrary, ID: "1234", Hash: "ab"}
	tests := []struct {
		name string
		a, b string
		same bool
	}{
		{"normalised queries share a key", SearchKey(source.Anna, " Crime   Punishment ", source.SearchOptions{Page: 1}), SearchKey(source.Anna, "crime punishment", source.SearchOptions{Page: 1}), true},
		{"pages differ", SearchKey(source.Anna, "q", source.SearchOptions{Page: 1}), SearchKey(source.Anna, "q", source.SearchOptions{Page: 2}), false},
		{"queries differ", SearchKey(source.Anna, "q", source.SearchOptions{Page: 1}), SearchKey(source.Anna, "other", source.SearchOptions{Page: 1}), false},
		{"sources differ", SearchKey(source.Anna, "q", source.SearchOptions{Page: 1}), SearchKey(source.ZLibrary, "q", source.SearchOptions{Page: 1}), false},
		{"languages differ", SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Languages: []string{"en"}}), SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Languages: []string{"fa"}}), false},
		{"formats differ", SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Formats: []string{"epub"}}), SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Formats: []string{"pdf"}}), false},
		{"language order is irrelevant", SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Languages: []string{"en", "fa"}}), SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Languages: []string{"fa", "en"}}), true},
		{"duplicate languages are irrelevant", SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Languages: []string{"en", "en"}}), SearchKey(source.Anna, "q", source.SearchOptions{Page: 1, Languages: []string{"en"}}), true},
		{"same id across sources differs", RefKey(anna), RefKey(zlib), false},
		{"hash differs", RefKey(zlib), RefKey(source.Ref{Source: source.ZLibrary, ID: "1234", Hash: "cd"}), false},
		{"search and detail differ", SearchKey(source.Anna, "1234", source.SearchOptions{Page: 0}), RefKey(anna), false},
		{"identity is hashed as given", Key("a"), Key("a"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if (tc.a == tc.b) != tc.same {
				t.Fatalf("keys %q and %q: same=%t, want %t", tc.a, tc.b, tc.a == tc.b, tc.same)
			}
		})
	}
	if Key("A") == Key("a") {
		t.Fatal("Key must not normalise")
	}
}

func TestStorePutGetAndRejectBadEntries(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	key := SearchKey(source.Anna, "Crime  Punishment", source.SearchOptions{Page: 1})
	want := Entry{Key: key, Query: "Crime  Punishment", Page: 1, Status: 200, FetchedAt: time.Now(), Body: []byte("body")}
	if err := store.Put(want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil || decoded["fetched_at"] == nil || decoded["key"] == nil {
		t.Fatalf("fetched_at or key missing or invalid JSON: %v", err)
	}
	got, ok := store.Get(key)
	if !ok || string(got.Body) != "body" {
		t.Fatalf("Get() = %#v, %v", got, ok)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(files) != 0 {
		t.Fatalf("temporary files remain: %v", files)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(key); ok {
		t.Fatal("corrupt entry must miss")
	}
	wrong := want
	wrong.Key = SearchKey(source.ZLibrary, "Crime  Punishment", source.SearchOptions{Page: 1})
	data, _ = json.Marshal(wrong)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(key); ok {
		t.Fatal("an entry stored under another key must miss")
	}
}

func TestPutNeedsKey(t *testing.T) {
	dir := t.TempDir()
	if err := Open(dir).Put(Entry{Query: "q"}); err == nil {
		t.Fatal("Put without a key must fail")
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Fatalf("files written: %v", files)
	}
}

func TestSourcesDoNotShareEntries(t *testing.T) {
	store := Open(t.TempDir())
	annaKey, zlibKey := SearchKey(source.Anna, "q", source.SearchOptions{Page: 1}), SearchKey(source.ZLibrary, "q", source.SearchOptions{Page: 1})
	if err := store.Put(Entry{Key: annaKey, Query: "q", Page: 1, Body: []byte("anna")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(zlibKey); ok {
		t.Fatal("a zlibrary lookup must not see an anna entry")
	}
}

func TestSweep(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	for _, query := range []string{"old", "new"} {
		if err := store.Put(Entry{Key: SearchKey(source.Anna, query, source.SearchOptions{Page: 1}), Query: query, Page: 1}); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(dir, SearchKey(source.Anna, "old", source.SearchOptions{Page: 1})+".json")
	if err := os.Chtimes(old, time.Now().Add(-31*24*time.Hour), time.Now().Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Sweep(SweepAge); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old entry was not removed")
	}
	if _, ok := store.Get(SearchKey(source.Anna, "new", source.SearchOptions{Page: 1})); !ok {
		t.Fatal("recent entry was removed")
	}
	if err := Open(filepath.Join(dir, "missing")).Sweep(SweepAge); err != nil {
		t.Fatal(err)
	}
}

func TestPath(t *testing.T) {
	got, err := Path(t.TempDir())
	if err != nil || filepath.Base(got) != "cache" || filepath.Base(filepath.Dir(got)) != "booxd" {
		t.Fatalf("Path(temp) = %q, %v", got, err)
	}
	got, err = Path("")
	base, baseErr := os.UserCacheDir()
	if err != nil || baseErr != nil || got != filepath.Join(base, "booxd", "cache") {
		t.Fatalf("Path(empty) = %q, %v", got, err)
	}
}
