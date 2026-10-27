//go:build integration

package repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func qualityLiveRouter(f *repository.QualityLiveFixture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := admin.NewScheduledTestHandler(service.NewScheduledTestService(f.Plans, repository.NewScheduledTestResultRepository(repository.QualityLiveDB())))
	r := gin.New()
	r.GET("/plans/:id/ownership", h.QualityOwnershipPreview)
	r.POST("/plans/:id/restore", h.QualityOwnershipRestore)
	r.DELETE("/plans/:id", h.Delete)
	return r
}
func qualityLiveRequest(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	if body == nil {
		data = nil
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func qualityLivePreview(t *testing.T, r *gin.Engine, id int64) service.QualityOwnershipPreview {
	t.Helper()
	w := qualityLiveRequest(r, "GET", fmt.Sprintf("/plans/%d/ownership", id), nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var p service.QualityOwnershipPreview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	return p
}
func qualityLiveFinish(t *testing.T, f *repository.QualityLiveFixture) {
	t.Helper()
	require.NoError(t, f.Plans.FinishPelican(context.Background(), f.Plan.ID, f.Until, time.Now()))
}
func qualityLiveRestore(r *gin.Engine, id int64, fence string) *httptest.ResponseRecorder {
	return qualityLiveRequest(r, "POST", fmt.Sprintf("/plans/%d/restore", id), map[string]any{"confirm": true, "fence": fence})
}

func TestQualityLiveDeleteHTTPRestoresMatchingOwnershipThenDeletes(t *testing.T) {
	f, group, other := repository.NewQualityLiveFixture(t)
	require.Equal(t, "groups_removed", f.Round("failed", "failed"))
	qualityLiveFinish(t, f)
	r := qualityLiveRouter(f)
	path := fmt.Sprintf("/plans/%d", f.Plan.ID)
	require.Equal(t, 409, qualityLiveRequest(r, "DELETE", path, nil).Code, "retain the original isolation deletion guard")
	p := qualityLivePreview(t, r, f.Plan.ID)
	require.True(t, p.RestoreReady)
	require.Equal(t, 400, qualityLiveRequest(r, "POST", path+"/restore", map[string]any{"fence": p.Fence}).Code)
	require.Equal(t, 1, f.Count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
	w := qualityLiveRestore(r, f.Plan.ID, p.Fence)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, f.Extra(), service.QualityModelIsolationKey)
	var status string
	var schedulable, enabled bool
	var concurrency, priority int
	var allowed string
	require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT status,schedulable,concurrency FROM accounts WHERE id=$1`, f.Account).Scan(&status, &schedulable, &concurrency))
	require.Equal(t, "active", status)
	require.True(t, schedulable)
	require.Equal(t, 20, concurrency)
	require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT enabled FROM scheduled_test_plans WHERE id=$1`, f.Plan.ID).Scan(&enabled))
	require.False(t, enabled)
	require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT priority,allowed_models::text FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.Account, group).Scan(&priority, &allowed))
	require.Equal(t, 7, priority)
	require.JSONEq(t, `["sol","astra"]`, allowed)
	require.Equal(t, 2, f.Count(`SELECT count(*) FROM account_groups WHERE account_id=$1`))
	require.Positive(t, f.Count(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`))
	require.Equal(t, "stale_run", f.Round("failed", "failed"), "an old worker cannot recreate restored ownership")
	fresh := qualityLivePreview(t, r, f.Plan.ID)
	confirmation := map[string]any{"confirm": true, "fence": fresh.Fence}
	require.Equal(t, 200, qualityLiveRequest(r, "DELETE", path, confirmation).Code)
	require.Equal(t, 200, qualityLiveRequest(r, "DELETE", path, confirmation).Code, "deletion retry is idempotent")
	var remaining int
	require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT count(*) FROM scheduled_test_plans WHERE id=$1`, f.Plan.ID).Scan(&remaining))
	require.Zero(t, remaining)
	require.Equal(t, 2, f.Count(`SELECT count(*) FROM account_groups WHERE account_id=$1 AND group_id IN (`+qualityLiveID(group)+`,`+qualityLiveID(other)+`)`))
	var states int
	require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT count(*) FROM account_quality_states WHERE plan_id=$1`, f.Plan.ID).Scan(&states))
	require.Zero(t, states)
}

func TestQualityLiveDeleteHTTPRejectsStaleFenceAndConflictsWithoutWrites(t *testing.T) {
	for _, change := range []string{"rule", "account", "state", "model", "membership", "orphan", "inactive", "running"} {
		t.Run(change, func(t *testing.T) {
			f, group, _ := repository.NewQualityLiveFixture(t)
			f.Round("failed", "failed")
			qualityLiveFinish(t, f)
			r := qualityLiveRouter(f)
			p := qualityLivePreview(t, r, f.Plan.ID)
			require.True(t, p.RestoreReady)
			var err error
			switch change {
			case "rule":
				_, err = repository.QualityLiveDB().Exec(`UPDATE scheduled_test_plans SET updated_at=clock_timestamp() WHERE id=$1`, f.Plan.ID)
			case "account":
				f.Exec(`UPDATE accounts SET updated_at=clock_timestamp() WHERE id=$1`)
			case "state":
				_, err = repository.QualityLiveDB().Exec(`UPDATE account_quality_states SET state=jsonb_set(state,'{quality_5xx_episode}','123') WHERE plan_id=$1`, f.Plan.ID)
			case "model":
				f.Exec(`UPDATE accounts SET extra=jsonb_set(extra,'{quality_model_isolation,sol}', '{"reason":"quality_rule:999"}'),updated_at=clock_timestamp() WHERE id=$1`)
			case "membership":
				f.Exec(`INSERT INTO account_groups(account_id,group_id,priority,allowed_models) VALUES($1,$2,99,'["external"]')`, group)
			case "orphan":
				f.Exec(`UPDATE accounts SET extra=jsonb_set(extra,'{quality_model_isolation,orphan}',jsonb_build_object('reason',$2::text)) WHERE id=$1`, fmt.Sprintf("quality_rule:%d", f.Plan.ID))
			case "inactive":
				f.Exec(`UPDATE accounts SET status='disabled',updated_at=clock_timestamp() WHERE id=$1`)
			case "running":
				_, err = repository.QualityLiveDB().Exec(`UPDATE scheduled_test_plans SET running_until=NOW()+interval '5 minutes' WHERE id=$1`, f.Plan.ID)
			}
			require.NoError(t, err)
			var beforeAccount, beforeState string
			require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT row_to_json(a)::text FROM accounts a WHERE id=$1`, f.Account).Scan(&beforeAccount))
			require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.Plan.ID).Scan(&beforeState))
			w := qualityLiveRestore(r, f.Plan.ID, p.Fence)
			require.Equal(t, 409, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "QUALITY_OWNERSHIP_PREVIEW_CHANGED")
			fresh := qualityLivePreview(t, r, f.Plan.ID)
			if change == "model" || change == "membership" || change == "orphan" || change == "inactive" || change == "running" {
				require.False(t, fresh.RestoreReady)
				require.NotEmpty(t, fresh.RestoreBlockedReason)
				require.Equal(t, 409, qualityLiveRestore(r, f.Plan.ID, fresh.Fence).Code)
			}
			var afterAccount, afterState string
			require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT row_to_json(a)::text FROM accounts a WHERE id=$1`, f.Account).Scan(&afterAccount))
			require.NoError(t, repository.QualityLiveDB().QueryRow(`SELECT state::text FROM account_quality_states WHERE plan_id=$1`, f.Plan.ID).Scan(&afterState))
			require.JSONEq(t, beforeAccount, afterAccount)
			require.JSONEq(t, beforeState, afterState)
			require.Equal(t, 409, qualityLiveRequest(r, "DELETE", fmt.Sprintf("/plans/%d", f.Plan.ID), nil).Code)
		})
	}
}

func TestQualityLiveDeleteHTTPRejectsOrphanMarkerAfterRestore(t *testing.T) {
	f, _, _ := repository.NewQualityLiveFixture(t)
	f.Round("failed", "passed")
	qualityLiveFinish(t, f)
	r := qualityLiveRouter(f)
	p := qualityLivePreview(t, r, f.Plan.ID)
	require.Equal(t, 200, qualityLiveRestore(r, f.Plan.ID, p.Fence).Code)
	f.Exec(`UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{quality_model_isolation}',jsonb_build_object('orphan',jsonb_build_object('reason',$2::text))) WHERE id=$1`, fmt.Sprintf("quality_rule:%d", f.Plan.ID))
	w := qualityLiveRequest(r, "DELETE", fmt.Sprintf("/plans/%d", f.Plan.ID), nil)
	require.Equal(t, 409, w.Code, w.Body.String())
	require.Contains(t, f.Extra()[service.QualityModelIsolationKey], "orphan")
}

func qualityLiveID(id int64) string { return fmt.Sprint(id) }

func TestQualityLiveDeleteHTTPRevalidatesVersionAfterRestoration(t *testing.T) {
	f, _, _ := repository.NewQualityLiveFixture(t)
	f.Round("failed", "failed")
	qualityLiveFinish(t, f)
	r := qualityLiveRouter(f)
	p := qualityLivePreview(t, r, f.Plan.ID)
	require.Equal(t, 200, qualityLiveRestore(r, f.Plan.ID, p.Fence).Code)
	fresh := qualityLivePreview(t, r, f.Plan.ID)
	_, err := repository.QualityLiveDB().Exec(`UPDATE scheduled_test_plans SET updated_at=clock_timestamp() WHERE id=$1`, f.Plan.ID)
	require.NoError(t, err)
	path := fmt.Sprintf("/plans/%d", f.Plan.ID)
	w := qualityLiveRequest(r, "DELETE", path, map[string]any{"confirm": true, "fence": fresh.Fence})
	require.Equal(t, 409, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "QUALITY_OWNERSHIP_PREVIEW_CHANGED")
	current, err := f.Plans.GetByID(context.Background(), f.Plan.ID)
	require.NoError(t, err)
	require.False(t, current.Enabled)
	latest := qualityLivePreview(t, r, f.Plan.ID)
	require.Equal(t, 200, qualityLiveRequest(r, "DELETE", path, map[string]any{"confirm": true, "fence": latest.Fence}).Code)
}
