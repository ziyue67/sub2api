package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
	"github.com/stretchr/testify/require"
)

func TestContentModerationDisabledTypeSafeCategoriesStillRequireValidAnswers(t *testing.T) {
	for _, category := range []string{"jailbreak", "control_bypass"} {
		for _, invalid := range []string{"missing", "wrong-type", "out-of-range"} {
			t.Run(category+"/"+invalid, func(t *testing.T) {
				s, _, _ := categoryFixtureService(t, category, 0.90, false)
				calls := 0
				s.httpClient = &http.Client{Transport: typeSafeURLRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls++
					var request typesafe.Request
					require.NoError(t, json.NewDecoder(req.Body).Decode(&request))
					require.Len(t, request.Questions, 15)
					require.Contains(t, request.Questions, category)
					answers := map[string]any{}
					for id := range request.Questions {
						answers[id] = map[string]any{"type": "noul", "noul": 0.01}
					}
					switch invalid {
					case "missing":
						delete(answers, category)
					case "wrong-type":
						answers[category] = map[string]any{"type": "score", "noul": 0.90}
					case "out-of-range":
						answers[category] = map[string]any{"type": "noul", "noul": 1.1}
					}
					raw, err := json.Marshal(map[string]any{"model": "jev-fixture", "answers": answers})
					require.NoError(t, err)
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
				})}
				trial, err := s.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{Prompt: "fixture"})
				require.NoError(t, err)
				require.Nil(t, trial.AuditResult)
				require.Len(t, trial.Items, 1)
				require.Equal(t, "frozen", trial.Items[0].Status)
				require.NotEmpty(t, trial.Items[0].LastError)
				require.Zero(t, trial.Items[0].SuccessCount)
				// A fresh service exercises the actual failure path without the
				// test API's key freeze preventing its upstream attempt.
				actual, _, _ := categoryFixtureService(t, category, 0.90, false)
				actual.httpClient = s.httpClient
				decision, err := actual.Check(context.Background(), exemptionInput(false))
				require.NoError(t, err)
				require.True(t, decision.Allowed)
				require.False(t, decision.Flagged)
				require.False(t, decision.Blocked)
				repo, ok := actual.repo.(*contentModerationTestRepo)
				require.True(t, ok)
				require.Len(t, repo.logs, 1)
				require.Equal(t, ContentModerationActionError, repo.logs[0].Action)
				require.Empty(t, repo.logs[0].CategoryScores)
				require.False(t, repo.logs[0].EngineMeta.CategorySettings[category].Enabled)
				require.Equal(t, 2, calls)
			})
		}
	}
}

func TestContentModerationDisablingCategoryPreservesHistoricalHashBlock(t *testing.T) {
	for _, category := range []string{"jailbreak", "control_bypass"} {
		t.Run(category, func(t *testing.T) {
			ctx := context.Background()
			s, _, calls := categoryFixtureService(t, category, 0.90, true)
			hashes := &contentModerationTestHashCache{}
			s.hashCache = hashes
			enabled := true
			_, err := s.UpdateConfig(ctx, UpdateContentModerationConfigInput{PreHashCheckEnabled: &enabled})
			require.NoError(t, err)
			input := exemptionInput(false)
			decision, err := s.Check(ctx, input)
			require.NoError(t, err)
			require.Equal(t, ContentModerationActionBlock, decision.Action)
			require.Equal(t, 1, *calls)
			task := <-s.asyncQueue
			require.True(t, task.recordHash)
			s.persistContentModerationLog(ctx, task.config, task.log, task.inputHash, task.recordHash, task.applySideEffects)
			require.True(t, hashes.hasHash(task.inputHash))
			settings := map[string]ContentModerationCategorySetting{category: {Enabled: false}}
			view, err := s.UpdateConfig(ctx, UpdateContentModerationConfigInput{CategorySettings: &settings})
			require.NoError(t, err)
			require.False(t, view.CategorySettings[category].Enabled)
			require.True(t, view.PreHashCheckEnabled)
			require.True(t, hashes.hasHash(task.inputHash))
			decision, err = s.Check(ctx, input)
			require.NoError(t, err)
			require.True(t, decision.Blocked)
			require.Equal(t, ContentModerationActionHashBlock, decision.Action)
			require.Equal(t, task.inputHash, decision.InputHash)
			require.Equal(t, 1, *calls, "historical hash rejection precedes the API")
			hashTask := <-s.asyncQueue
			require.Equal(t, ContentModerationActionHashBlock, hashTask.log.Action)
			require.Nil(t, hashTask.log.EngineMeta, "hash rejection is not a new model judgment")
			// Only the existing explicit hash-removal action clears this gate.
			deleted, err := s.DeleteFlaggedInputHash(ctx, task.inputHash)
			require.NoError(t, err)
			require.True(t, deleted.Deleted)
			decision, err = s.Check(ctx, input)
			require.NoError(t, err)
			require.True(t, decision.Allowed)
			require.False(t, decision.Flagged)
			require.Equal(t, 2, *calls)
		})
	}
}
