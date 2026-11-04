package down

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadJar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	contents := "# Netscape HTTP Cookie File\n\n.example.com\tTRUE\t/\tTRUE\t4102444800\tsid\tsecret\n#HttpOnly_localhost\tFALSE\t/private\tFALSE\t0\tlocal\tvalue\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	jar, err := LoadJar(path)
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]string{
		"https://www.example.com/":   "sid=secret",
		"http://www.example.com/":    "",
		"http://localhost/private/a": "local=value",
		"http://localhost/":          "",
	} {
		u, _ := url.Parse(raw)
		cookies := jar.Cookies(u)
		got := ""
		if len(cookies) > 0 {
			got = cookies[0].Name + "=" + cookies[0].Value
		}
		if got != want {
			t.Errorf("cookies for %s = %q, want %q", raw, got, want)
		}
	}
}

func TestLoadJarErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.txt")
	if err := os.WriteFile(path, []byte("# comment\na\tb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadJar(path); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %v, want line 2", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")
	if _, err := LoadJar(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("error = %v, want path %q", err, missing)
	}
}
