package ledger

import (
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"testing"
)

func TestUSDExactPrimaryQuantum(t *testing.T) {
	for _, a := range []regional.Amount{0, 10, 999999990, 1000000000, 9223372036854775800} {
		s, err := USD(a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseUSD(s)
		if err != nil || a != b {
			t.Fatalf("round trip %d: %d %v", a, b, err)
		}
	}
	for _, a := range []regional.Amount{-10, 1, 999999999} {
		if _, err := USD(a); err == nil {
			t.Fatalf("accepted %d", a)
		}
	}
	for _, s := range []string{"-1.00", "NaN", "1.000000001", "1e3", "1.2.3", "9223372037.00", ""} {
		if _, err := ParseUSD(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
