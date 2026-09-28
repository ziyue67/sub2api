//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The repository manages its own transactions, so fixtures are committed through the
// shared client and removed afterwards. Prune is global by design, so nothing else in
// this package writes pelican_showcase_items.
func TestPelicanShowcaseRepo_PublishListGetPrune(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewPelicanShowcaseRepository(integrationDB)
	plans := NewPelicanGroupTestRepository(integrationDB)
	suffix := time.Now().UnixNano()
	name := func(label string) string { return fmt.Sprintf("showcase-%s-%d", label, suffix) }

	groupA := mustCreateGroup(t, client, &service.Group{Name: name("a")})
	groupB := mustCreateGroup(t, client, &service.Group{Name: name("b")})
	untested := mustCreateGroup(t, client, &service.Group{Name: name("untested")})
	disabled := mustCreateGroup(t, client, &service.Group{Name: name("disabled"), Status: service.StatusDisabled})
	deleted := mustCreateGroup(t, client, &service.Group{Name: name("deleted")})
	allGroups := []int64{groupA.ID, groupB.ID, untested.ID, disabled.ID, deleted.ID}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM pelican_showcase_items WHERE group_id = ANY($1)`, pq.Array(allGroups))
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id = ANY($1)`, pq.Array(allGroups))
	})
	for _, group := range []*service.Group{groupA, groupB, disabled, deleted} {
		_, err := plans.CreatePlan(ctx, &service.PelicanGroupTestPlan{GroupID: group.ID, ModelID: "gpt-6-astra", CronExpression: "0 * * * *",
			PelicanConfig: &service.PelicanTestConfig{QuestionKind: "pelican", Prompt: "pelican", ReasoningEffort: "high", ParallelCount: 1}})
		require.NoError(t, err)
	}
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET sort_order = 1 WHERE id = $1`, groupA.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET deleted_at = NOW() WHERE id = $1`, deleted.ID)
	require.NoError(t, err)

	base := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	publish := func(resultID, groupID int64, maxItems int) {
		t.Helper()
		require.NoError(t, repo.Publish(ctx, service.PelicanShowcaseSnapshot{
			GroupID: groupID, SourceResultID: resultID, ModelID: "gpt-6-astra", ReasoningEffort: "high",
			ResponseText: fmt.Sprintf(`<svg data-result="%d"></svg>`, resultID), LatencyMs: resultID * 100,
			GeneratedAt: base.Add(time.Duration(resultID) * time.Hour),
		}, maxItems))
	}
	sourcesOf := func(groupID int64) []int64 {
		t.Helper()
		rows, err := integrationDB.QueryContext(ctx, `SELECT source_result_id FROM pelican_showcase_items
 WHERE group_id = $1 ORDER BY generated_at DESC`, groupID)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		out := []int64{}
		for rows.Next() {
			var id int64
			require.NoError(t, rows.Scan(&id))
			out = append(out, id)
		}
		require.NoError(t, rows.Err())
		return out
	}

	// Each answer goes to its own group only, which is trimmed to maxItems.
	for id := int64(1); id <= 5; id++ {
		publish(id, groupA.ID, 3)
		publish(id+10, groupB.ID, 3)
	}
	publish(5, groupA.ID, 3) // republishing the same result is a no-op
	require.Equal(t, []int64{5, 4, 3}, sourcesOf(groupA.ID))
	require.Equal(t, []int64{15, 14, 13}, sourcesOf(groupB.ID))
	publish(21, untested.ID, 3)
	require.Equal(t, []int64{21}, sourcesOf(untested.ID), "stored, but a group without a plan is not shown")

	groups, err := repo.ListGroups(ctx)
	require.NoError(t, err)
	shown := map[int64]*service.PelicanShowcaseGroup{}
	order := []int64{}
	for _, group := range groups {
		for _, id := range allGroups {
			if group.ID == id {
				shown[id] = group
				order = append(order, id)
			}
		}
	}
	require.Equal(t, []int64{groupB.ID, groupA.ID}, order, "only active groups with a plan; sort_order, then id")
	require.Equal(t, groupA.Name, shown[groupA.ID].Name)

	items, err := repo.ListItems(ctx, []int64{groupA.ID, groupB.ID}, 2, time.Time{})
	require.NoError(t, err)
	require.Len(t, items, 4)
	for _, item := range items {
		require.Empty(t, item.ResponseText, "the list never carries HTML")
		require.Equal(t, "gpt-6-astra", item.ModelID)
		require.Equal(t, "high", item.ReasoningEffort)
	}
	require.Equal(t, base.Add(5*time.Hour), items[0].GeneratedAt.UTC())
	require.EqualValues(t, 500, items[0].LatencyMs)
	items, err = repo.ListItems(ctx, []int64{groupA.ID}, 3, base.Add(4*time.Hour))
	require.NoError(t, err)
	require.Len(t, items, 2, "items older than the retention cutoff are hidden")

	itemID := func(groupID, source int64) int64 {
		t.Helper()
		var id int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM pelican_showcase_items
 WHERE group_id = $1 AND source_result_id = $2`, groupID, source).Scan(&id))
		return id
	}
	oldest := itemID(groupA.ID, 3)
	item, err := repo.GetItem(ctx, oldest, 3, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, item)
	require.Equal(t, `<svg data-result="3"></svg>`, item.ResponseText)
	for _, hidden := range []struct {
		id       int64
		maxItems int
		since    time.Time
	}{
		{oldest, 2, time.Time{}},                  // beyond the per-group count
		{oldest, 3, base.Add(4 * time.Hour)},      // past the retention window
		{itemID(untested.ID, 21), 3, time.Time{}}, // group without a plan
	} {
		item, err = repo.GetItem(ctx, hidden.id, hidden.maxItems, hidden.since)
		require.NoError(t, err)
		require.Nil(t, item, "%+v", hidden)
	}

	// Prune drops groups without a plan, trims to the count and applies the age limit.
	require.NoError(t, repo.Prune(ctx, 2, time.Time{}))
	require.Equal(t, []int64{5, 4}, sourcesOf(groupA.ID))
	require.Equal(t, []int64{15, 14}, sourcesOf(groupB.ID))
	require.Empty(t, sourcesOf(untested.ID))
	require.NoError(t, repo.Prune(ctx, 2, base.Add(5*time.Hour)))
	require.Equal(t, []int64{5}, sourcesOf(groupA.ID))

	// Deleting a group's plans takes it off the gallery at the next cleanup.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM pelican_group_test_plans WHERE group_id = $1`, groupB.ID)
	require.NoError(t, err)
	require.NoError(t, repo.Prune(ctx, 2, time.Time{}))
	require.Empty(t, sourcesOf(groupB.ID))

	newest := itemID(groupA.ID, 5)
	removed, err := repo.Delete(ctx, newest)
	require.NoError(t, err)
	require.True(t, removed)
	removed, err = repo.Delete(ctx, newest)
	require.NoError(t, err)
	require.False(t, removed)
}
