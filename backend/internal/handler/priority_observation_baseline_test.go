//go:build unit

package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestPriorityObservationBaselineMapping(t *testing.T) {
	body := []byte(`{"model":"public-alias"}`)
	got := openAIModelMappedBody(body, true, " route-model ", func(b []byte, m string) []byte {
		out, err := sjson.SetBytes(b, "model", m)
		require.NoError(t, err)
		return out
	})
	require.Equal(t, "route-model", gjson.GetBytes(got, "model").String(), "forward and scheduler identities must normalize the same mapping")
}
