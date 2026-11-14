package update

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/fetch"
)

// offerOptions builds options that would prompt and fail the test on exec.
func offerOptions(t *testing.T, client *fetch.Client, store *cache.Store, in string) (Options, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	// The prompt path is a Unix one; the Windows notice has its own test.
	previous := manualUpdatePlatform
	manualUpdatePlatform = false
	t.Cleanup(func() { manualUpdatePlatform = previous })
	return Options{
		Current: "v1.2.3",
		Client:  client,
		Store:   store,
		In:      strings.NewReader(in),
		Out:     out,
		IsTTY:   func() bool { return true },
		Exec:    func(string, []string) error { t.Fatal("exec called"); return nil },
		Now:     time.Unix(1_700_000_000, 0),
	}, out
}

func TestMaybeOfferSkips(t *testing.T) {
	client, calls := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	for _, tc := range []struct {
		name  string
		apply func(*Options)
	}{
		{"disabled", func(o *Options) { o.Disabled = true }},
		{"not a terminal", func(o *Options) { o.IsTTY = func() bool { return false } }},
		{"dev build", func(o *Options) { o.Current = "dev" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, out := offerOptions(t, client, newStore(t), "y\n")
			tc.apply(&opts)
			if err := MaybeOffer(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			if out.Len() != 0 {
				t.Fatalf("output = %q; want none", out.String())
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("requests = %d; want 0", calls.Load())
	}
}

func TestMaybeOfferLookupFailure(t *testing.T) {
	client, calls := releaseServer(t, http.StatusInternalServerError, "")
	store := newStore(t)
	opts, out := offerOptions(t, client, store, "y\n")
	if err := MaybeOffer(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || calls.Load() == 0 {
		t.Fatalf("output = %q, requests = %d; want none and at least one attempt", out.String(), calls.Load())
	}

	// The failed attempt is cached, so the next start pays no timeout.
	before := calls.Load()
	again, quiet := offerOptions(t, client, store, "y\n")
	again.Now = again.Now.Add(time.Minute)
	if err := MaybeOffer(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if quiet.Len() != 0 || calls.Load() != before {
		t.Fatalf("output = %q, requests = %d; want none, %d", quiet.String(), calls.Load(), before)
	}
}

func TestMaybeOfferDeclineIsRemembered(t *testing.T) {
	client, calls := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	store := newStore(t)
	opts, out := offerOptions(t, client, store, "n\n")
	if err := MaybeOffer(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	const want = "booxd v1.2.3: v1.2.4 is available.\nUpdate and restart? [y/N] "
	if out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}

	again, quiet := offerOptions(t, client, store, "y\n")
	again.Now = again.Now.Add(time.Minute)
	if err := MaybeOffer(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if quiet.Len() != 0 || calls.Load() != 1 {
		t.Fatalf("output = %q, requests = %d; want none, 1", quiet.String(), calls.Load())
	}
}

func TestMaybeOfferInstallsAndRestarts(t *testing.T) {
	archive := tarGz(t, "booxd")
	asset := hostAsset(t)
	_, _ = releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	downloads, _ := installServer(t, archive, sumsFor(asset, archive))
	target := installTarget(t, []byte("old binary"))

	args := []string{"search", "test"}
	opts, out := offerOptions(t, downloads, newStore(t), "y\n")
	opts.Args = args
	var exe string
	var restarted []string
	opts.Exec = func(path string, got []string) error {
		exe = path
		restarted = append([]string(nil), got...)
		return nil
	}
	if err := MaybeOffer(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if exe != resolved(t, target) || strings.Join(restarted, " ") != strings.Join(args, " ") {
		t.Fatalf("exec = %q %q; want %q %q", exe, restarted, resolved(t, target), args)
	}
	const want = "booxd v1.2.3: v1.2.4 is available.\nUpdate and restart? [y/N] "
	if out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}
	assertFile(t, target, "binary booxd", 0o755)
}

func TestMaybeOfferAfterReplaceIsQuiet(t *testing.T) {
	archive := tarGz(t, "booxd")
	asset := hostAsset(t)
	_, _ = releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	downloads, _ := installServer(t, archive, sumsFor(asset, archive))
	installTarget(t, []byte("old binary"))

	store := newStore(t)
	opts, _ := offerOptions(t, downloads, store, "y\n")
	opts.Exec = func(string, []string) error { return nil }
	if err := MaybeOffer(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	// The restarted process reports the new version and must stay quiet.
	after, quiet := offerOptions(t, downloads, store, "y\n")
	after.Current = "v1.2.4"
	after.Now = after.Now.Add(time.Minute)
	if err := MaybeOffer(context.Background(), after); err != nil {
		t.Fatal(err)
	}
	if quiet.Len() != 0 {
		t.Fatalf("output = %q; want none after the replace", quiet.String())
	}
}

func TestMaybeOfferInstallFailureLine(t *testing.T) {
	archive := tarGz(t, "booxd")
	client, _ := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	installServer(t, archive, []byte("0000  other.tar.gz\n"))
	installTarget(t, []byte("old binary"))

	opts, out := offerOptions(t, client, newStore(t), "y\n")
	if err := MaybeOffer(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.HasPrefix(text, "booxd v1.2.3: v1.2.4 is available.\nUpdate and restart? [y/N] ") {
		t.Fatalf("output = %q; want the prompt first", text)
	}
	if !strings.Contains(text, "booxd: update failed: update: SHA256SUMS has no entry for ") {
		t.Fatalf("output = %q; want the failure line", text)
	}
}

func TestMaybeOfferExecFailureLine(t *testing.T) {
	archive := tarGz(t, "booxd")
	asset := hostAsset(t)
	client, _ := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	installServer(t, archive, sumsFor(asset, archive))
	installTarget(t, []byte("old binary"))

	opts, out := offerOptions(t, client, newStore(t), "y\n")
	opts.Exec = func(string, []string) error { return errors.New("exec denied") }
	if err := MaybeOffer(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "booxd: update failed: exec denied\n") {
		t.Fatalf("output = %q; want the failure line", out.String())
	}
}

func TestMaybeOfferWindowsNotice(t *testing.T) {
	client, calls := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	store := newStore(t)
	opts, out := offerOptions(t, client, store, "y\n")
	// offerOptions forces the Unix path; this test wants the Windows notice.
	previous := manualUpdatePlatform
	manualUpdatePlatform = true
	t.Cleanup(func() { manualUpdatePlatform = previous })
	if err := MaybeOffer(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	const want = "booxd v1.2.3: v1.2.4 is available. Run install.ps1 to upgrade.\n"
	if out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}

	again, quiet := offerOptions(t, client, store, "y\n")
	again.Now = again.Now.Add(time.Minute)
	manualUpdatePlatform = true
	if err := MaybeOffer(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if quiet.Len() != 0 || calls.Load() != 1 {
		t.Fatalf("output = %q, requests = %d; want none, 1", quiet.String(), calls.Load())
	}
}
