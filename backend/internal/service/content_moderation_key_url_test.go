package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestContentModerationPerKeyURLPersistenceAndRouting(t *testing.T) {
	ctx := context.Background()
	cfg := defaultContentModerationConfig()
	cfg.Engine = "typesafe"
	cfg.APIKeys = []string{"fixture-unrelated-engine"}
	cfg.TypeSafe = moderationEngineDefaults("typesafe")
	keys := []string{"fixture-first-shared", "fixture-second-shared"}
	cfg.TypeSafe.APIKeys = keys
	cfg.TypeSafe.BaseURL = "https://default.example"
	raw, _ := json.Marshal(cfg)
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{SettingKeyContentModerationConfig: string(raw)}}
	s := runtimeCacheTestService(repo, time.Hour)
	beforeSnapshot, err := s.loadRuntimeSnapshot(ctx)
	require.NoError(t, err)
	s.markAPIKeyError(keys[0], "typesafe API status 401", 10, 401, "typesafe")
	beforeHealth := s.apiKeyStatuses(keys, "typesafe")
	save := func(hash, base, expected string) error {
		payload, _ := json.Marshal(map[string]any{"engine_configs": map[string]any{"typesafe": map[string]any{"key_url_updates": []map[string]string{{"key_hash": hash, "base_url": base, "expected_base_url": expected}}}}})
		var in UpdateContentModerationConfigInput
		require.NoError(t, json.Unmarshal(payload, &in))
		_, err := s.UpdateConfig(ctx, in)
		return err
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, base := range []string{"https://first.example/relay/v1/", "https://second.example/v1"} {
		wg.Add(1)
		go func(i int, base string) {
			defer wg.Done()
			errs <- save(scopedModerationKeyHash(keys[i], "typesafe"), base, cfg.TypeSafe.BaseURL)
		}(i, base)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	fresh := &ContentModerationService{settingRepo: repo}
	view, err := fresh.GetConfig(ctx)
	require.NoError(t, err)
	body, _ := json.Marshal(view)
	var response struct {
		Statuses []struct {
			BaseURL string `json:"base_url"`
		} `json:"api_key_statuses"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.Equal(t, "https://first.example/relay/v1", response.Statuses[0].BaseURL)
	require.Equal(t, "https://second.example/v1", response.Statuses[1].BaseURL)
	body, _ = json.Marshal(view)
	for _, key := range keys {
		require.False(t, strings.Contains(string(body), key))
	}
	require.Equal(t, beforeHealth, s.apiKeyStatuses(keys, "typesafe"))
	snapshot, err := s.loadRuntimeSnapshot(ctx)
	require.NoError(t, err)
	before, _ := repo.GetValue(ctx, SettingKeyContentModerationConfig)
	for _, tc := range []struct{ hash, base, expected string }{
		{scopedModerationKeyHash(keys[0], "typesafe"), "", "https://first.example/relay/v1"},
		{scopedModerationKeyHash(keys[0], "typesafe"), "https://user:fixture@unsafe.example", "https://first.example/relay/v1"},
		{scopedModerationKeyHash(keys[0], "typesafe"), "ftp://relay.example", "https://first.example/relay/v1"},
		{scopedModerationKeyHash(keys[0], "typesafe"), "https://relay.example?token=fixture", "https://first.example/relay/v1"},
		{scopedModerationKeyHash(keys[0], "typesafe"), "https://relay.example#fragment", "https://first.example/relay/v1"},
		{scopedModerationKeyHash(keys[0], "typesafe"), "/v1", "https://first.example/relay/v1"},
		{scopedModerationKeyHash(keys[0], "typesafe"), "https://new.example", "https://stale.example"},
		{"unknown-hash", "https://new.example", "https://default.example"},
	} {
		require.Error(t, save(tc.hash, tc.base, tc.expected))
		after, _ := repo.GetValue(ctx, SettingKeyContentModerationConfig)
		require.True(t, before == after)
	}
	persisted, err := fresh.loadConfig(ctx)
	require.NoError(t, err)
	active := persisted.effectiveEngine("typesafe")
	active.APIKeys = append(active.APIKeys, "fixture-legacy")
	active.APIKeys[0], active.APIKeys[1] = active.APIKeys[1], active.APIKeys[0]
	targets := []string{}
	fresh.httpClient = &http.Client{Transport: typeSafeURLRoundTripper(func(req *http.Request) (*http.Response, error) {
		targets = append(targets, req.URL.String())
		answers := map[string]any{}
		for category := range typeSafeModerationQuestions() {
			answers[category] = map[string]any{"type": "noul", "noul": 0.01}
		}
		body, _ := json.Marshal(map[string]any{"model": "jev-latest", "answers": answers})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	for _, key := range active.APIKeys {
		_, err = fresh.callModerationOnceWithInput(ctx, active, key, "fixture text", nil)
		require.NoError(t, err)
	}
	require.Equal(t, []string{"https://second.example/v1/systemone", "https://first.example/relay/v1/systemone", "https://default.example/v1/systemone"}, targets)
	require.True(t, keys[0] == persisted.TypeSafe.APIKeys[0] && keys[1] == persisted.TypeSafe.APIKeys[1])
	require.True(t, len(persisted.APIKeys) == 1 && persisted.APIKeys[0] == cfg.APIKeys[0])
	targets = nil
	_, err = fresh.callModerationOnceWithInput(ctx, snapshot.config, keys[0], "fixture text", nil)
	require.NoError(t, err)
	_, err = fresh.callModerationOnceWithInput(ctx, beforeSnapshot.config, keys[0], "fixture text", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"https://first.example/relay/v1/systemone", "https://default.example/v1/systemone"}, targets)
	// Exercise real scheduler selection against the persisted effective config.
	targets = nil
	active.RetryCount = 0
	for range active.APIKeys {
		_, err = fresh.callModeration(ctx, active, "fixture text")
		require.NoError(t, err)
	}
	require.Equal(t, []string{"https://second.example/v1/systemone", "https://first.example/relay/v1/systemone", "https://default.example/v1/systemone"}, targets)
	failed := &ContentModerationService{settingRepo: &perKeyURLFailingRepo{repo}}
	next := "https://failed.example"
	var input UpdateContentModerationConfigInput
	payload, _ := json.Marshal(map[string]any{"engine_configs": map[string]any{"typesafe": map[string]any{"key_url_updates": []map[string]string{{"key_hash": scopedModerationKeyHash(keys[0], "typesafe"), "base_url": next, "expected_base_url": "https://first.example/relay/v1"}}}}})
	require.NoError(t, json.Unmarshal(payload, &input))
	_, err = failed.UpdateConfig(ctx, input)
	require.Error(t, err)
	after, _ := repo.GetValue(ctx, SettingKeyContentModerationConfig)
	require.True(t, before == after)

}

type perKeyURLFailingRepo struct {
	*contentModerationRuntimeSettingRepo
}

func (*perKeyURLFailingRepo) Set(context.Context, string, string) error {
	return errors.New("fixture persistence failure")
}
