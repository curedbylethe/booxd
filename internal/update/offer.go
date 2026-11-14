package update

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/fetch"
)

// Options carries the startup check's inputs and its seams for tests.
type Options struct {
	Current  string
	Args     []string
	Client   *fetch.Client
	Store    *cache.Store
	Disabled bool
	In       io.Reader
	Out      io.Writer // stderr in production
	IsTTY    func() bool
	Exec     func(exe string, args []string) error
	Now      time.Time
}

// MaybeOffer performs the whole startup flow. It returns nil in every case;
// the requested command must run regardless of what happens here.
func MaybeOffer(ctx context.Context, opts Options) error {
	if opts.Disabled {
		return nil
	}
	if !opts.IsTTY() {
		return nil
	}
	if !IsRelease(opts.Current) {
		return nil
	}
	offer, err := Check(ctx, opts.Client, opts.Store, opts.Current, opts.Now)
	if err != nil || !offer.Available {
		return nil
	}
	if manualUpdatePlatform {
		_, _ = fmt.Fprintf(opts.Out, "booxd %s: %s is available. Run install.ps1 to upgrade.\n", offer.Current, offer.Latest)
		_ = Decline(opts.Store, opts.Now)
		return nil
	}

	_, _ = fmt.Fprintf(opts.Out, "booxd %s: %s is available.\n", offer.Current, offer.Latest)
	_, _ = fmt.Fprint(opts.Out, "Update and restart? [y/N] ")
	answer, _ := bufio.NewReader(opts.In).ReadString('\n')
	if answer = strings.TrimSpace(answer); answer != "y" && answer != "Y" {
		_ = Decline(opts.Store, opts.Now)
		return nil
	}

	exe, err := Install(ctx, opts.Client, offer)
	if err != nil {
		_, _ = fmt.Fprintf(opts.Out, "booxd: update failed: %v\n", err)
		return nil
	}
	if err := opts.Exec(exe, opts.Args); err != nil {
		_, _ = fmt.Fprintf(opts.Out, "booxd: update failed: %v\n", err)
	}
	return nil
}
