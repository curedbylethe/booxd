package irc

import (
	"context"
	"io"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
)

const (
	defaultSearchBot  = "search"
	fallbackSearchBot = "searchook"
	defaultZipWait    = 120 * time.Second
	defaultOfferWait  = 180 * time.Second
)

// Client is the slice of *irc.Client the source needs. Declaring it here keeps
// production code independent of internal/irc; only a test asserts the fit.
type Client interface {
	Channel() string
	Say(ctx context.Context, target, text string) error
	DCC() <-chan dcc.Offer
	ReleaseSearch()
}

// Searcher sends searches and reads the archives that answer them.
type Searcher struct {
	Client    Client
	SearchBot string        // default "search"; fallback "searchook"
	ZipWait   time.Duration // default 120s per bot name
	OfferWait time.Duration // default 180s; used by Fetch (stage 04)

	openOffer func(context.Context, dcc.Offer) (io.ReadCloser, error) // nil means dcc.Offer.Open
}

// New fills production defaults. The client's SearchTimeout must be non-zero:
// otherwise an unanswered search never frees its slot on its own.
func New(client Client) *Searcher {
	return &Searcher{
		Client:    client,
		SearchBot: defaultSearchBot,
		ZipWait:   defaultZipWait,
		OfferWait: defaultOfferWait,
	}
}

// opener returns the archive opener: the real DCC dial unless a test replaced
// the seam.
func (s *Searcher) opener() func(context.Context, dcc.Offer) (io.ReadCloser, error) {
	if s.openOffer != nil {
		return s.openOffer
	}
	return func(ctx context.Context, offer dcc.Offer) (io.ReadCloser, error) {
		return offer.Open(ctx)
	}
}
