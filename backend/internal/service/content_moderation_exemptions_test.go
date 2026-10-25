package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Decode the wire contract so this behavioral regression also compiles on the old schema.
func exemptionFixture(t *testing.T, keyword, api, ban bool, email ...bool) ModerationUserState {
	t.Helper()
	var state ModerationUserState
	mail := len(email) > 0 && email[0]
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"version":2,"keyword_exempt":%t,"api_audit_exempt":%t,"auto_ban_exempt":%t,"email_exempt":%t}`, keyword, api, ban, mail)), &state))
	return state
}

func exemptionService(t *testing.T, mode, policy string, state ModerationUserState) (*ContentModerationService, *moderationRecoveryRepo, *int) {
	t.Helper()
	cfg := defaultContentModerationConfig()
	cfg.Enabled, cfg.Mode, cfg.KeywordBlockingMode = true, mode, policy
	cfg.APIKeys = []string{"offline-fixture"}
	cfg.BlockedKeywords = []string{"blocked-word"}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &moderationRecoveryRepo{contentModerationTestRepo: &contentModerationTestRepo{}, state: state}
	calls := new(int)
	svc := &ContentModerationService{repo: repo, settingRepo: &contentModerationTestSettingRepo{values: map[string]string{SettingKeyRiskControlEnabled: "true", SettingKeyContentModerationConfig: string(raw)}}, asyncQueue: make(chan contentModerationTask, 32), keyHealth: make(map[string]*contentModerationKeyHealth)}
	svc.httpClient = &http.Client{Transport: typeSafeURLRoundTripper(func(*http.Request) (*http.Response, error) {
		*calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"results":[{"category_scores":{"violence":1}}]}`))}, nil
	})}
	return svc, repo, calls
}

func exemptionInput(hit bool) ContentModerationCheckInput {
	text := "ordinary fixture"
	if hit {
		text = "blocked-word fixture"
	}
	return ContentModerationCheckInput{UserID: 306, Protocol: ContentModerationProtocolAnthropicMessages, Body: []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":%q}]}`, text))}
}

func TestModerationExemptionsPolicyMatrix(t *testing.T) {
	for _, policy := range []string{ContentModerationKeywordModeKeywordAndAPI, ContentModerationKeywordModeKeywordOnly, ContentModerationKeywordModeAPIOnly} {
		for mask := 0; mask < 16; mask++ {
			for _, hit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/mask%d/hit%t", policy, mask, hit), func(t *testing.T) {
					keyword, api, ban := mask&1 != 0, mask&2 != 0, mask&4 != 0
					svc, _, calls := exemptionService(t, ContentModerationModePreBlock, policy, exemptionFixture(t, keyword, api, ban, mask&8 != 0))
					decision, err := svc.Check(context.Background(), exemptionInput(hit))
					require.NoError(t, err)
					keywordBlock := hit && !keyword && policy != ContentModerationKeywordModeAPIOnly
					wantCalls := 0
					if !keywordBlock && !api && policy != ContentModerationKeywordModeKeywordOnly {
						wantCalls = 1
					}
					require.Equal(t, wantCalls, *calls, "external moderation call count")
					require.Equal(t, keywordBlock || wantCalls == 1, decision.Blocked)
					if keywordBlock {
						require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
					}
				})
			}
		}
	}
}

func TestModerationExemptionsObserveHasNoKeywordLayer(t *testing.T) {
	for _, policy := range []string{ContentModerationKeywordModeKeywordAndAPI, ContentModerationKeywordModeKeywordOnly, ContentModerationKeywordModeAPIOnly} {
		for mask := 0; mask < 16; mask++ {
			svc, _, calls := exemptionService(t, ContentModerationModeObserve, policy, exemptionFixture(t, mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0))
			decision, err := svc.Check(context.Background(), exemptionInput(true))
			require.NoError(t, err)
			require.True(t, decision.Allowed)
			if mask&2 != 0 {
				require.Empty(t, svc.asyncQueue)
				continue
			}
			require.Len(t, svc.asyncQueue, 1)
			task := <-svc.asyncQueue
			snapshot, err := svc.loadRuntimeSnapshot(context.Background())
			require.NoError(t, err)
			delay := 1
			svc.checkSync(context.Background(), task.input, snapshot.config, task.content, task.inputHash, &delay, false)
			require.Equal(t, 1, *calls)
		}
	}
}

func TestModerationExemptionsRevocationAndQueuedGrant(t *testing.T) {
	svc, _, calls := exemptionService(t, ContentModerationModePreBlock, ContentModerationKeywordModeAPIOnly, exemptionFixture(t, false, true, false))
	cache := &contentModerationTestAuthCacheInvalidator{}
	svc.authCacheInvalidator = cache
	decision, err := svc.Check(context.Background(), exemptionInput(false))
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Zero(t, *calls)
	var action ModerationUserAction
	require.NoError(t, json.Unmarshal([]byte(`{"action":"exemption","version":2,"confirm":true,"reason":"revoke","api_audit_exempt":false}`), &action))
	action.ActorID = 7
	state, err := svc.ManageModerationUser(context.Background(), 306, action)
	require.NoError(t, err)
	require.Equal(t, int64(3), state.Version)
	require.Equal(t, []int64{306}, cache.userIDs)
	decision, err = svc.Check(context.Background(), exemptionInput(false))
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, 1, *calls)
	// An observe job queued before a grant must respect the grant before sending.
	svc, repo, calls := exemptionService(t, ContentModerationModeObserve, ContentModerationKeywordModeKeywordAndAPI, exemptionFixture(t, false, false, false))
	_, err = svc.Check(context.Background(), exemptionInput(true))
	require.NoError(t, err)
	task := <-svc.asyncQueue
	repo.state = exemptionFixture(t, false, true, false)
	snapshot, err := svc.loadRuntimeSnapshot(context.Background())
	require.NoError(t, err)
	delay := 1
	svc.checkSync(context.Background(), task.input, snapshot.config, task.content, task.inputHash, &delay, false)
	require.Zero(t, *calls)
}

func TestModerationExemptionsKeepHashAndFailClosedOnReadError(t *testing.T) {
	svc, repo, calls := exemptionService(t, ContentModerationModePreBlock, ContentModerationKeywordModeKeywordAndAPI, exemptionFixture(t, true, true, true))
	snapshot, err := svc.loadRuntimeSnapshot(context.Background())
	require.NoError(t, err)
	cfg := cloneContentModerationConfig(snapshot.config)
	cfg.PreHashCheckEnabled = true
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	svc.replaceRuntimeConfig(cfg, raw)
	svc.hashCache = &contentModerationTestHashCache{hasResult: true, hasResultUsed: true}
	decision, err := svc.Check(context.Background(), exemptionInput(false))
	require.NoError(t, err)
	require.Equal(t, ContentModerationActionHashBlock, decision.Action)
	require.Zero(t, *calls)
	svc.hashCache = nil
	repo.getErr = context.DeadlineExceeded
	decision, err = svc.Check(context.Background(), exemptionInput(true))
	require.NoError(t, err)
	require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
}

func TestModerationExemptionsLegacyDefaults(t *testing.T) {
	var state ModerationUserState
	require.NoError(t, json.Unmarshal([]byte(`{"auto_ban_exempt":true}`), &state))
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"auto_ban_exempt":true`)
	require.Contains(t, string(raw), `"keyword_exempt":false`)
	require.Contains(t, string(raw), `"api_audit_exempt":false`)
	require.Contains(t, string(raw), `"email_exempt":false`)
	// Auto-ban exemption continues to count without disabling; audit exemptions do not suppress cyber hits.
	for mask := 0; mask < 16; mask++ {
		repo := &moderationRecoveryRepo{contentModerationTestRepo: &contentModerationTestRepo{}, state: exemptionFixture(t, mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0), user: &User{ID: 306, Status: StatusActive}}
		svc := &ContentModerationService{repo: repo}
		cfg := defaultContentModerationConfig()
		cfg.AutoBanEnabled = true
		cfg.BanThreshold = 1
		id, version := int64(306), int64(2)
		log := &ContentModerationLog{UserID: &id, Flagged: true, Action: ContentModerationActionCyberPolicy, RequestID: "fixture", moderationVersion: &version, CreatedAt: time.Now()}
		svc.applyFlaggedAccountSideEffects(context.Background(), cfg, log)
		require.Len(t, repo.hits, 1)
		require.Equal(t, mask&4 == 0, repo.user.Status == StatusDisabled)
	}
}

func TestModerationExemptionsTypeSafeAdapter(t *testing.T) {
	for _, hitCategory := range []string{"violence", "jailbreak", "control_bypass"} {
		for mask := 0; mask < 16; mask++ {
			svc, _, _ := exemptionService(t, ContentModerationModePreBlock, ContentModerationKeywordModeKeywordAndAPI, exemptionFixture(t, mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0))
			snapshot, err := svc.loadRuntimeSnapshot(context.Background())
			require.NoError(t, err)
			cfg := cloneContentModerationConfig(snapshot.config)
			cfg.Engine = ContentModerationEngineTypeSafe
			cfg.Model = "jev-latest"
			cfg.BaseURL = "https://offline.invalid"
			cfg.TypeSafe = moderationEngineDefaults(ContentModerationEngineTypeSafe)
			cfg.TypeSafe.CategorySettings[hitCategory] = ContentModerationCategorySetting{Enabled: true}
			cfg.TypeSafe.BaseURL = cfg.BaseURL
			cfg.TypeSafe.APIKeys = append([]string(nil), cfg.APIKeys...)
			// Exercise the merged exemption gates and persisted per-key URL routing
			// through the real Check -> scheduler -> TypeSafe adapter path.
			cfg.TypeSafe.APIKeyBaseURLs = map[string]string{
				scopedModerationKeyHash(cfg.TypeSafe.APIKeys[0], ContentModerationEngineTypeSafe): "https://per-key.invalid/relay/v1",
			}
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			svc.replaceRuntimeConfig(cfg, raw)
			calls := 0
			svc.httpClient = &http.Client{Transport: typeSafeURLRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "https://per-key.invalid/relay/v1/systemone", req.URL.String())
				var body struct {
					State     string         `json:"state"`
					Questions map[string]any `json:"questions"`
				}
				require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				require.Contains(t, body.State, "blocked-word")
				require.Len(t, body.Questions, 15)
				answers := make(map[string]any)
				for category := range body.Questions {
					answers[category] = map[string]any{"type": "noul", "noul": 0.0}
				}
				answers[hitCategory] = map[string]any{"type": "noul", "noul": 1.0}
				raw, err := json.Marshal(map[string]any{"model": "jev-fixed", "answers": answers})
				require.NoError(t, err)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			decision, err := svc.Check(context.Background(), exemptionInput(true))
			require.NoError(t, err)
			wantCalls := 0
			if mask&3 == 1 {
				wantCalls = 1
			}
			require.Equal(t, wantCalls, calls)
			require.Equal(t, mask&3 != 3, decision.Blocked)
		}
	}
}
