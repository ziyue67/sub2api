package repository

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestRegionalFundsConfigTargets(t *testing.T) {
	base := RegionalFundsRuntimeConfig{Mode: "regional", Region: "us", MaximumOutbox: 10, Prices: []ledger.Price{{Model: "mock-text", Input: 1_000_000_000, Output: 2_000_000_000, MaxInput: 100, MaxOutput: 100}}, Targets: []RegionalFundsTarget{{UserID: 1, APIKeyID: 2, AccountID: 3, Amount: 1_000_000_000, BillingMode: "subscription"}}}
	path := filepath.Join(t.TempDir(), "funds.json")
	write := func(c RegionalFundsRuntimeConfig) {
		raw, err := json.Marshal(c)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0600))
	}
	write(base)
	got, err := readRegionalFundsConfig(path)
	require.NoError(t, err)
	require.Equal(t, base.Targets, got.Targets)
	base.Targets = append(base.Targets, base.Targets[0])
	write(base)
	_, err = readRegionalFundsConfig(path)
	require.ErrorContains(t, err, "duplicate")
	base.Targets = base.Targets[:1]
	base.Targets[0].BillingMode = "balance"
	write(base)
	_, err = readRegionalFundsConfig(path)
	require.ErrorContains(t, err, "billing mode")
}

func TestRegionalFundsRefusesUnboundedBatchWorker(t *testing.T) {
	cfg := &config.Config{}
	cfg.Runtime.RegionalFundsFile = "unread-funds-file"
	cfg.BatchImage.QueueEnabled = true
	_, err := ProvideRegionalMetering(nil, cfg, nil, nil)
	require.ErrorContains(t, err, "batch image queue")
}
