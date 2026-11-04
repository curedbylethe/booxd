package mirror

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadState(t *testing.T) {
	for _, content := range []string{"missing", "{", "null", "{}", `{"base_url":"partial","checked_at":"bad"}`} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mirror.json")
			if content != "missing" {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := loadState(path)
			if got.BaseURL != "" || !got.CheckedAt.IsZero() || got.DeadUntil == nil || len(got.DeadUntil) != 0 {
				t.Fatalf("state = %#v, want empty state and non-nil map", got)
			}
		})
	}
	if got := loadState(t.TempDir()); got.BaseURL != "" || got.DeadUntil == nil {
		t.Fatalf("unreadable cache state = %#v", got)
	}
}

func TestSaveState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "cache")
	path := filepath.Join(dir, "mirror.json")
	now := time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC)
	want := state{
		BaseURL: "https://mirror.test", CheckedAt: now,
		DeadUntil: map[string]time.Time{"https://dead.test": now.Add(time.Hour)},
	}
	for range 2 {
		if err := saveState(path, want); err != nil {
			t.Fatal(err)
		}
		if got := loadState(path); !reflect.DeepEqual(got, want) {
			t.Fatalf("state = %#v, want %#v", got, want)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "mirror.json" {
		t.Fatalf("directory entries = %v, error = %v", entries, err)
	}
}

func TestSaveStateFailure(t *testing.T) {
	for _, name := range []string{"parent file", "encoding", "rename"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mirror.json")
			s := state{}
			switch name {
			case "parent file":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "mirror.json")
			case "encoding":
				s.CheckedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			case "rename":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := saveState(path, s); err == nil {
				t.Fatal("saveState succeeded, want error")
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, ".mirror-*.tmp"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("temporary files = %v, error = %v", leftovers, err)
			}
		})
	}
}

func TestStatePath(t *testing.T) {
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := StatePath()
	if err != nil || got != filepath.Join(dir, "booxd", "mirror.json") {
		t.Fatalf("StatePath = %q, %v", got, err)
	}
}
