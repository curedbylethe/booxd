package update

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
	"github.com/curedbylethe/booxd/internal/fetch"
)

const (
	// CheckTTL is how long a cached check answer stays fresh.
	CheckTTL = 24 * time.Hour
	// CheckTimeout bounds the latest-release lookup.
	CheckTimeout = 5 * time.Second
	// stateKey is the identity the update check stores under; the state uses page 0.
	stateKey = "update-check"
)

// Offer reports the running version and whether a newer release is due.
type Offer struct {
	Current   string // running version as compiled, e.g. "v1.2.3" or "dev"
	Latest    string // release tag_name, e.g. "v1.2.4"; empty when unknown
	Available bool   // Latest parses, is strictly newer, and was not declined
}

type state struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
	Declined  bool      `json:"declined"`
}

// Check reads the cached answer, performs the lookup when stale, persists the
// result, and reports whether an offer is due.
func Check(ctx context.Context, client *fetch.Client, store *cache.Store, current string, now time.Time) (Offer, error) {
	stored, ok := loadState(store)
	if ok && !stored.CheckedAt.IsZero() && now.Sub(stored.CheckedAt) < CheckTTL {
		return offer(current, stored), nil
	}
	tag, err := latestTag(ctx, client)
	if err != nil {
		// Record the attempt so a bad network costs one timeout a day, not one
		// per start. Latest keeps its previous value, so nothing is offered.
		stored.CheckedAt = now
		_ = saveState(store, stored)
		return Offer{Current: current}, err
	}
	next := state{CheckedAt: now, Latest: tag, Declined: stored.Declined && stored.Latest == tag}
	// The offer stands even when the cache is unwritable; the next run retries.
	_ = saveState(store, next)
	return offer(current, next), nil
}

// Decline records that the user answered anything other than y for Latest.
func Decline(store *cache.Store, now time.Time) error {
	declined, ok := loadState(store)
	if !ok {
		declined.CheckedAt = now
	}
	declined.Declined = true
	if err := saveState(store, declined); err != nil {
		return fmt.Errorf("update: decline: %w", err)
	}
	return nil
}

// offer derives the answer from the running version and a check state.
func offer(current string, s state) Offer {
	return Offer{
		Current:   current,
		Latest:    s.Latest,
		Available: IsRelease(current) && Newer(current, s.Latest) && !s.Declined,
	}
}

// loadState reads the check state. Entry.FetchedAt carries the check time, so
// the record and the entry never disagree.
func loadState(store *cache.Store) (state, bool) {
	entry, ok := store.Get(cache.Key(stateKey))
	if !ok {
		return state{}, false
	}
	var s state
	if err := json.Unmarshal(entry.Body, &s); err != nil {
		return state{}, false
	}
	s.CheckedAt = entry.FetchedAt
	return s, true
}

func saveState(store *cache.Store, s state) error {
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return store.Put(cache.Entry{Key: cache.Key(stateKey), Query: stateKey, Page: 0, Status: 200, FetchedAt: s.CheckedAt, Body: body})
}
