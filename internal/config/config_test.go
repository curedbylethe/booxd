package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var envNames = []string{
	"BOOXD_MIRRORS", "BOOXD_DOWNLOAD_DIR", "BOOXD_CACHE_DIR", "BOOXD_PROXY",
	"BOOXD_USER_AGENT", "BOOXD_REQUEST_TIMEOUT", "BOOXD_PER_HOST_INTERVAL",
}

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, name := range envNames {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaults(t *testing.T) {
	cleanEnv(t)
	c := Defaults()
	if c.UserAgent != "booxd/0.1" || c.RequestTimeout != 30*time.Second || c.PerHostInterval != time.Second {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.DownloadDir == "" || c.CacheDir == "" || c.Mirrors != nil || c.Proxy != nil {
		t.Fatalf("unexpected path or mirror defaults: %+v", c)
	}
}

func TestPath(t *testing.T) {
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "booxd", "config.toml"); path != want {
		t.Fatalf("Path() = %q, want %q", path, want)
	}
}

func TestLoadPrecedenceAndFields(t *testing.T) {
	cleanEnv(t)
	base := Config{Mirrors: []string{"https://default.test"}, DownloadDir: "/downloads", CacheDir: "/cache", UserAgent: "default/1", RequestTimeout: time.Minute, PerHostInterval: 2 * time.Second}
	path := writeConfig(t, "mirrors = [\"https://file.test/\"]\ndownload_dir = \"/file-downloads\"\ncache_dir = \"/file-cache\"\nproxy = \"http://user:secret@proxy.invalid:3128\"\nuser_agent = \"file/1\"\nrequest_timeout = \"30s\"\nper_host_interval = \"3s\"\n")
	t.Setenv("BOOXD_USER_AGENT", "env/2")
	t.Setenv("BOOXD_PER_HOST_INTERVAL", "4s")
	c, err := Load(path, false, base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Mirrors, ",") != "https://file.test" || c.DownloadDir != "/file-downloads" || c.CacheDir != "/file-cache" || c.UserAgent != "env/2" || c.RequestTimeout != 30*time.Second || c.PerHostInterval != 4*time.Second {
		t.Fatalf("unexpected merged config: %+v", c)
	}
	if c.Proxy == nil || c.Proxy.Host != "proxy.invalid:3128" {
		t.Fatalf("proxy not decoded: %v", c.Proxy)
	}
	password, _ := c.Proxy.User.Password()
	if password != "secret" {
		t.Fatalf("proxy credentials not decoded")
	}
}

func TestLoadMissingFile(t *testing.T) {
	cleanEnv(t)
	path := filepath.Join(t.TempDir(), "missing.toml")
	base := Defaults()
	if _, err := Load(path, false, base); err != nil {
		t.Fatalf("optional missing file: %v", err)
	}
	_, err := Load(path, true, base)
	if err == nil || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("explicit missing error = %v", err)
	}
}

func TestLoadEnvironmentMirrors(t *testing.T) {
	cleanEnv(t)
	t.Setenv("BOOXD_MIRRORS", " https://one.test/, ,https://two.test ")
	t.Setenv("BOOXD_USER_AGENT", "")
	c, err := Load("", false, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Mirrors, ",") != "https://one.test,https://two.test" || c.UserAgent != "booxd/0.1" {
		t.Fatalf("unexpected env values: %+v", c)
	}
	t.Setenv("BOOXD_MIRRORS", "")
	c, err = Load("", false, Config{Mirrors: []string{"https://base.test"}, DownloadDir: "/d", CacheDir: "/c", UserAgent: "ua", RequestTimeout: time.Second, PerHostInterval: time.Second})
	if err != nil || len(c.Mirrors) != 1 {
		t.Fatalf("empty env should be unset: %+v, %v", c, err)
	}
}

func TestLoadEmptyMirrorList(t *testing.T) {
	cleanEnv(t)
	c, err := Load(writeConfig(t, "mirrors = []\n"), false, Config{Mirrors: []string{"https://base.test"}, DownloadDir: "/d", CacheDir: "/c", UserAgent: "ua", RequestTimeout: time.Second, PerHostInterval: time.Second})
	if err != nil || c.Mirrors == nil || len(c.Mirrors) != 0 {
		t.Fatalf("empty list = %#v, %v", c.Mirrors, err)
	}
}

func TestLoadErrors(t *testing.T) {
	cleanEnv(t)
	base := Config{DownloadDir: "/d", CacheDir: "/c", UserAgent: "ua", RequestTimeout: time.Second, PerHostInterval: time.Second}
	for _, tc := range []struct{ name, body, field string }{
		{"syntax", "request_timeout =\n", "request_timeout"},
		{"unknown", "per_host_intervall = \"1s\"\n", "per_host_intervall"},
		{"mirrors", "mirrors = [\"file:///tmp\"]\n", "mirrors"},
		{"download_dir", "download_dir = \"\"\n", "download_dir"},
		{"cache_dir", "cache_dir = \"\"\n", "cache_dir"},
		{"proxy", "proxy = \"ftp://proxy.invalid\"\n", "proxy"},
		{"user_agent", "user_agent = \"\"\n", "user_agent"},
		{"request_timeout", "request_timeout = \"0s\"\n", "request_timeout"},
		{"interval", "per_host_interval = 0\n", "per_host_interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.body)
			_, err := Load(path, false, base)
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.field) || strings.Contains(err.Error(), "\n") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestEnvironmentErrorsNameField(t *testing.T) {
	cleanEnv(t)
	t.Setenv("BOOXD_MIRRORS", "https://ok.test,not a url")
	_, err := Load("", false, Defaults())
	if err == nil || !strings.Contains(err.Error(), "BOOXD_MIRRORS") || !strings.Contains(err.Error(), "entry 2") {
		t.Fatalf("error = %v", err)
	}
	cleanEnv(t)
	t.Setenv("BOOXD_PROXY", "socks4://proxy.invalid")
	_, err = Load("", false, Defaults())
	if err == nil || !strings.Contains(err.Error(), "BOOXD_PROXY") || strings.Contains(err.Error(), "proxy.invalid") {
		t.Fatalf("proxy error leaked URL or missed field: %v", err)
	}
}

func TestProxyValue(t *testing.T) {
	u, err := url.Parse("https://proxy.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseProxy(u.String())
	if err != nil || v.String() != u.String() {
		t.Fatalf("proxy = %v, %v", v, err)
	}
}
