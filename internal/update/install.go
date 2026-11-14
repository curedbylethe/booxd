package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
)

// releaseDownloadBase is a variable so tests can point it at a local server.
var releaseDownloadBase = "https://github.com/curedbylethe/booxd/releases/download"

// findExecutable is a variable so tests can target a temporary file.
var findExecutable = os.Executable

// manualUpdatePlatform marks a platform whose running image cannot be replaced.
// It is a variable so tests exercise the Windows path on any platform.
var manualUpdatePlatform = runtime.GOOS == "windows"

// restartProcess is a variable so tests never replace the process image.
// Stage 03 calls it; nothing in this stage restarts.
var restartProcess = Restart //nolint:unused // consumed by the startup hook

// errManualUpdate reports that this platform upgrades through its installer.
var errManualUpdate = errors.New("self-update is not supported on this platform")

const (
	// downloadTimeout bounds the checksum and archive downloads.
	downloadTimeout = 2 * time.Minute
	// maxAssetSize caps one downloaded file or archive member.
	maxAssetSize = 64 << 20
	// maxRedirects is how many hops one file may follow.
	maxRedirects = 3
	// binaryName is the archive member on Unix, windowsName on Windows.
	binaryName  = "booxd"
	windowsName = "booxd.exe"
)

// releaseAssets maps the released targets to their archive extension. A pair
// outside this table has no asset.
var releaseAssets = map[[2]string]string{
	{"darwin", "amd64"}:  ".tar.gz",
	{"darwin", "arm64"}:  ".tar.gz",
	{"linux", "amd64"}:   ".tar.gz",
	{"linux", "arm64"}:   ".tar.gz",
	{"android", "arm64"}: ".tar.gz",
	{"windows", "amd64"}: ".zip",
}

// Install replaces the running executable with the release asset and returns
// the executable path. It does not restart.
func Install(ctx context.Context, client *fetch.Client, offer Offer) (string, error) {
	if manualUpdatePlatform {
		return "", errManualUpdate
	}
	name := assetName(offer.Latest, runtime.GOOS, runtime.GOARCH)
	if name == "" {
		return "", fmt.Errorf("update: no release asset for %s/%s of %q", runtime.GOOS, runtime.GOARCH, offer.Latest)
	}
	exe, err := findExecutable()
	if err != nil {
		return "", fmt.Errorf("update: locate executable: %w", err)
	}
	// Resolve a symlinked launcher so the link keeps pointing at the new file.
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("update: resolve executable: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	base := releaseDownloadBase + "/" + offer.Latest

	sums, err := downloadFile(ctx, client, base+"/SHA256SUMS")
	if err != nil {
		return "", fmt.Errorf("update: fetch SHA256SUMS: %w", err)
	}
	want, ok := checksumFor(sums, name)
	if !ok {
		return "", fmt.Errorf("update: SHA256SUMS has no entry for %s", name)
	}
	archive, err := downloadFile(ctx, client, base+"/"+name)
	if err != nil {
		return "", fmt.Errorf("update: fetch %s: %w", name, err)
	}
	digest := sha256.Sum256(archive)
	if got := hex.EncodeToString(digest[:]); got != want {
		return "", fmt.Errorf("update: checksum mismatch for %s: got %s, want %s", name, got, want)
	}

	var binary []byte
	if strings.HasSuffix(name, ".zip") {
		binary, err = extractZip(archive)
	} else {
		binary, err = extractTarGz(archive)
	}
	if err != nil {
		return "", err
	}
	if err := replaceExecutable(exe, binary); err != nil {
		return "", fmt.Errorf("update: replace %s: %w", exe, err)
	}
	return exe, nil
}

// assetName returns the release asset file name, or "" when the version or the
// target is outside the release matrix.
func assetName(version, goos, goarch string) string {
	extension, ok := releaseAssets[[2]string{goos, goarch}]
	if !ok || !IsRelease(version) {
		return ""
	}
	return fmt.Sprintf("booxd_%s_%s_%s%s", strings.TrimPrefix(version, "v"), goos, goarch, extension)
}

// checksumFor returns the digest SHA256SUMS records for name.
func checksumFor(sums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.EqualFold(fields[1], name) {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// downloadFile fetches a whole file, following up to maxRedirects hops.
func downloadFile(ctx context.Context, client *fetch.Client, rawURL string) ([]byte, error) {
	current, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	for hop := 0; ; hop++ {
		stream, err := client.Open(ctx, current.String(), nil)
		if err != nil {
			return nil, err
		}
		if stream.StatusCode == http.StatusOK {
			data, err := io.ReadAll(io.LimitReader(stream.Body, maxAssetSize+1))
			_ = stream.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", current, err)
			}
			if len(data) > maxAssetSize {
				return nil, fmt.Errorf("%s exceeds %d bytes", current, maxAssetSize)
			}
			return data, nil
		}
		location := ""
		if redirectStatus(stream.StatusCode) {
			location = stream.Header.Get("Location")
		}
		_ = stream.Body.Close()
		if location == "" {
			return nil, fmt.Errorf("%s returned status %d", current, stream.StatusCode)
		}
		if hop == maxRedirects {
			return nil, fmt.Errorf("%s exceeded %d redirects", rawURL, maxRedirects)
		}
		next, err := current.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("redirect: %w", err)
		}
		// shortcut: a plain-http origin, which only tests use, may redirect to
		// plain http; an https origin never downgrades. Drop the origin check
		// when every test target serves TLS.
		if next.Scheme != "http" && next.Scheme != "https" {
			return nil, fmt.Errorf("redirect to unsupported scheme %q", next.Scheme)
		}
		if current.Scheme == "https" && next.Scheme != "https" {
			return nil, fmt.Errorf("refusing redirect from https to %s", next.Scheme)
		}
		current = next
	}
}

func redirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// extractTarGz returns the booxd member of a release archive.
func extractTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("update: open archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("update: archive has no %s member", binaryName)
		}
		if err != nil {
			return nil, fmt.Errorf("update: read archive: %w", err)
		}
		if header.Typeflag == tar.TypeReg && header.Name == binaryName {
			return readMember(reader, binaryName)
		}
	}
}

// extractZip returns the booxd.exe member of a Windows release archive.
func extractZip(data []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("update: open archive: %w", err)
	}
	for _, file := range reader.File {
		if file.Name != windowsName {
			continue
		}
		member, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("update: read %s: %w", windowsName, err)
		}
		data, err := readMember(member, windowsName)
		_ = member.Close()
		return data, err
	}
	return nil, fmt.Errorf("update: archive has no %s member", windowsName)
}

// readMember bounds one archive member so a crafted archive cannot fill memory.
func readMember(reader io.Reader, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxAssetSize+1))
	if err != nil {
		return nil, fmt.Errorf("update: read %s: %w", name, err)
	}
	if len(data) > maxAssetSize {
		return nil, fmt.Errorf("update: %s member exceeds %d bytes", name, maxAssetSize)
	}
	return data, nil
}

// replaceExecutable writes data beside exe and renames it into place, so the
// path either keeps its old bytes or gets the new ones.
func replaceExecutable(exe string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(exe), ".booxd-update-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	_, err = file.Write(data)
	if err == nil {
		err = file.Chmod(0o755)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp, exe)
	}
	if err != nil {
		_ = os.Remove(temp)
	}
	return err
}
