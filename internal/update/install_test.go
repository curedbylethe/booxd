package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/curedbylethe/booxd/internal/fetch"
)

func TestAssetName(t *testing.T) {
	for _, tc := range []struct {
		goos, goarch, want string
	}{
		{"darwin", "amd64", "booxd_1.2.4_darwin_amd64.tar.gz"},
		{"darwin", "arm64", "booxd_1.2.4_darwin_arm64.tar.gz"},
		{"linux", "amd64", "booxd_1.2.4_linux_amd64.tar.gz"},
		{"linux", "arm64", "booxd_1.2.4_linux_arm64.tar.gz"},
		{"android", "arm64", "booxd_1.2.4_android_arm64.tar.gz"},
		{"windows", "amd64", "booxd_1.2.4_windows_amd64.zip"},
		{"linux", "386", ""},
		{"windows", "arm64", ""},
		{"freebsd", "amd64", ""},
	} {
		if got := assetName("v1.2.4", tc.goos, tc.goarch); got != tc.want {
			t.Errorf("assetName(v1.2.4, %s, %s) = %q; want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
	for _, version := range []string{"1.2.4", "dev", "v1.2", ""} {
		if got := assetName(version, "linux", "arm64"); got != "" {
			t.Errorf("assetName(%q) = %q; want no asset", version, got)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := []byte("aaaa  install.sh\n" +
		"BBBB  booxd_1.2.4_linux_arm64.tar.gz\n" +
		"cccc\n")
	digest, ok := checksumFor(sums, "booxd_1.2.4_linux_arm64.tar.gz")
	if !ok || digest != "bbbb" {
		t.Fatalf("digest = %q, ok = %v; want bbbb, true", digest, ok)
	}
	if digest, ok := checksumFor(sums, "booxd_1.2.4_darwin_arm64.tar.gz"); ok {
		t.Fatalf("digest = %q, ok = true; want no entry", digest)
	}
}

func tarGz(t *testing.T, names ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for _, name := range names {
		body := []byte("binary " + name)
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zipArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("binary " + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractTarGz(t *testing.T) {
	binary, err := extractTarGz(tarGz(t, "README", "booxd"))
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "binary booxd" {
		t.Fatalf("member = %q; want the booxd content", binary)
	}
	for _, names := range [][]string{{"other"}, {"dir/booxd"}, {}} {
		if _, err := extractTarGz(tarGz(t, names...)); err == nil {
			t.Errorf("members %v: error = nil; want a missing-member error", names)
		}
	}
	if _, err := extractTarGz([]byte("not gzip")); err == nil {
		t.Error("garbage: error = nil; want an open error")
	}
}

func TestExtractZip(t *testing.T) {
	binary, err := extractZip(zipArchive(t, "README", "booxd.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "binary booxd.exe" {
		t.Fatalf("member = %q; want the booxd.exe content", binary)
	}
	if _, err := extractZip(zipArchive(t, "booxd")); err == nil {
		t.Error("missing member: error = nil; want a missing-member error")
	}
	if _, err := extractZip([]byte("not zip")); err == nil {
		t.Error("garbage: error = nil; want an open error")
	}
}

// installServer serves SHA256SUMS and one archive at the download base and
// reports how many requests it answered.
func installServer(t *testing.T, archive, sums []byte) (*fetch.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if filepath.Base(r.URL.Path) == "SHA256SUMS" {
			_, _ = w.Write(sums)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	previous := releaseDownloadBase
	releaseDownloadBase = server.URL
	t.Cleanup(func() { releaseDownloadBase = previous })
	return fetch.New(fetch.WithInterval(0)), &calls
}

// installTarget writes original at a temporary executable path and points
// findExecutable at it.
func installTarget(t *testing.T, original []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "booxd")
	if err := os.WriteFile(path, original, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := findExecutable
	findExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { findExecutable = previous })
	// The replace path is a Unix one; keep the Windows runner on it too.
	previousPlatform := manualUpdatePlatform
	manualUpdatePlatform = false
	t.Cleanup(func() { manualUpdatePlatform = previousPlatform })
	return path
}

// sumsFor renders one SHA256SUMS line in sha256sum format.
func sumsFor(name string, archive []byte) []byte {
	digest := sha256.Sum256(archive)
	return []byte(hex.EncodeToString(digest[:]) + "  " + name + "\n")
}

// hostAsset returns the archive name for the running target, skipping a target
// outside the release matrix.
func hostAsset(t *testing.T) string {
	t.Helper()
	name := assetName("v1.2.4", runtime.GOOS, runtime.GOARCH)
	if name == "" {
		t.Skipf("host %s/%s outside the release matrix", runtime.GOOS, runtime.GOARCH)
	}
	return name
}

func sampleOffer() Offer { return Offer{Current: "v1.2.3", Latest: "v1.2.4", Available: true} }

func TestInstall(t *testing.T) {
	archive := tarGz(t, "booxd")
	asset := hostAsset(t)
	client, calls := installServer(t, archive, sumsFor(asset, archive))
	target := installTarget(t, []byte("old binary"))

	exe, err := Install(context.Background(), client, sampleOffer())
	if err != nil {
		t.Fatal(err)
	}
	if want := resolved(t, target); exe != want {
		t.Fatalf("exe = %q; want %q", exe, want)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d; want 2", got)
	}
	assertFile(t, target, "binary booxd", 0o755)
	assertNoTemp(t, filepath.Dir(target))
}

func TestInstallThroughSymlink(t *testing.T) {
	archive := tarGz(t, "booxd")
	asset := hostAsset(t)
	client, _ := installServer(t, archive, sumsFor(asset, archive))
	dir := t.TempDir()
	real := filepath.Join(dir, "booxd-real")
	if err := os.WriteFile(real, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "booxd")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	previous := findExecutable
	findExecutable = func() (string, error) { return link, nil }
	t.Cleanup(func() { findExecutable = previous })

	exe, err := Install(context.Background(), client, sampleOffer())
	if err != nil {
		t.Fatal(err)
	}
	if want := resolved(t, real); exe != want {
		t.Fatalf("exe = %q; want %q", exe, want)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("launcher is no longer a symlink: %v %v", info, err)
	}
	assertFile(t, real, "binary booxd", 0o755)
}

func TestInstallKeepsOriginalOnBadChecksum(t *testing.T) {
	archive := tarGz(t, "booxd")
	asset := hostAsset(t)
	for _, tc := range []struct {
		name string
		sums []byte
	}{
		{"mismatch", sumsFor(asset, []byte("other archive"))},
		{"missing entry", sumsFor("booxd_1.0.0_plan9_riscv.tar.gz", archive)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := installServer(t, archive, tc.sums)
			target := installTarget(t, []byte("old binary"))
			if _, err := Install(context.Background(), client, sampleOffer()); err == nil {
				t.Fatal("error = nil; want a checksum error")
			}
			assertFile(t, target, "old binary", 0o755)
			assertNoTemp(t, filepath.Dir(target))
		})
	}
}

func TestInstallFollowsRedirect(t *testing.T) {
	archive := tarGz(t, "booxd")
	asset := hostAsset(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/hop/") {
			http.Redirect(w, r, "/hop"+r.URL.Path, http.StatusFound)
			return
		}
		if filepath.Base(r.URL.Path) == "SHA256SUMS" {
			_, _ = w.Write(sumsFor(asset, archive))
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	previous := releaseDownloadBase
	releaseDownloadBase = server.URL
	t.Cleanup(func() { releaseDownloadBase = previous })
	target := installTarget(t, []byte("old binary"))

	if _, err := Install(context.Background(), fetch.New(fetch.WithInterval(0)), sampleOffer()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("requests = %d; want 4 (two redirects)", got)
	}
	assertFile(t, target, "binary booxd", 0o755)
}

func TestInstallRejects(t *testing.T) {
	hostAsset(t)
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"redirect loop", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
		}},
		{"missing status", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			t.Cleanup(server.Close)
			previous := releaseDownloadBase
			releaseDownloadBase = server.URL
			t.Cleanup(func() { releaseDownloadBase = previous })
			target := installTarget(t, []byte("old binary"))
			if _, err := Install(context.Background(), fetch.New(fetch.WithInterval(0)), sampleOffer()); err == nil {
				t.Fatal("error = nil; want a download error")
			}
			assertFile(t, target, "old binary", 0o755)
			assertNoTemp(t, filepath.Dir(target))
		})
	}
}

func TestInstallManualUpdate(t *testing.T) {
	client, calls := installServer(t, nil, nil)
	previous := manualUpdatePlatform
	manualUpdatePlatform = true
	t.Cleanup(func() { manualUpdatePlatform = previous })

	if _, err := Install(context.Background(), client, sampleOffer()); err != errManualUpdate {
		t.Fatalf("error = %v; want errManualUpdate", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("requests = %d; want 0", got)
	}
}

func TestReplaceExecutableFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "booxd")
	// A directory in the target's place makes the rename fail after the temp
	// file is written.
	if err := os.MkdirAll(filepath.Join(target, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(target, []byte("new binary")); err == nil {
		t.Fatal("error = nil; want a rename error")
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Errorf("original target changed: %v", err)
	}
	assertNoTemp(t, dir)
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFile(t *testing.T, path, want string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("%s content = %q; want %q", path, data, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("%s mode = %o; want %o", path, got, mode)
		}
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".booxd-update-") {
			t.Errorf("temp file %s survived", entry.Name())
		}
	}
}
