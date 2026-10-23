package urlvalidator

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateHTTPSOrigin(t *testing.T) {
	for _, value := range []string{"https://node-a.example.invalid", "https://node-a.example.invalid/", "https://node-a.example.invalid:8443"} {
		require.NoError(t, ValidateHTTPSOrigin(value))
	}
	for _, value := range []string{"", "http://node-a.example.invalid", "https://user:private@node-a.example.invalid", "https://node-a.example.invalid/path", "https://node-a.example.invalid/%2f", "https://node-a.example.invalid?secret=private", "https://node-a.example.invalid#"} {
		err := ValidateHTTPSOrigin(value)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private")
	}
}

func TestHTTPSOriginSharedContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/https-origin-contract.json")
	require.NoError(t, err)
	var cases []struct {
		Name  string `json:"name"`
		URL   string `json:"url"`
		Valid bool   `json:"valid"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := ValidateHTTPSOrigin(tc.URL)
			require.Equal(t, tc.Valid, err == nil)
			if err != nil {
				require.NotContains(t, err.Error(), "private")
				require.NotContains(t, err.Error(), "node-a")
			}
		})
	}
}
