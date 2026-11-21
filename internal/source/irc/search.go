package irc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
)

// errAwaitTimeout marks one bot name's ZipWait expiring. It is internal; the
// error returned to callers names the query and the bots.
var errAwaitTimeout = errors.New("offer wait expired")

// Search sends one @search and reads the archive the answering bot offers.
// It sends once: ErrSendTooSoon and ErrSearchPending go back to the caller,
// which decides when to retry (Q-05).
func (s *Searcher) Search(ctx context.Context, q string) ([]Book, error) {
	defer s.Client.ReleaseSearch()

	if err := s.Client.Say(ctx, s.Client.Channel(), "@search "+q); err != nil {
		return nil, fmt.Errorf("source irc: search %q: %w", q, err)
	}

	primary := s.SearchBot
	if primary == "" {
		primary = defaultSearchBot
	}
	allNames := []string{primary}
	if !strings.EqualFold(primary, fallbackSearchBot) {
		allNames = append(allNames, fallbackSearchBot)
	}

	for i := range allNames {
		offer, err := s.awaitOffer(ctx, allNames)
		if err != nil {
			if err != errAwaitTimeout {
				return nil, err // context cancellation
			}
			if i < len(allNames)-1 {
				continue // Q-03: the fallback gets its own ZipWait window
			}
			break
		}

		conn, err := s.opener()(ctx, offer)
		if err != nil {
			return nil, fmt.Errorf("source irc: search %q: open offer from %q: %w", q, offer.From, err)
		}
		defer func() { _ = conn.Close() }()
		// The zip read blocks on the connection; close it when the context
		// ends so a stalled sender cannot outlive the cancellation.
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		books, err := readOffer(conn, offer.Size)
		if err != nil {
			// Cancellation wins over the stream error the closed pipe
			// reports, as in Fetch.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("source irc: search %q: %w", q, err)
		}
		return books, nil
	}

	last := allNames[len(allNames)-1]
	return nil, fmt.Errorf(
		"source irc: search %q: no results offer from %q (bot %q) within %s",
		q, last, primary, s.ZipWait,
	)
}

// awaitOffer waits for an offer from any of names within ZipWait. The
// fallback bot's offer counts while the primary bot's window is still open:
// discarding it would make every early fallback answer time out. Offers from
// other nicks are consumed and ignored; this is the only reader of DCC()
// during the wait, so no goroutine outlives the call (G-09).
func (s *Searcher) awaitOffer(ctx context.Context, names []string) (dcc.Offer, error) {
	offers := s.Client.DCC()
	timer := time.NewTimer(s.ZipWait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return dcc.Offer{}, ctx.Err()
		case <-timer.C:
			return dcc.Offer{}, errAwaitTimeout
		case offer, ok := <-offers:
			if !ok {
				return dcc.Offer{}, errAwaitTimeout // session ended
			}
			for _, name := range names {
				if strings.EqualFold(offer.From, name) {
					return offer, nil
				}
			}
		}
	}
}
