package down

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/curedbylethe/booxd/internal/fetch"
)

// ErrExists indicates the destination file already exists.
var ErrExists = errors.New("down: destination already exists")

// ErrInsecureRedirect indicates a redirect from https to plain http.
var ErrInsecureRedirect = errors.New("down: redirect leaves https")

// ErrNotBook indicates the server sent a web page or an empty body instead of a file.
var ErrNotBook = errors.New("down: server sent a web page or an empty body, not a book")

// StatusError reports a transfer response outside 2xx and 3xx. Its message names no URL.
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	if e.Code == http.StatusUnauthorized || e.Code == http.StatusForbidden || e.Code == http.StatusProxyAuthRequired {
		return fmt.Sprintf("download refused: %s", http.StatusText(e.Code))
	}
	return fmt.Sprintf("download failed: status %d", e.Code)
}

// Request describes one download.
type Request struct {
	URL      string
	Dir      string
	Name     string
	Progress io.Writer
	// Bytes reports done against total (−1 when the size is unknown) after
	// every write and once at the end, whether or not Progress lines are
	// being written.
	Bytes func(done, total int64)
}

const maxRedirects = 3

// Download streams a file through client, keeping partial data for resumption.
func Download(ctx context.Context, client *fetch.Client, req Request) (string, error) {
	initialURL, err := httpURL(req.URL)
	if err != nil {
		return "", err
	}
	if req.Dir == "" {
		return "", errors.New("download directory is empty")
	}
	if err := os.MkdirAll(req.Dir, 0o755); err != nil {
		return "", err
	}
	requestURL := initialURL
	for {
		// A discovery request resolves Content-Disposition before choosing the
		// partial-file path, so this request cannot carry Range.
		stream, finalURL, err := openRedirected(ctx, client, requestURL, nil)
		if err != nil {
			return "", err
		}
		if stream.StatusCode != http.StatusOK {
			_ = stream.Body.Close()
			return "", responseError(stream.StatusCode)
		}
		name, err := responseName(stream.Header, finalURL, req.Name)
		if err != nil {
			_ = stream.Body.Close()
			return "", err
		}
		final := filepath.Join(req.Dir, name)
		part := final + ".part"
		if _, statErr := os.Stat(final); statErr == nil {
			_ = stream.Body.Close()
			return final, ErrExists
		} else if !os.IsNotExist(statErr) {
			_ = stream.Body.Close()
			return "", statErr
		}
		have, err := partSize(part)
		if err != nil {
			_ = stream.Body.Close()
			return "", err
		}
		meta := part + ".meta"
		validator := validatorOf(stream.Header)
		// A partial resumes only against the validator and total it was written
		// with; otherwise the discovery response restarts the file from zero.
		if have > 0 && !resumable(meta, validator, stream.Header.Get("Content-Length")) {
			have = 0
		}
		if have > 0 {
			_ = stream.Body.Close()
			header := make(http.Header)
			header.Set("Range", fmt.Sprintf("bytes=%d-", have))
			header.Set("If-Range", validator)
			stream, finalURL, err = openRedirected(ctx, client, finalURL, header)
			if err != nil {
				return "", err
			}
			if stream.StatusCode == http.StatusUnauthorized || stream.StatusCode == http.StatusForbidden || stream.StatusCode == http.StatusProxyAuthRequired {
				_ = stream.Body.Close()
				return "", responseError(stream.StatusCode)
			}
			resumedName, nameErr := responseName(stream.Header, finalURL, req.Name)
			if nameErr != nil {
				_ = stream.Body.Close()
				return "", nameErr
			}
			if resumedName != name {
				_ = stream.Body.Close()
				return "", errors.New("download filename changed while resuming")
			}
		}
		if stream.StatusCode != http.StatusOK && stream.StatusCode != http.StatusPartialContent && stream.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			_ = stream.Body.Close()
			return "", responseError(stream.StatusCode)
		}
		var body io.Reader = stream.Body
		if stream.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			buffered := bufio.NewReader(stream.Body)
			if isHTML(stream.Header.Get("Content-Type"), buffered, stream.StatusCode == http.StatusOK) {
				_ = stream.Body.Close()
				removePart(part)
				return "", ErrNotBook
			}
			body = buffered
		}
		file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			_ = stream.Body.Close()
			return "", err
		}
		var rangeTotal int64
		var rangeTotalKnown bool
		if stream.StatusCode == http.StatusPartialContent {
			start, total, known, rangeErr := parseContentRange(stream.Header.Get("Content-Range"))
			if rangeErr != nil || start != have {
				_ = file.Truncate(0)
				_ = file.Close()
				_ = stream.Body.Close()
				if rangeErr != nil {
					return "", rangeErr
				}
				return "", fmt.Errorf("invalid Content-Range start %d; expected %d", start, have)
			}
			rangeTotal, rangeTotalKnown = total, known
		}
		switch stream.StatusCode {
		case http.StatusPartialContent:
			if _, err := file.Seek(have, io.SeekStart); err != nil {
				_ = file.Close()
				_ = stream.Body.Close()
				return "", err
			}
		case http.StatusOK:
			if err := file.Truncate(0); err != nil {
				_ = file.Close()
				_ = stream.Body.Close()
				return "", err
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				_ = file.Close()
				_ = stream.Body.Close()
				return "", err
			}
			have = 0
		case http.StatusRequestedRangeNotSatisfiable:
			total, ok := unsatisfiedLength(stream.Header.Get("Content-Range"))
			_ = stream.Body.Close()
			if ok && have == total {
				if err := file.Sync(); err != nil {
					_ = file.Close()
					return "", err
				}
				if err := file.Close(); err != nil {
					return "", err
				}
				if err := renamePart(part, final); err != nil {
					return "", err
				}
				progress := newProgress(req.Progress, req.Bytes, total, have)
				progress.finish()
				return final, nil
			}
			if err := file.Truncate(0); err != nil {
				_ = file.Close()
				return "", err
			}
			_ = file.Close()
			requestURL = finalURL
			continue
		}

		expected := stream.Header.Get("Content-Length")
		length := int64(-1)
		if expected != "" {
			length, err = strconv.ParseInt(expected, 10, 64)
			if err != nil || length < 0 {
				_ = file.Close()
				_ = stream.Body.Close()
				return "", fmt.Errorf("invalid Content-Length %q", expected)
			}
		}
		if stream.StatusCode == http.StatusOK {
			if err := os.WriteFile(meta, []byte(validatorOf(stream.Header)+"\n"+expected+"\n"), 0o644); err != nil {
				_ = file.Close()
				_ = stream.Body.Close()
				return "", err
			}
		}
		total := int64(-1)
		if length >= 0 {
			total = length + have
			if stream.StatusCode == http.StatusPartialContent && rangeTotalKnown {
				total = rangeTotal
			}
		}
		progress := newProgress(req.Progress, req.Bytes, total, have)
		written, copyErr := io.CopyBuffer(progressWriter{file: file, progress: progress}, body, make([]byte, 32*1024))
		_ = stream.Body.Close()
		if copyErr != nil {
			truncated := length >= 0 && written < length && errors.Is(copyErr, io.ErrUnexpectedEOF)
			if !truncated {
				_ = file.Close()
				return "", copyErr
			}
		}
		if length >= 0 && written != length {
			_ = file.Close()
			if written < length {
				return "", fmt.Errorf("truncated download: wrote %d of %d bytes; partial kept at %s", written, length, part)
			}
			return "", fmt.Errorf("download size mismatch: wrote %d bytes; expected %d", written, length)
		}
		if written == 0 {
			_ = file.Close()
			removePart(part)
			return "", ErrNotBook
		}
		if stream.StatusCode == http.StatusPartialContent && rangeTotalKnown && have+written != rangeTotal {
			_ = file.Truncate(0)
			_ = file.Close()
			return "", fmt.Errorf("download size mismatch: expected %d bytes total, got %d; partial cleared at %s", rangeTotal, have+written, part)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		if err := renamePart(part, final); err != nil {
			return "", err
		}
		progress.finish()
		return final, nil
	}
}

func responseError(status int) error {
	return &StatusError{Code: status}
}

// isHTML reports a web page by media type, or by sniffing when body starts the file.
func isHTML(contentType string, body *bufio.Reader, sniff bool) bool {
	if media, _, err := mime.ParseMediaType(contentType); err == nil && (media == "text/html" || media == "application/xhtml+xml") {
		return true
	}
	if !sniff {
		return false
	}
	// A short or failed read leaves its error in body for the copy to report.
	head, _ := body.Peek(512)
	return strings.HasPrefix(http.DetectContentType(head), "text/html")
}

// validatorOf returns the strong ETag, else Last-Modified, for If-Range.
func validatorOf(header http.Header) string {
	if etag := header.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		return etag
	}
	return header.Get("Last-Modified")
}

// resumable reports whether the sidecar at meta matches validator and the
// Content-Length length of the current full response.
func resumable(meta, validator, length string) bool {
	data, err := os.ReadFile(meta)
	if err != nil {
		return false
	}
	stored, total, _ := strings.Cut(strings.TrimSuffix(string(data), "\n"), "\n")
	return stored != "" && stored == validator && (total == "" || length == "" || total == length)
}

func removePart(part string) {
	_ = os.Remove(part)
	_ = os.Remove(part + ".meta")
}

func openRedirected(ctx context.Context, client *fetch.Client, current *url.URL, header http.Header) (*fetch.Stream, *url.URL, error) {
	for redirects := 0; ; redirects++ {
		stream, err := client.Open(ctx, current.String(), header)
		if err != nil {
			return nil, nil, err
		}
		if stream.StatusCode < 300 || stream.StatusCode > 399 {
			return stream, current, nil
		}
		location := stream.Header.Get("Location")
		_ = stream.Body.Close()
		if location == "" {
			return nil, nil, errors.New("redirect has no Location")
		}
		if redirects == maxRedirects {
			return nil, nil, fmt.Errorf("download exceeded %d redirects", maxRedirects)
		}
		// Redirect errors name only scheme or host: the Location may carry a signed query.
		reference, err := url.Parse(location)
		if err != nil {
			return nil, nil, errors.New("invalid redirect URL")
		}
		next := current.ResolveReference(reference)
		if next.Scheme != "http" && next.Scheme != "https" {
			return nil, nil, fmt.Errorf("redirect to unsupported scheme %q", next.Scheme)
		}
		if next.Host == "" {
			return nil, nil, errors.New("invalid redirect URL")
		}
		if current.Scheme == "https" && next.Scheme != "https" {
			return nil, nil, fmt.Errorf("%w: %s", ErrInsecureRedirect, next.Host)
		}
		current = next
	}
}

func httpURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("download URL must be absolute http or https: %s", raw)
	}
	return u, nil
}

func responseName(header http.Header, u *url.URL, fallback string) (string, error) {
	if disposition := header.Get("Content-Disposition"); disposition != "" {
		_, params, err := mime.ParseMediaType(disposition)
		if err == nil && params["filename"] != "" {
			return safeName(params["filename"])
		}
	}
	if name := pathName(u); filepath.Ext(name) != "" {
		return safeName(name)
	}
	return safeName(fallback)
}

func pathName(u *url.URL) string {
	segment := filepath.Base(strings.TrimRight(u.EscapedPath(), "/"))
	if segment == "." || segment == string(filepath.Separator) {
		return ""
	}
	name, err := url.PathUnescape(segment)
	if err != nil {
		return ""
	}
	return name
}

func safeName(candidate string) (string, error) {
	name := filepath.Base(candidate)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("download filename is empty or invalid")
	}
	return name, nil
}

func unsatisfiedLength(value string) (int64, bool) {
	unit, length, ok := strings.Cut(value, " */")
	if !ok || unit != "bytes" {
		return 0, false
	}
	total, err := strconv.ParseInt(length, 10, 64)
	return total, err == nil && total >= 0
}

func parseContentRange(value string) (start, total int64, totalKnown bool, err error) {
	unit, rangeValue, ok := strings.Cut(value, " ")
	if !ok || unit != "bytes" {
		return 0, 0, false, fmt.Errorf("invalid Content-Range %q", value)
	}
	byteRange, totalValue, ok := strings.Cut(rangeValue, "/")
	if !ok {
		return 0, 0, false, fmt.Errorf("invalid Content-Range %q", value)
	}
	startValue, endValue, ok := strings.Cut(byteRange, "-")
	if !ok {
		return 0, 0, false, fmt.Errorf("invalid Content-Range %q", value)
	}
	start, err = strconv.ParseInt(startValue, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false, fmt.Errorf("invalid Content-Range %q", value)
	}
	end, err := strconv.ParseInt(endValue, 10, 64)
	if err != nil || end < start {
		return 0, 0, false, fmt.Errorf("invalid Content-Range %q", value)
	}
	if totalValue == "*" {
		return start, 0, false, nil
	}
	total, err = strconv.ParseInt(totalValue, 10, 64)
	if err != nil || total <= end {
		return 0, 0, false, fmt.Errorf("invalid Content-Range %q", value)
	}
	return start, total, true, nil
}

func renamePart(part, final string) error {
	if err := os.Rename(part, final); err != nil {
		return fmt.Errorf("rename partial download: %w", err)
	}
	_ = os.Remove(part + ".meta")
	return nil
}

func partSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		return 0, fmt.Errorf("partial download path is a directory: %s", path)
	}
	return info.Size(), nil
}

type progress struct {
	writer io.Writer
	bytes  func(done, total int64)
	total  int64
	done   int64
	last   int64
}

func newProgress(writer io.Writer, bytes func(done, total int64), total, done int64) *progress {
	return &progress{writer: writer, bytes: bytes, total: total, done: done, last: -1}
}

func (p *progress) advance() {
	if p.bytes != nil {
		p.bytes(p.done, p.total)
	}
	if p.writer == nil || p.total <= 0 {
		return
	}
	percent := p.done * 100 / p.total
	if percent > 100 {
		percent = 100
	}
	if percent > p.last {
		_, _ = fmt.Fprintf(p.writer, "%d%%\n", percent)
		p.last = percent
	}
}

func (p *progress) finish() {
	if p.bytes != nil {
		p.bytes(p.done, p.total)
	}
	if p.writer != nil && p.total > 0 && p.last < 100 {
		_, _ = io.WriteString(p.writer, "100%\n")
		p.last = 100
	}
}

type progressWriter struct {
	file     io.Writer
	progress *progress
}

func (w progressWriter) Write(data []byte) (int, error) {
	n, err := w.file.Write(data)
	w.progress.done += int64(n)
	w.progress.advance()
	return n, err
}
