package typesafe

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Anonymous shape from the existing audit endpoint: all values are synthetic;
// no real request ID, provider name, prompt, response text or usage is retained.
func TestTypeSafeQualityChoiceMetadataCompatibility(t *testing.T) {
	body, err := os.ReadFile("testdata/quality-choice-metadata.json")
	require.NoError(t, err)
	criteria := map[string]string{"correct": "same", "incorrect": "different", "unknown": "uncertain"}
	result, err := decodeChoice(body, criteria, "quality")
	require.NoError(t, err, "confirmed bounded id/provider metadata must not discard a valid native choice")
	require.Equal(t, "correct", result.Choice)
	require.Equal(t, 1.0, result.Confidence)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "synthetic")
	require.NotContains(t, string(encoded), "provider")

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	for _, key := range []string{"id", "provider"} {
		delete(fields, key)
		without, err := json.Marshal(fields)
		require.NoError(t, err)
		_, err = decodeChoice(without, criteria, "quality")
		require.NoError(t, err, "metadata is optional")
	}
}

func TestTypeSafeQualityChoiceMetadataKeepsStrictBoundaries(t *testing.T) {
	fixture, err := os.ReadFile("testdata/quality-choice-metadata.json")
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fixture, &fields))
	criteria := map[string]string{"correct": "same", "incorrect": "different", "unknown": "uncertain"}
	for _, key := range []string{"id", "provider"} {
		limit := 128
		if key == "provider" {
			limit = 64
		}
		for _, raw := range []string{`null`, `7`, `true`, `{}`, `[]`, `""`, `"   "`, `"line\nbreak"`, `"\u0000"`, `"` + strings.Repeat("x", limit+1) + `"`, `"` + strings.Repeat("界", limit/3+1) + `"`} {
			t.Run(key+"/"+raw[:min(len(raw), 25)], func(t *testing.T) {
				var copy map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(fixture, &copy))
				copy[key] = json.RawMessage(raw)
				body, err := json.Marshal(copy)
				require.NoError(t, err)
				_, err = decodeChoice(body, criteria, "quality")
				require.Error(t, err)
			})
		}
		fields[key] = json.RawMessage(`"` + strings.Repeat("x", limit) + `"`)
	}
	bounded, err := json.Marshal(fields)
	require.NoError(t, err)
	_, err = decodeChoice(bounded, criteria, "quality")
	require.NoError(t, err)
	valid := string(fixture)
	for name, body := range map[string]string{
		"unknown metadata":   strings.Replace(valid, `"id":`, `"extension":"ignored?","id":`, 1),
		"duplicate id":       strings.Replace(valid, `"id":`, `"id":"first","id":`, 1),
		"escaped duplicate":  strings.Replace(valid, `"id":`, `"\u0069d":"first","id":`, 1),
		"alias id":           strings.Replace(valid, `"id":`, `"ID":`, 1),
		"alias provider":     strings.Replace(valid, `"provider":`, `"Provider":`, 1),
		"duplicate usage":    strings.Replace(valid, `"input_tokens": 100`, `"input_tokens":1,"input_tokens":100`, 1),
		"refusal":            strings.Replace(valid, `"id":`, `"refusal":"refused","id":`, 1),
		"malformed choice":   strings.Replace(valid, `"choice": "correct"`, `"choice":"not-a-choice"`, 1),
		"null confidence":    strings.Replace(valid, `"confidence": 1.0`, `"confidence":null`, 1),
		"bad probabilities":  strings.Replace(valid, `"correct": 0.98`, `"correct":0.4`, 1),
		"deep ignored usage": strings.Replace(valid, `"synthetic_flag": false`, `"nested":`+strings.Repeat(`[`, 20)+`0`+strings.Repeat(`]`, 20), 1),
		"oversized":          strings.Repeat(" ", (4<<20)+1) + valid,
	} {
		t.Run(name, func(t *testing.T) { _, err := decodeChoice([]byte(body), criteria, "quality"); require.Error(t, err) })
	}
}
