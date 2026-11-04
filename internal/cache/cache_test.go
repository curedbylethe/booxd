package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeys(t *testing.T) {
	if Key(" Crime   Punishment ", 1) != Key("crime punishment", 1) {
		t.Fatal("normalised queries must share a key")
	}
	if Key("crime punishment", 1) == Key("crime punishment", 2) || Key("crime punishment", 1) == Key("other", 1) {
		t.Fatal("distinct pages and queries need distinct keys")
	}
}

func TestStorePutGetAndRejectBadEntries(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	want := Entry{Query: "Crime  Punishment", Page: 1, Status: 200, FetchedAt: time.Now(), Body: []byte("body")}
	if err := store.Put(want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Key(want.Query, want.Page)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil || decoded["fetched_at"] == nil {
		t.Fatalf("fetched_at missing or invalid JSON: %v", err)
	}
	got, ok := store.Get("crime punishment", 1)
	if !ok || string(got.Body) != "body" {
		t.Fatalf("Get() = %#v, %v", got, ok)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(files) != 0 {
		t.Fatalf("temporary files remain: %v", files)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(want.Query, want.Page); ok {
		t.Fatal("corrupt entry must miss")
	}
	if err := store.Put(want); err != nil {
		t.Fatal(err)
	}
	wrong := want
	wrong.Query = "other"
	data, _ = json.Marshal(wrong)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(want.Query, want.Page); ok {
		t.Fatal("mismatched entry must miss")
	}
}

func TestSweep(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	for _, query := range []string{"old", "new"} {
		if err := store.Put(Entry{Query: query, Page: 1}); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(dir, Key("old", 1)+".json")
	if err := os.Chtimes(old, time.Now().Add(-31*24*time.Hour), time.Now().Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Sweep(SweepAge); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old entry was not removed")
	}
	if _, ok := store.Get("new", 1); !ok {
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
