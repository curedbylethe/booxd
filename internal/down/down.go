package down

import (
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

	"github.com/curedbylethe/booxd/internal/fetch"
)

// ErrExists indicates the destination file already exists.
var ErrExists = errors.New("down: destination already exists")

// Request describes one download.
type Request struct {
	URL      string
	Dir      string
	Name     string
	Progress io.Writer
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
	var retried416 bool
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
		if have > 0 {
			_ = stream.Body.Close()
			header := make(http.Header)
			header.Set("Range", fmt.Sprintf("bytes=%d-", have))
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
		if _, statErr := os.Stat(final); statErr == nil {
			_ = stream.Body.Close()
			return final, ErrExists
		} else if !os.IsNotExist(statErr) {
			_ = stream.Body.Close()
			return "", statErr
		}
		file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			_ = stream.Body.Close()
			return "", err
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			_ = stream.Body.Close()
			return "", err
		}
		have = info.Size()
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
				_ = file.Close()
				if err := renamePart(part, final); err != nil {
					return "", err
				}
				progress := newProgress(req.Progress, total, have)
				progress.finish()
				return final, nil
			}
			if retried416 {
				_ = file.Close()
				return "", errors.New("range retry failed after 416 response")
			}
			if err := file.Truncate(0); err != nil {
				_ = file.Close()
				return "", err
			}
			_ = file.Close()
			retried416 = true
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
		total := int64(-1)
		if length >= 0 {
			total = length + have
		}
		progress := newProgress(req.Progress, total, have)
		written, copyErr := io.CopyBuffer(progressWriter{file: file, progress: progress}, stream.Body, make([]byte, 32*1024))
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
	if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusProxyAuthRequired {
		return fmt.Errorf("download refused: %s", http.StatusText(status))
	}
	return fmt.Errorf("download failed: status %d", status)
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
		reference, err := url.Parse(location)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid redirect URL: %w", err)
		}
		current = current.ResolveReference(reference)
		if _, err := httpURL(current.String()); err != nil {
			return nil, nil, fmt.Errorf("invalid redirect URL: %w", err)
		}
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
	if name := pathName(u); name != "" {
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
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
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

func renamePart(part, final string) error {
	if err := os.Rename(part, final); err != nil {
		return fmt.Errorf("rename partial download: %w", err)
	}
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
	total  int64
	done   int64
	last   int64
}

func newProgress(writer io.Writer, total, done int64) *progress {
	return &progress{writer: writer, total: total, done: done, last: -1}
}

func (p *progress) advance(n int64) {
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
	w.progress.advance(int64(n))
	return n, err
}
