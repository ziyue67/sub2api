package ledger

import (
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func textPrice() Price {
	return Price{Model: "mock-text", Input: 1_000_000_000, Output: 2_000_000_000, CacheCreation: 3_000_000_000, CacheRead: 100_000_000, MaxInput: 1000, MaxOutput: 200}
}
func TestPricingExactBoundsAndIdentity(t *testing.T) {
	p := textPrice()
	charge, err := p.Charge(10, 5, 0, 10)
	require.NoError(t, err)
	require.Equal(t, regional.Amount(21000), charge)
	maximum, err := p.Maximum(10)
	require.NoError(t, err)
	require.Equal(t, regional.Amount(3020000), maximum)
	_, err = p.Charge(999, 0, 1, 1)
	require.Error(t, err)
	_, err = p.Maximum(0)
	require.Error(t, err)
	_, err = p.Maximum(201)
	require.Error(t, err)
	changed := p
	changed.MaxInput++
	require.NotEqual(t, p.Version(), changed.Version())
	changed = p
	changed.Input++
	require.NotEqual(t, p.Version(), changed.Version())
	p.Input = 1
	p.Output = 1
	charge, err = p.Charge(1, 1, 0, 0)
	require.NoError(t, err)
	require.Equal(t, regional.Amount(10), charge)
	p.Input = math.MaxInt64
	p.MaxInput = math.MaxInt32
	_, err = p.Maximum(1)
	require.Error(t, err)
}
