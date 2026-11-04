package down

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadJar parses a Netscape cookies.txt file into an HTTP cookie jar.
func LoadJar(path string) (http.CookieJar, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read cookie file %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := scanner.Text()
		httpOnly := false
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#HttpOnly_") {
			httpOnly = true
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			return nil, fmt.Errorf("cookie file %s line %d: expected 7 tab-separated fields", path, lineNumber)
		}
		if fields[1] != "TRUE" && fields[1] != "FALSE" {
			return nil, fmt.Errorf("cookie file %s line %d: invalid include-subdomains value", path, lineNumber)
		}
		if fields[3] != "TRUE" && fields[3] != "FALSE" {
			return nil, fmt.Errorf("cookie file %s line %d: invalid secure value", path, lineNumber)
		}
		expiry, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cookie file %s line %d: invalid expiry", path, lineNumber)
		}
		secure := fields[3] == "TRUE"
		domain := strings.TrimPrefix(fields[0], ".")
		scheme := "http"
		if secure {
			scheme = "https"
		}
		cookie := &http.Cookie{
			Name:     fields[5],
			Value:    fields[6],
			Path:     fields[2],
			Secure:   secure,
			HttpOnly: httpOnly,
		}
		if fields[1] == "TRUE" {
			cookie.Domain = domain
		}
		if expiry > 0 {
			cookie.Expires = time.Unix(expiry, 0)
		}
		jar.SetCookies(&url.URL{Scheme: scheme, Host: domain}, []*http.Cookie{cookie})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read cookie file %s: %w", path, err)
	}
	return jar, nil
}
