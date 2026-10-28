//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestUpstreamOperationsHalfOpenPagingGroupsAndPathIsolation(t *testing.T) {
	f := newChannelOperationsFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	from := now.Add(-24 * time.Hour)
	for _, a := range []struct {
		id   int64
		base string
	}{{f.account, "https://same.example:8443/A/v1/"}, {f.outsider, "https://same.example:8443/B/v1/"}} {
		_, err := integrationDB.Exec(`UPDATE accounts SET type='apikey',credentials=jsonb_build_object('base_url',$2::text),extra='{"cost_multiplier":0}' WHERE id=$1`, a.id, a.base)
		require.NoError(t, err)
		for i, at := range []time.Time{from, now, from.Add(-time.Microsecond)} {
			_, err = integrationDB.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,total_cost,actual_cost,group_id,created_at) VALUES($1,$2,$3,$4,'per-request',2,1,$5,$6)`, f.user, f.key, a.id, fmt.Sprintf("range-%d-%d", a.id, i), f.group, at)
			require.NoError(t, err)
		}
	}
	q := service.UpstreamOperationsFilter{From: from, Until: now, GroupID: f.group, Dimension: "upstream", ModelScope: "public", Sort: "name", Direction: "asc", Page: 1, PageSize: 1}
	read := func() map[string]any {
		out, err := f.repo.ReadUpstreamOperations(context.Background(), q)
		require.NoError(t, err)
		var data map[string]any
		require.NoError(t, json.Unmarshal(out.Usage, &data))
		require.Equal(t, "ready", out.Errors.Status)
		if q.Model != "" && q.ModelScope != "upstream" {
			require.Equal(t, "unsupported_scope", out.Quality.Status)
		} else {
			require.Equal(t, "ready", out.Quality.Status)
		}
		return data
	}
	first := read()
	require.Equal(t, 2.0, first["total"])
	require.Equal(t, 2.0, first["summary"].(map[string]any)["requests"])
	require.Len(t, first["rows"], 1)
	require.Equal(t, "https://same.example:8443/A/v1", first["rows"].([]any)[0].(map[string]any)["key"])
	q.Page = 2
	second := read()
	require.Equal(t, first["summary"], second["summary"])
	require.Equal(t, "https://same.example:8443/B/v1", second["rows"].([]any)[0].(map[string]any)["key"])
	q.Model = "nonexistent"
	empty := read()
	require.Equal(t, 0.0, empty["summary"].(map[string]any)["requests"])
}

func TestUpstreamOperationsQualityRetainsCompleteFrozenRoundBoundary(t *testing.T) {
	f := newChannelOperationsFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	_, err := integrationDB.Exec(`UPDATE accounts SET type='apikey',credentials='{"base_url":"https://quality.example/v1"}' WHERE id=$1`, f.account)
	require.NoError(t, err)
	var plan int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO scheduled_test_plans(account_id,model_id,cron_expression,enabled,pelican_config) VALUES($1,'current-model','*/30 * * * *',false,'{"quality":{}}') RETURNING id`, f.account).Scan(&plan))
	_, err = integrationDB.Exec(`INSERT INTO scheduled_test_results(plan_id,status,started_at,finished_at,created_at,pelican_config,quality_round_id,quality_action,quality_judgment,error_message) VALUES($1,'success',$2,$2,$2,'{"quality":{},"model_id":"a","model_ids":["a","b"],"parallel_count":1}','frozen-peer','passed','{"verdict":"correct"}','')`, plan, now.Add(-time.Minute))
	require.NoError(t, err)
	q := service.UpstreamOperationsFilter{From: now.Add(-time.Hour), Until: now, Upstream: "https://quality.example/v1", AccountID: f.account, Model: "a", ModelScope: "upstream", Dimension: "model", Sort: "requests", Direction: "desc", Page: 1, PageSize: 20}
	out, err := f.repo.ReadUpstreamOperations(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, "ready", out.Quality.Status)
	var quality struct {
		Summary struct{ Passed, Inconclusive int }
		Rows    []struct {
			Complete bool `json:"round_complete"`
			Outcome  string
		}
	}
	require.NoError(t, json.Unmarshal(out.Quality.Data, &quality))
	require.Zero(t, quality.Summary.Passed, "missing peer still cannot pass after changing upstream cohort")
	require.Equal(t, 1, quality.Summary.Inconclusive)
	require.Len(t, quality.Rows, 1)
	require.False(t, quality.Rows[0].Complete)
}

func TestUpstreamOperationsIdleAccountsDiscoveredWithoutAnyModelChannel(t *testing.T) {
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('idle upstream discovery','openai','apikey','{"base_url":"https://idle.example:8443/A/v1/"}') RETURNING id`).Scan(&id))
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, id) })
	now := time.Now().UTC()
	q := service.UpstreamOperationsFilter{From: now.Add(-time.Hour), Until: now, AccountID: id, Dimension: "upstream", ModelScope: "public", Sort: "requests", Direction: "desc", Page: 1, PageSize: 20}
	out, err := (&channelRepository{db: integrationDB}).ReadUpstreamOperations(context.Background(), q)
	require.NoError(t, err)
	var data struct {
		Summary struct{ Requests int }
		Catalog []struct {
			Key      string
			Accounts int
		}
		Members []struct {
			ID  int64
			Key string
		}
		MemberTotal int `json:"member_total"`
	}
	require.NoError(t, json.Unmarshal(out.Usage, &data))
	require.Zero(t, data.Summary.Requests)
	require.Len(t, data.Catalog, 1)
	require.Equal(t, "https://idle.example:8443/A/v1", data.Catalog[0].Key)
	require.Equal(t, 1, data.Catalog[0].Accounts)
	require.Len(t, data.Members, 1)
	require.Equal(t, id, data.Members[0].ID)
	require.Equal(t, 1, data.MemberTotal)
}
