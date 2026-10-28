package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidChange is wrapped by every validation error, so callers can
// check errors.Is(err, ErrInvalidChange).
var ErrInvalidChange = errors.New("invalid change")

// Validate reports why a change cannot be applied. Invalid changes are
// skipped and reported as problems (docs/02-decisions.md, D8).
func (c Change) Validate() error {
	var reasons []string

	if strings.TrimSpace(c.SKU) == "" {
		reasons = append(reasons, "missing SKU")
	}
	if c.Version < 1 {
		reasons = append(reasons, "version must be 1 or more")
	}

	switch c.Op {
	case OpWithdraw:
		// A withdrawal only needs SKU and version.
	case OpUpsert:
		if strings.TrimSpace(c.Name) == "" {
			reasons = append(reasons, "missing name")
		}
		if c.PriceCents <= 0 {
			reasons = append(reasons, "price must be positive")
		}
		if !isCurrencyCode(c.Currency) {
			reasons = append(reasons, fmt.Sprintf("invalid currency %q", c.Currency))
		}
	default:
		reasons = append(reasons, fmt.Sprintf("unknown operation %q", c.Op))
	}

	if len(reasons) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidChange, strings.Join(reasons, "; "))
}

// isCurrencyCode accepts three upper-case letters, e.g. "EUR".
func isCurrencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
