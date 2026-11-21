package irc

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
)

// Fetch re-sends b's request form, accepts the archive offered by b.Server,
// and streams it to dstPath through a ".part" file before renaming into place.
// It never resumes: an existing .part is truncated and an existing final file
// is replaced.
func (s *Searcher) Fetch(ctx context.Context, b Book, dstPath string) (int64, error) {
	defer s.Client.ReleaseSearch()

	if dstPath == "" {
		return 0, fmt.Errorf("source irc: fetch: empty dstPath (book %q)", b.ID)
	}
	if b.Raw == "" {
		return 0, fmt.Errorf("source irc: fetch: empty raw line (book %q)", b.ID)
	}
	if b.Server == "" {
		return 0, fmt.Errorf("source irc: fetch: empty server (book %q)", b.ID)
	}

	if err := s.Client.Say(ctx, s.Client.Channel(), b.requestLine()); err != nil {
		return 0, fmt.Errorf("source irc: fetch %q: %w", b.ID, err)
	}

	offer, err := s.awaitFileOffer(ctx, b)
	if err != nil {
		return 0, err
	}

	conn, err := s.opener()(ctx, offer)
	if err != nil {
		return 0, fmt.Errorf("source irc: fetch %q: open offer from %q: %w", b.ID, offer.From, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	part := dstPath + ".part"
	file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("source irc: fetch %q: create %s: %w", b.ID, part, err)
	}

	written, copyErr := io.Copy(file, conn)
	syncErr := file.Sync()
	closeErr := file.Close()

	// Cancellation wins over the byte-count check: the stream is cut short on
	// purpose and the partial file is left for inspection (step 8).
	if ctx.Err() != nil {
		return written, ctx.Err()
	}
	if written != offer.Size {
		return written, fmt.Errorf("source irc: fetch %q: wrote %d of %d bytes; partial kept at %s", b.ID, written, offer.Size, part)
	}
	if copyErr != nil {
		return written, fmt.Errorf("source irc: fetch %q: %w", b.ID, copyErr)
	}
	if syncErr != nil {
		return written, fmt.Errorf("source irc: fetch %q: sync %s: %w", b.ID, part, syncErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("source irc: fetch %q: close %s: %w", b.ID, part, closeErr)
	}

	// Windows refuses to rename over an existing target; remove it first so
	// the same single rename works on every platform (G-15).
	if _, err := os.Stat(dstPath); err == nil {
		if err := os.Remove(dstPath); err != nil {
			return written, fmt.Errorf("source irc: fetch %q: remove %s: %w", b.ID, dstPath, err)
		}
	}
	if err := os.Rename(part, dstPath); err != nil {
		return written, fmt.Errorf("source irc: fetch %q: rename %s: %w", b.ID, dstPath, err)
	}
	return written, nil
}

// awaitFileOffer waits for an offer from b.Server within OfferWait. Offers
// from other nicks are consumed and ignored. It is the only reader of DCC()
// during the wait, so no goroutine outlives the call (G-09).
func (s *Searcher) awaitFileOffer(ctx context.Context, b Book) (dcc.Offer, error) {
	timer := time.NewTimer(s.OfferWait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return dcc.Offer{}, ctx.Err()
		case <-timer.C:
			return dcc.Offer{}, fmt.Errorf("source irc: fetch %q: no offer from %q within %s", b.ID, b.Server, s.OfferWait)
		case offer, ok := <-s.Client.DCC():
			if !ok {
				return dcc.Offer{}, fmt.Errorf("source irc: fetch %q: session ended before offer from %q", b.ID, b.Server)
			}
			if strings.EqualFold(offer.From, b.Server) {
				return offer, nil
			}
		}
	}
}
