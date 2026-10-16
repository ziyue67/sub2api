//go:build unit

package repository

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestRegionalConcurrencyRuntimeConfigRejectsUnsafeMembership(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"disabled-is-not-a-file", "{}", false},
		{"missing-member", `{"Mode":"primary","Instance":"a","Epoch":"one","PrimaryInstances":["b"],"LegacyRedisUser":"old"}`, false},
		{"duplicate-member", `{"Mode":"primary","Instance":"a","Epoch":"one","PrimaryInstances":["a","a"],"LegacyRedisUser":"old"}`, false},
		{"primary", `{"Mode":"primary","Instance":"a","Epoch":"one","PrimaryInstances":["a","b"],"LegacyRedisUser":"old"}`, true},
		{"regional-missing-region", `{"Mode":"regional","Instance":"a","Epoch":"one"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0600))
			_, err := readRegionalConcurrencyConfig(path)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
