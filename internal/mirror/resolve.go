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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	st := loadState(statePath)
	if st.BaseURL != "" && now.Sub(st.CheckedAt) < stateTTL {
		return st.BaseURL, nil
	}
	var tried []string
	for _, seed := range seeds {
		base := strings.TrimRight(seed, "/")
		if st.DeadUntil[base].After(now) {
			continue
		}
		result := probe(ctx, c, base, probeTimeout)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch result {
		case pass:
			st.BaseURL, st.CheckedAt = base, now
			if err := saveState(statePath, st); err != nil {
				return "", fmt.Errorf("save mirror state: %w", err)
			}
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
	if err := saveState(statePath, st); err != nil {
		return "", fmt.Errorf("save mirror state: %w", err)
	}
	if len(tried) == 0 {
		return "", errors.New("no working mirror: every seed is cooling down after a recent failure")
	}
	return "", fmt.Errorf("no working mirror: %s", strings.Join(tried, ", "))
}
