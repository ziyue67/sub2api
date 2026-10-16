// Package ledger implements durable regional reservations. Callers must use
// separate primary and regional databases; no method falls back across regions.
package ledger

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/regional"
)

// USD formats nano dollars without a float conversion. Primary monetary columns
// have eight decimal places, so amounts below that quantum must be rejected,
// not independently rounded by each ledger operation.
func USD(a regional.Amount) (string, error) {
	if a < 0 || a%10 != 0 {
		return "", errors.New("amount must be nonnegative and a multiple of ten nano USD")
	}
	return fmt.Sprintf("%d.%08d", a/1_000_000_000, (a%1_000_000_000)/10), nil
}

// ParseUSD accepts the exact NUMERIC text representation returned by PostgreSQL.
func ParseUSD(s string) (regional.Amount, error) {
	if s == "" {
		return 0, errors.New("empty USD amount")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("invalid USD amount")
	}
	whole, fraction := parts[0], ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" {
			return 0, errors.New("missing USD fraction")
		}
	}
	if len(fraction) > 8 {
		return 0, errors.New("USD precision exceeds primary monetary scale")
	}
	for len(fraction) < 9 {
		fraction += "0"
	}
	value := whole + fraction
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid USD amount")
		}
	}
	a, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New("USD amount outside regional range")
	}
	return regional.Amount(a), nil
}
