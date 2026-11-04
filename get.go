package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/curedbylethe/booxd/internal/down"
	"github.com/curedbylethe/booxd/internal/parse"
)

func runGet(args []string, stdout, stderr io.Writer, resolve resolveFunc, newClient clientFunc) int {
	flags := flag.NewFlagSet("get", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: booxd get [flags] <id>\nflags must precede the id")
		flags.PrintDefaults()
	}
	configPath := flags.String("config", "", "config file path")
	cookiesPath := flags.String("cookies", "", "Netscape cookie file; required for a download")
	override := flags.String("mirror", "", "base URL; skip mirror discovery")
	verbose := flags.Bool("verbose", false, "log each fetch attempt to stderr")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 1 {
		flags.Usage()
		return 2
	}
	bookID := flags.Arg(0)
	if !bookIDPattern.MatchString(bookID) {
		_, _ = fmt.Fprintf(stderr, "booxd: invalid book id %q\n", bookID)
		return 2
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	var jar http.CookieJar
	if *cookiesPath != "" {
		jar, err = down.LoadJar(*cookiesPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
	}
	var log io.Writer
	if *verbose {
		log = stderr
	}
	pageTimeout := cfg.RequestTimeout
	clientConfig := cfg
	clientConfig.RequestTimeout = 0
	client := newClient(clientConfig, log, jar)
	ctx := context.Background()
	base, err := resolve(ctx, cfg, *override, client)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	base = strings.TrimRight(base, "/")
	_, _ = fmt.Fprintf(stderr, "mirror: %s\n", base)
	bookURL := base + "/books/" + bookID
	if *cookiesPath == "" {
		_, _ = fmt.Fprintf(stderr, "booxd: downloads need a signed-in session; pass --cookies cookies.txt: %s\n", bookURL)
		return 2
	}

	pageCtx, cancel := context.WithTimeout(ctx, pageTimeout)
	response, err := client.Get(pageCtx, bookURL)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	if response.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "booxd: get: status %d\n", response.StatusCode)
		return 1
	}
	detail, err := parse.DetailPage(bytes.NewReader(response.Body))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}

	var option *parse.Download
	for i := range detail.Downloads {
		if !detail.Downloads[i].RequiresSession && detail.Downloads[i].URL != "" {
			option = &detail.Downloads[i]
			break
		}
	}
	if option == nil {
		if !jarHasCookiesFor(jar, bookURL) {
			book, _ := url.Parse(bookURL)
			_, _ = fmt.Fprintf(stderr, "booxd: downloads need a signed-in session; cookie file has no cookies for mirror host %s. Use --mirror URL to choose a mirror matching the cookie domain: %s\n", book.Host, bookURL)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "booxd: downloads need a signed-in session: %s\n", bookURL)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "option: %s\n", option.Label)
	downloadURL, err := resolveDownloadURL(base, option.URL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}

	path, err := down.Download(ctx, client, down.Request{
		URL:      downloadURL,
		Dir:      cfg.DownloadDir,
		Name:     fallbackName(bookID, detail.Format),
		Progress: stderr,
	})
	if errors.Is(err, down.ErrExists) {
		_, _ = fmt.Fprintf(stdout, "already downloaded: %s\n", path)
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "saved %s\n", path)
	return 0
}

func jarHasCookiesFor(jar http.CookieJar, rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && len(jar.Cookies(u)) > 0
}

func resolveDownloadURL(base, href string) (string, error) {
	baseURL, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil {
		return "", fmt.Errorf("invalid mirror URL: %w", err)
	}
	reference, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}
	resolved := baseURL.ResolveReference(reference)
	if !resolved.IsAbs() || resolved.Host == "" || (resolved.Scheme != "http" && resolved.Scheme != "https") {
		return "", errors.New("download URL must resolve to an absolute http or https URL")
	}
	return resolved.String(), nil
}

func fallbackName(id, format string) string {
	if format == "" {
		return id
	}
	return id + "." + strings.ToLower(format)
}
