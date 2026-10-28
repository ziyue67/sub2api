//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func completeTemplateRouter(t *testing.T, observer bool) *gin.Engine {
	t.Helper()
	db := repository.QualityLiveDB()
	// The real repository needs Ent only for dynamic filter dispatch. Passive
	// preset and per-account roundtrips below deliberately do not invoke it.
	svc := service.ProvideScheduledTestService(repository.NewScheduledTestPlanRepository(db), repository.NewScheduledTestResultRepository(db), repository.NewQualityRuleTemplateRepository(nil, db), nil)
	h := admin.NewScheduledTestHandler(svc)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if observer {
		r.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(service.WithObserverScope(c.Request.Context(), []int64{}))
			c.Next()
		})
	}
	r.POST("/presets", h.CreateQualityPreset)
	r.GET("/presets", h.ListQualityPresets)
	r.PUT("/presets/:id", h.UpdateQualityPreset)
	r.POST("/plans", h.Create)
	r.PUT("/plans/:id", h.Update)
	r.GET("/accounts/:id/plans", h.ListByAccount)
	r.GET("/plans/:id/ownership", h.QualityOwnershipPreview)
	r.POST("/plans/:id/remove-model", h.QualityOwnershipRemoveModel)
	return r
}
func completeTemplatePayload(action string, group int64) map[string]any {
	return map[string]any{"name": "Complete reusable quality check", "model_id": "test-model", "cron_expression": "7 */2 * * *", "enabled": false, "max_results": 137,
		"account_id": 99999, "running_until": "2099-01-01T00:00:00Z", "auto_recover": true,
		"pelican_config": map[string]any{"question_kind": "candy", "test_channel": "account", "prompt": "Synthetic question", "reasoning_effort": "xhigh", "parallel_count": 3, "model_ids": []string{"test-model", "peer-model"},
			"quality_model_actions": map[string]string{"test-model": "isolated"}, "quality_restore_diagnostic": map[string]any{"ownership_retained": true}, "trigger_source": "not-a-template-field", "credentials": "synthetic-do-not-copy", "rule_name": "source-display-only",
			"quality": map[string]any{"action": action, "remove_group_ids": []int64{group}, "auto_restore": true, "trigger_on_upstream_5xx": true, "expected_answer": "42", "judge": map[string]any{"kind": "jev", "group_id": 0, "model_id": "", "prompt": "Compare values", "credentials": "synthetic-do-not-copy"}, "remove_models": []string{"stale-model"}, "recovery_concurrency": 9}}}
}
func completeUnchangedBusiness(t *testing.T) string {
	t.Helper()
	var raw string
	require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT jsonb_build_object(
 'accounts',(SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY id),'[]') FROM accounts a),
 'plans',(SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY id),'[]') FROM scheduled_test_plans p),
 'states',(SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY plan_id),'[]') FROM account_quality_states s),
 'groups',(SELECT COALESCE(jsonb_agg(to_jsonb(g) ORDER BY account_id,group_id),'[]') FROM account_groups g),
 'outbox',(SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY id),'[]') FROM scheduler_outbox o),
 'results',(SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY id),'[]') FROM scheduled_test_results r))::text`).Scan(&raw))
	return raw
}
func completeFindPreset(t *testing.T, r *gin.Engine, id int64) service.QualityRulePreset {
	t.Helper()
	w := qualityLiveRequest(r, "GET", "/presets", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var list []service.QualityRulePreset
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	for _, p := range list {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("preset %d not read back", id)
	return service.QualityRulePreset{}
}
func TestQualityTemplateCompleteHTTPRoundtripAllActions(t *testing.T) {
	for _, action := range []string{"observe_only", "remove_groups", "isolate_models", "disable_scheduling"} {
		t.Run(action, func(t *testing.T) {
			f, group, _ := repository.NewQualityLiveFixture(t)
			r := completeTemplateRouter(t, false)
			body := completeTemplatePayload(action, group)
			before := completeUnchangedBusiness(t)
			w := qualityLiveRequest(r, "POST", "/presets", body)
			require.Equal(t, 200, w.Code, w.Body.String())
			var saved service.QualityRulePreset
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &saved))
			t.Cleanup(func() {
				_, _ = repository.QualityLiveDB().Exec(`DELETE FROM quality_rule_presets WHERE id=$1`, saved.ID)
			})
			require.JSONEq(t, before, completeUnchangedBusiness(t), "saving a preset must not execute, distribute, or change any business row")
			loaded := completeFindPreset(t, completeTemplateRouter(t, false), saved.ID)
			require.Equal(t, action, loaded.PelicanConfig.Quality.Action)
			require.Equal(t, action != "observe_only", loaded.PelicanConfig.Quality.AutoRestore)
			if action == "remove_groups" || action == "isolate_models" {
				require.Equal(t, []int64{group}, loaded.PelicanConfig.Quality.RemoveGroupIDs)
			} else {
				require.Empty(t, loaded.PelicanConfig.Quality.RemoveGroupIDs)
			}
			encoded, _ := json.Marshal(loaded)
			for _, forbidden := range []string{"credentials", "source-display-only", "quality_model_actions", "quality_restore_diagnostic", "trigger_source", "account_id", "running_until", "auto_recover"} {
				require.NotContains(t, string(encoded), forbidden)
			}
			// Edit every category through the real passive-template update endpoint.
			body["name"] = "Edited complete template"
			body["cron_expression"] = "9 */3 * * *"
			body["max_results"] = 145
			cfg := body["pelican_config"].(map[string]any)
			cfg["reasoning_effort"] = "medium"
			cfg["parallel_count"] = 2
			w = qualityLiveRequest(r, "PUT", fmt.Sprintf("/presets/%d", saved.ID), body)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.JSONEq(t, before, completeUnchangedBusiness(t), "editing a preset is also passive")
			loaded = completeFindPreset(t, completeTemplateRouter(t, false), saved.ID)
			require.Equal(t, "9 */3 * * *", loaded.CronExpression)
			require.Equal(t, 145, loaded.MaxResults)
			require.Equal(t, 2, loaded.PelicanConfig.ParallelCount)
			// Applying only constructs this draft; ordinary plan save is a separate HTTP
			// request and uses the real guarded repository. No scheduler/run API exists.
			draft := map[string]any{"model_id": loaded.ModelID, "cron_expression": loaded.CronExpression, "enabled": loaded.Enabled, "max_results": loaded.MaxResults, "pelican_config": loaded.PelicanConfig, "preserve_quality_state": true}
			require.JSONEq(t, before, completeUnchangedBusiness(t))
			w = qualityLiveRequest(r, "PUT", fmt.Sprintf("/plans/%d", f.Plan.ID), draft)
			require.Equal(t, 200, w.Code, w.Body.String())
			read, err := f.Plans.GetByID(context.Background(), f.Plan.ID)
			require.NoError(t, err)
			require.Equal(t, loaded.CronExpression, read.CronExpression)
			require.Equal(t, loaded.MaxResults, read.MaxResults)
			require.Equal(t, loaded.Enabled, read.Enabled)
			read.PelicanConfig.ModelID = ""
			require.Equal(t, loaded.PelicanConfig, read.PelicanConfig)
			// Cross-account creation uses a fresh identity, retaining the reusable group
			// target; missing membership is valid because removal only affects matches.
			var target int64
			require.NoError(t, repository.QualityLiveDB().QueryRow(`INSERT INTO accounts(name,platform,type,status) VALUES('complete-template-target','openai','oauth','active') RETURNING id`).Scan(&target))
			t.Cleanup(func() {
				_, _ = repository.QualityLiveDB().Exec(`DELETE FROM scheduled_test_plans WHERE account_id=$1`, target)
				_, _ = repository.QualityLiveDB().Exec(`DELETE FROM accounts WHERE id=$1`, target)
			})
			draft["account_id"] = target
			w = qualityLiveRequest(r, "POST", "/plans", draft)
			require.Equal(t, 200, w.Code, w.Body.String())
			var created service.ScheduledTestPlan
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
			require.Equal(t, target, created.AccountID)
			require.False(t, created.AutoRecover)
			require.Nil(t, created.RunningUntil)
			require.Nil(t, created.LastRunAt)
			w = qualityLiveRequest(r, "GET", fmt.Sprintf("/accounts/%d/plans", target), nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			var plans []service.ScheduledTestPlan
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &plans))
			require.Len(t, plans, 1)
			require.Equal(t, read.PelicanConfig, func() *service.PelicanTestConfig { plans[0].PelicanConfig.ModelID = ""; return plans[0].PelicanConfig }())
		})
	}
}

func TestQualityTemplateCompleteRejectsInvalidGroupsAndPermissions(t *testing.T) {
	f, group, _ := repository.NewQualityLiveFixture(t)
	r := completeTemplateRouter(t, false)
	body := completeTemplatePayload("remove_groups", group)
	body["pelican_config"].(map[string]any)["quality"].(map[string]any)["remove_group_ids"] = []int64{9223372036854770000}
	before := completeUnchangedBusiness(t)
	w := qualityLiveRequest(r, "POST", "/presets", body)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.JSONEq(t, before, completeUnchangedBusiness(t))
	body = completeTemplatePayload("remove_groups", group)
	for _, method := range []string{"POST", "PUT", "GET"} {
		path := "/presets"
		if method == "PUT" {
			path = "/presets/1"
		}
		w = qualityLiveRequest(completeTemplateRouter(t, true), method, path, body)
		require.Equal(t, 403, w.Code, w.Body.String())
	}
	body["account_id"] = f.Account
	delete(body, "auto_recover")
	delete(body["pelican_config"].(map[string]any)["quality"].(map[string]any), "judge")
	w = qualityLiveRequest(completeTemplateRouter(t, true), "POST", "/plans", body)
	require.Equal(t, 403, w.Code, w.Body.String())
	require.JSONEq(t, before, completeUnchangedBusiness(t))
}

func TestQualityTemplateCompleteExistingIsolationGuard(t *testing.T) {
	f, group, _ := repository.NewQualityLiveFixture(t)
	require.Equal(t, "groups_removed", f.Round("failed", "failed"))
	require.NoError(t, f.Plans.FinishPelican(context.Background(), f.Plan.ID, f.Until, time.Now()))
	r := completeTemplateRouter(t, false)
	for _, action := range []string{"observe_only", "remove_groups", "disable_scheduling", "isolate_models"} {
		body := completeTemplatePayload(action, group)
		delete(body, "auto_recover")
		before := completeUnchangedBusiness(t)
		body["preserve_quality_state"] = true
		w := qualityLiveRequest(r, "PUT", fmt.Sprintf("/plans/%d", f.Plan.ID), body)
		require.Equal(t, 409, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), service.QualityScopeConflictReason)
		require.JSONEq(t, before, completeUnchangedBusiness(t), "template application cannot bypass existing isolation")
	}
}

func TestQualityTemplateCompleteLegacyReadAndRevalidation(t *testing.T) {
	f, group, _ := repository.NewQualityLiveFixture(t)
	r := completeTemplateRouter(t, false)
	body := completeTemplatePayload("", group)
	// Represent an actually old persisted row, bypassing new-write normalization.
	raw, _ := json.Marshal(body["pelican_config"])
	var id int64
	require.NoError(t, repository.QualityLiveDB().QueryRow(`INSERT INTO quality_rule_presets(name,model_id,cron_expression,enabled,max_results,pelican_config) VALUES('legacy','test-model','*/30 * * * *',false,137,$1) RETURNING id`, string(raw)).Scan(&id))
	t.Cleanup(func() { _, _ = repository.QualityLiveDB().Exec(`DELETE FROM quality_rule_presets WHERE id=$1`, id) })
	before := completeUnchangedBusiness(t)
	loaded := completeFindPreset(t, r, id)
	require.Equal(t, service.QualityActionObserveOnly, loaded.PelicanConfig.Quality.Action)
	require.False(t, loaded.PelicanConfig.Quality.AutoRestore)
	require.Empty(t, loaded.PelicanConfig.Quality.RemoveGroupIDs)
	require.JSONEq(t, before, completeUnchangedBusiness(t), "legacy read does not write account or plan state")
	// Valid saved presets can become invalid before application. Rule save must
	// reject them after the group was removed, without trusting the loaded preset.
	body = completeTemplatePayload("remove_groups", group)
	w := qualityLiveRequest(r, "POST", "/presets", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	var saved service.QualityRulePreset
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &saved))
	t.Cleanup(func() {
		_, _ = repository.QualityLiveDB().Exec(`DELETE FROM quality_rule_presets WHERE id=$1`, saved.ID)
	})
	_, err := repository.QualityLiveDB().Exec(`UPDATE groups SET deleted_at=clock_timestamp() WHERE id=$1`, group)
	require.NoError(t, err)
	before = completeUnchangedBusiness(t)
	draft := map[string]any{"pelican_config": saved.PelicanConfig, "model_id": saved.ModelID, "enabled": false, "max_results": 137, "preserve_quality_state": true}
	w = qualityLiveRequest(r, "PUT", fmt.Sprintf("/plans/%d", f.Plan.ID), draft)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.JSONEq(t, before, completeUnchangedBusiness(t))
}

func TestQualityTemplateCompleteDetectionModesAndProviders(t *testing.T) {
	for _, kind := range []string{"candy-llm", "random-jev", "probe", "bps-observation"} {
		t.Run(kind, func(t *testing.T) {
			f, group, _ := repository.NewQualityLiveFixture(t)
			r := completeTemplateRouter(t, false)
			body := completeTemplatePayload("observe_only", group)
			cfg := body["pelican_config"].(map[string]any)
			q := cfg["quality"].(map[string]any)
			switch kind {
			case "candy-llm":
				q["judge"] = map[string]any{"kind": "llm", "group_id": group, "model_id": "judge-model", "prompt": "Compare"}
			case "random-jev":
				cfg["question_kind"] = "random_reasoning"
				cfg["prompt"] = ""
				q["expected_answer"] = ""
			case "probe":
				cfg["question_kind"] = "state_probe"
				cfg["parallel_count"] = 1
				cfg["model_ids"] = []string{"test-model"}
				cfg["prompt"] = ""
				delete(q, "judge")
				q["expected_answer"] = ""
			case "bps-observation":
				cfg["test_channel"] = "bps"
			}
			before := completeUnchangedBusiness(t)
			w := qualityLiveRequest(r, "POST", "/presets", body)
			require.Equal(t, 200, w.Code, w.Body.String())
			var saved service.QualityRulePreset
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &saved))
			t.Cleanup(func() {
				_, _ = repository.QualityLiveDB().Exec(`DELETE FROM quality_rule_presets WHERE id=$1`, saved.ID)
			})
			loaded := completeFindPreset(t, completeTemplateRouter(t, false), saved.ID)
			require.Equal(t, saved, loaded)
			require.JSONEq(t, before, completeUnchangedBusiness(t))
			w = qualityLiveRequest(r, "PUT", fmt.Sprintf("/plans/%d", f.Plan.ID), map[string]any{"pelican_config": loaded.PelicanConfig, "model_id": loaded.ModelID, "enabled": false, "max_results": loaded.MaxResults, "preserve_quality_state": true})
			require.Equal(t, 200, w.Code, w.Body.String())
			read, err := f.Plans.GetByID(context.Background(), f.Plan.ID)
			require.NoError(t, err)
			read.PelicanConfig.ModelID = ""
			require.Equal(t, loaded.PelicanConfig, read.PelicanConfig)
		})
	}
}
