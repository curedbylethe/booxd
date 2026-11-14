package update

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/cache"
)

func newStore(t *testing.T) *cache.Store {
	t.Helper()
	dir, err := cache.Path(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cache.Open(dir)
}

func TestCheckFreshSkipsLookup(t *testing.T) {
	client, calls := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	store := newStore(t)
	now := time.Unix(1_700_000_000, 0)

	offer, err := Check(context.Background(), client, store, "v1.2.3", now)
	if err != nil {
		t.Fatal(err)
	}
	if !offer.Available || offer.Latest != "v1.2.4" || offer.Current != "v1.2.3" {
		t.Fatalf("offer = %#v; want an available v1.2.4", offer)
	}

	fresh, err := Check(context.Background(), client, store, "v1.2.3", now.Add(CheckTTL-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Available || calls.Load() != 1 {
		t.Fatalf("offer = %#v, requests = %d; want an available offer after 1 request", fresh, calls.Load())
	}

	stale, err := Check(context.Background(), client, store, "v1.2.3", now.Add(CheckTTL))
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Available || calls.Load() != 2 {
		t.Fatalf("offer = %#v, requests = %d; want a second lookup after CheckTTL", stale, calls.Load())
	}
}

func TestCheckZeroCheckedAtIsStale(t *testing.T) {
	client, calls := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	store := newStore(t)
	if err := saveState(store, state{Latest: "v1.2.4"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(context.Background(), client, store, "v1.2.3", time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("requests = %d; a zero check time must not count as fresh", calls.Load())
	}
}

func TestCheckFailedLookupPersistsNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"non-200", http.StatusNotFound, `{"tag_name":"v1.2.4"}`},
		{"bad JSON", http.StatusOK, `{"tag_name":`},
		{"empty tag", http.StatusOK, `{"tag_name":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := releaseServer(t, tc.status, tc.body)
			store := newStore(t)
			offer, err := Check(context.Background(), client, store, "v1.2.3", time.Unix(1_700_000_000, 0))
			if err == nil {
				t.Fatalf("offer = %#v, error = nil; want a lookup error", offer)
			}
			if offer.Current != "v1.2.3" || offer.Available {
				t.Fatalf("offer = %#v; want current only and no offer", offer)
			}
			if _, ok := store.Get(stateKey, 0); ok {
				t.Fatal("state persisted after a failed lookup")
			}
		})
	}
}

func TestCheckVersionGate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"newer release", "v1.2.3", "v1.2.4", true},
		{"dev build", "dev", "v1.2.4", false},
		{"same release", "v1.2.4", "v1.2.4", false},
		{"ahead of release", "v1.3.0", "v1.2.4", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := releaseServer(t, http.StatusOK, `{"tag_name":"`+tc.latest+`"}`)
			store := newStore(t)
			offer, err := Check(context.Background(), client, store, tc.current, time.Unix(1_700_000_000, 0))
			if err != nil {
				t.Fatal(err)
			}
			if offer.Available != tc.want || offer.Latest != tc.latest {
				t.Fatalf("offer = %#v; want available %v for %q", offer, tc.want, tc.latest)
			}
		})
	}
}

func TestDeclineSuppressesOffer(t *testing.T) {
	client, calls := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	store := newStore(t)
	now := time.Unix(1_700_000_000, 0)

	if offer, err := Check(context.Background(), client, store, "v1.2.3", now); err != nil || !offer.Available {
		t.Fatalf("offer = %#v, error = %v; want an available offer", offer, err)
	}
	if err := Decline(store, now); err != nil {
		t.Fatal(err)
	}
	offer, err := Check(context.Background(), client, store, "v1.2.3", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if offer.Available || offer.Latest != "v1.2.4" || calls.Load() != 1 {
		t.Fatalf("offer = %#v, requests = %d; want a declined offer with no new lookup", offer, calls.Load())
	}
}

func TestDeclineResetForNewTag(t *testing.T) {
	client, _ := releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.4"}`)
	store := newStore(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := Check(context.Background(), client, store, "v1.2.3", now); err != nil {
		t.Fatal(err)
	}
	if err := Decline(store, now); err != nil {
		t.Fatal(err)
	}

	client, _ = releaseServer(t, http.StatusOK, `{"tag_name":"v1.2.5"}`)
	offer, err := Check(context.Background(), client, store, "v1.2.3", now.Add(CheckTTL))
	if err != nil {
		t.Fatal(err)
	}
	if !offer.Available || offer.Latest != "v1.2.5" {
		t.Fatalf("offer = %#v; want a new tag to clear the decline", offer)
	}
}

func TestDeclineWithoutState(t *testing.T) {
	store := newStore(t)
	if err := Decline(store, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(stateKey, 0); !ok {
		t.Fatal("decline not persisted")
	}
}
