// Package config owns the config file path, fields, defaults and environment
// overrides. It must not perform network I/O or build an HTTP client.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/irc"
)

// Config contains the runtime settings read by booxd.
type Config struct {
	Mirrors         []string
	DownloadDir     string
	CacheDir        string
	Proxy           *url.URL
	UserAgent       string
	RequestTimeout  time.Duration
	PerHostInterval time.Duration
	NoUpdateCheck   bool
	// ZLibraryBaseURL and ZLibrarySessionFile have no default; the command
	// that selects zlibrary validates them.
	ZLibraryBaseURL     string
	ZLibrarySessionFile string
	IRCServer           string
	IRCNick             string
	IRCChannel          string
	IRCSearchTimeout    time.Duration
	IRCDownloadTimeout  time.Duration
}

// Defaults returns compiled-in settings. Mirror seeds are supplied by the caller.
func Defaults() Config {
	c := Config{UserAgent: fetch.DefaultUserAgent, RequestTimeout: fetch.DefaultTimeout, PerHostInterval: fetch.DefaultInterval}
	ircDefault := irc.DefaultConfig()
	c.IRCServer = net.JoinHostPort(ircDefault.Host, strconv.Itoa(ircDefault.Port))
	c.IRCNick = ircDefault.Nick
	c.IRCChannel = ircDefault.Channel
	c.IRCSearchTimeout = 2 * time.Minute
	c.IRCDownloadTimeout = 30 * time.Minute
	if home, err := os.UserHomeDir(); err == nil {
		c.DownloadDir = filepath.Join(home, "Downloads")
	}
	if cache, err := os.UserCacheDir(); err == nil {
		c.CacheDir = cache
	}
	return c
}

// Path returns the platform config file path.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "booxd", "config.toml"), nil
}

type fileConfig struct {
	Mirrors         *[]string `toml:"mirrors"`
	DownloadDir     *string   `toml:"download_dir"`
	CacheDir        *string   `toml:"cache_dir"`
	Proxy           *string   `toml:"proxy"`
	UserAgent       *string   `toml:"user_agent"`
	RequestTimeout  any       `toml:"request_timeout"`
	PerHostInterval any       `toml:"per_host_interval"`
	NoUpdateCheck   *bool     `toml:"no_update_check"`
	ZLibrary        struct {
		BaseURL     *string `toml:"base_url"`
		SessionFile *string `toml:"session_file"`
	} `toml:"zlibrary"`
	IRC struct {
		Server          *string `toml:"server"`
		Nick            *string `toml:"nick"`
		Channel         *string `toml:"channel"`
		SearchTimeout   any     `toml:"search_timeout"`
		DownloadTimeout any     `toml:"download_timeout"`
	} `toml:"irc"`
}

// Load merges base settings, an optional TOML file, then BOOXD_* variables.
func Load(path string, explicit bool, base Config) (Config, error) {
	c := base
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && !explicit {
				return applyEnvironment(c)
			}
			var pathErr *fs.PathError
			if errors.As(err, &pathErr) {
				err = pathErr.Err
			}
			return c, fmt.Errorf("%s: %s", path, oneLine(err.Error()))
		}
		var file fileConfig
		md, err := toml.Decode(string(data), &file)
		if err != nil {
			return c, fmt.Errorf("%s: %s", path, oneLine(err.Error()))
		}
		if keys := md.Undecoded(); len(keys) > 0 {
			return c, fmt.Errorf("%s: unknown key %q", path, keys[0])
		}
		var errField error
		c, errField = applyFile(c, file)
		if errField != nil {
			return c, fmt.Errorf("%s: %s", path, oneLine(errField.Error()))
		}
	}
	return applyEnvironment(c)
}

func applyFile(c Config, f fileConfig) (Config, error) {
	if f.Mirrors != nil && len(*f.Mirrors) > 0 {
		v, err := validateMirrors(*f.Mirrors, "mirrors")
		if err != nil {
			return c, err
		}
		c.Mirrors = v
	}
	if f.DownloadDir != nil {
		if *f.DownloadDir == "" {
			return c, fmt.Errorf("download_dir must not be empty")
		}
		c.DownloadDir = *f.DownloadDir
	}
	if f.CacheDir != nil {
		if *f.CacheDir == "" {
			return c, fmt.Errorf("cache_dir must not be empty")
		}
		c.CacheDir = *f.CacheDir
	}
	if f.Proxy != nil {
		v, err := parseProxy(*f.Proxy, "proxy")
		if err != nil {
			return c, err
		}
		c.Proxy = v
	}
	if f.UserAgent != nil {
		if *f.UserAgent == "" {
			return c, fmt.Errorf("user_agent must not be empty")
		}
		c.UserAgent = *f.UserAgent
	}
	if f.RequestTimeout != nil {
		v, err := parseDuration(f.RequestTimeout, "request_timeout")
		if err != nil {
			return c, err
		}
		c.RequestTimeout = v
	}
	if f.PerHostInterval != nil {
		v, err := parseDuration(f.PerHostInterval, "per_host_interval")
		if err != nil {
			return c, err
		}
		c.PerHostInterval = v
	}
	if f.NoUpdateCheck != nil {
		c.NoUpdateCheck = *f.NoUpdateCheck
	}
	if f.ZLibrary.BaseURL != nil {
		c.ZLibraryBaseURL = *f.ZLibrary.BaseURL
	}
	if f.ZLibrary.SessionFile != nil {
		c.ZLibrarySessionFile = *f.ZLibrary.SessionFile
	}
	if f.IRC.Server != nil {
		v, err := parseIRCServer(*f.IRC.Server, "irc server")
		if err != nil {
			return c, err
		}
		c.IRCServer = v
	}
	if f.IRC.Nick != nil {
		if *f.IRC.Nick == "" {
			return c, fmt.Errorf("irc nick must not be empty")
		}
		c.IRCNick = *f.IRC.Nick
	}
	if f.IRC.Channel != nil {
		if !strings.HasPrefix(*f.IRC.Channel, "#") {
			return c, fmt.Errorf("irc channel must start with %q", "#")
		}
		c.IRCChannel = *f.IRC.Channel
	}
	if f.IRC.SearchTimeout != nil {
		v, err := parseDuration(f.IRC.SearchTimeout, "irc search_timeout")
		if err != nil {
			return c, err
		}
		c.IRCSearchTimeout = v
	}
	if f.IRC.DownloadTimeout != nil {
		v, err := parseDuration(f.IRC.DownloadTimeout, "irc download_timeout")
		if err != nil {
			return c, err
		}
		c.IRCDownloadTimeout = v
	}
	return c, nil
}

func applyEnvironment(c Config) (Config, error) {
	if value, ok := os.LookupEnv("BOOXD_MIRRORS"); ok && strings.TrimSpace(value) != "" {
		parts := strings.Split(value, ",")
		values := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				values = append(values, part)
			}
		}
		v, err := validateMirrors(values, "BOOXD_MIRRORS")
		if err != nil {
			return c, err
		}
		c.Mirrors = v
	}
	for _, item := range []struct {
		name   string
		target *string
	}{
		{"BOOXD_DOWNLOAD_DIR", &c.DownloadDir}, {"BOOXD_CACHE_DIR", &c.CacheDir}, {"BOOXD_USER_AGENT", &c.UserAgent},
		{"BOOXD_ZLIBRARY_BASE_URL", &c.ZLibraryBaseURL}, {"BOOXD_ZLIBRARY_SESSION_FILE", &c.ZLibrarySessionFile},
		{"BOOXD_IRC_NICK", &c.IRCNick},
	} {
		if value, ok := os.LookupEnv(item.name); ok && value != "" {
			*item.target = value
		}
	}
	if value, ok := os.LookupEnv("BOOXD_IRC_SERVER"); ok && value != "" {
		v, err := parseIRCServer(value, "BOOXD_IRC_SERVER")
		if err != nil {
			return c, err
		}
		c.IRCServer = v
	}
	if value, ok := os.LookupEnv("BOOXD_IRC_CHANNEL"); ok && value != "" {
		if !strings.HasPrefix(value, "#") {
			return c, fmt.Errorf("BOOXD_IRC_CHANNEL must start with %q", "#")
		}
		c.IRCChannel = value
	}
	if value, ok := os.LookupEnv("BOOXD_PROXY"); ok && value != "" {
		v, err := parseProxy(value, "BOOXD_PROXY")
		if err != nil {
			return c, err
		}
		c.Proxy = v
	}
	for _, item := range []struct {
		name   string
		target *time.Duration
	}{
		{"BOOXD_REQUEST_TIMEOUT", &c.RequestTimeout}, {"BOOXD_PER_HOST_INTERVAL", &c.PerHostInterval},
		{"BOOXD_IRC_SEARCH_TIMEOUT", &c.IRCSearchTimeout}, {"BOOXD_IRC_DOWNLOAD_TIMEOUT", &c.IRCDownloadTimeout},
	} {
		if value, ok := os.LookupEnv(item.name); ok && value != "" {
			v, err := parseDuration(value, item.name)
			if err != nil {
				return c, err
			}
			*item.target = v
		}
	}
	if value, ok := os.LookupEnv("BOOXD_NO_UPDATE_CHECK"); ok && value != "" {
		v, err := strconv.ParseBool(value)
		if err != nil {
			return c, fmt.Errorf("BOOXD_NO_UPDATE_CHECK: %w", err)
		}
		c.NoUpdateCheck = v
	}
	return c, nil
}

func validateMirrors(values []string, field string) ([]string, error) {
	result := make([]string, 0, len(values))
	for i, raw := range values {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, fmt.Errorf("%s entry %d is not an absolute http or https URL", field, i+1)
		}
		result = append(result, strings.TrimRight(u.String(), "/"))
	}
	return result, nil
}

func parseIRCServer(raw, field string) (string, error) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" {
		return "", fmt.Errorf("%s must be host:port", field)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s must be host:port", field)
	}
	return raw, nil
}

func parseProxy(raw, field string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
		return nil, fmt.Errorf("%s is not a valid http, https or socks5 URL", field)
	}
	return u, nil
}

func parseDuration(raw any, field string) (time.Duration, error) {
	s, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("%s must be a duration string", field)
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", field)
	}
	return d, nil
}

func oneLine(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ") }
