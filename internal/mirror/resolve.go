package mirror

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
)

// Resolve reuses a fresh selection or probes seeds in order and persists the result.
func Resolve(ctx context.Context, c *fetch.Client, statePath string, seeds []string, now time.Time) (string, error) {
	return resolve(ctx, c, statePath, seeds, now, probeTimeout)
}

func resolve(ctx context.Context, c *fetch.Client, statePath string, seeds []string, now time.Time, timeout time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	st := loadState(statePath)
	// A timestamp in the future means the clock moved backwards; treat it as stale.
	age := now.Sub(st.CheckedAt)
	if st.BaseURL != "" && age >= 0 && age < stateTTL {
		return st.BaseURL, nil
	}
	var tried []string
	for _, seed := range seeds {
		base := strings.TrimRight(seed, "/")
		if until := st.DeadUntil[base]; until.After(now) && until.Sub(now) <= cooldown {
			continue
		}
		result := probe(ctx, c, base, timeout)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch result {
		case pass:
			st.BaseURL, st.CheckedAt = base, now
			// The cache is an optimization; a failed write must not fail the command.
			_ = saveState(statePath, st)
			return base, nil
		case dead:
			st.DeadUntil[base] = now.Add(cooldown)
			tried = append(tried, base+" (dead)")
		case fail:
			tried = append(tried, base+" (fail)")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	st.BaseURL, st.CheckedAt = "", time.Time{}
	_ = saveState(statePath, st)
	if len(tried) == 0 {
		return "", errors.New("no working mirror: every seed is cooling down after a recent failure")
	}
	return "", fmt.Errorf("no working mirror: %s", strings.Join(tried, ", "))
}
