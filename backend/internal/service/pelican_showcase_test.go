package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type showcaseSettingsStub struct {
	runtime PelicanShowcaseRuntime
	err     error
	saved   []PelicanShowcaseRuntime
}

func (s *showcaseSettingsStub) GetPelicanShowcaseRuntime(context.Context) (PelicanShowcaseRuntime, error) {
	return s.runtime, s.err
}

func (s *showcaseSettingsStub) UpdatePelicanShowcaseSettings(_ context.Context, enabled bool, cfg PelicanShowcaseConfig, apiEnabled *bool) (PelicanShowcaseRuntime, error) {
	if s.err != nil {
		return PelicanShowcaseRuntime{}, s.err
	}
	if apiEnabled != nil {
		s.runtime.APIEnabled = *apiEnabled
	}
	s.runtime.Enabled, s.runtime.Config = enabled, cfg
	s.saved = append(s.saved, s.runtime)
	return s.runtime, nil
}

type publishCall struct {
	snapshot PelicanShowcaseSnapshot
	maxItems int
}

type pruneCall struct {
	maxItems int
	before   time.Time
}

type showcaseRepoStub struct {
	published []publishCall
	pruned    []pruneCall
	groups    []*PelicanShowcaseGroup
	items     []*PelicanShowcaseItem
	listSince time.Time
	listIDs   []int64
	item      *PelicanShowcaseItem
	deleted   bool
}

func (r *showcaseRepoStub) Publish(_ context.Context, snapshot PelicanShowcaseSnapshot, maxItems int) error {
	r.published = append(r.published, publishCall{snapshot, maxItems})
	return nil
}
func (r *showcaseRepoStub) Prune(_ context.Context, maxItems int, before time.Time) error {
	r.pruned = append(r.pruned, pruneCall{maxItems, before})
	return nil
}
func (r *showcaseRepoStub) ListGroups(context.Context) ([]*PelicanShowcaseGroup, error) {
	return r.groups, nil
}
func (r *showcaseRepoStub) ListItems(_ context.Context, groupIDs []int64, _ int, since time.Time) ([]*PelicanShowcaseItem, error) {
	r.listIDs, r.listSince = groupIDs, since
	return r.items, nil
}
func (r *showcaseRepoStub) GetItem(context.Context, int64, int, time.Time) (*PelicanShowcaseItem, error) {
	return r.item, nil
}
func (r *showcaseRepoStub) Delete(context.Context, int64) (bool, error) { return r.deleted, nil }

func enabledShowcase() *showcaseSettingsStub {
	return &showcaseSettingsStub{runtime: PelicanShowcaseRuntime{Enabled: true, APIEnabled: true, Config: PelicanShowcaseConfig{
		MaxItems: 12, AutoCleanup: true, RetentionDays: 3,
	}}}
}

func groupTestSuccess(id int64, output string) *PelicanGroupTestResult {
	started := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	return &PelicanGroupTestResult{ID: id, PlanID: 7, GroupID: 4, Status: "success", ResponseText: output, LatencyMs: 1200, StartedAt: started,
		PelicanConfig: &PelicanTestConfig{ModelID: "gpt-6-astra", ReasoningEffort: "high"}}
}

func TestPelicanShowcaseConfigNormalizeAndParse(t *testing.T) {
	cfg, err := NormalizePelicanShowcaseConfig(PelicanShowcaseConfig{})
	require.NoError(t, err)
	require.Equal(t, PelicanShowcaseConfig{MaxItems: 20, RetentionDays: 7}, cfg)

	for _, bad := range []PelicanShowcaseConfig{{MaxItems: 101}, {MaxItems: -1}, {RetentionDays: 91}, {RetentionDays: -2}} {
		_, err := NormalizePelicanShowcaseConfig(bad)
		require.Error(t, err, "%+v", bad)
	}

	cfg, err = parsePelicanShowcaseConfig("")
	require.NoError(t, err)
	require.Equal(t, DefaultPelicanShowcaseConfig(), cfg, "never configured means defaults")
	_, err = parsePelicanShowcaseConfig("{broken")
	require.Error(t, err, "corrupt config must not silently become defaults")
	cfg, err = parsePelicanShowcaseConfig(`{"group_ids":[3,9],"max_items":8,"auto_cleanup":false,"retention_days":5}`)
	require.NoError(t, err, "configs saved before group tests still parse; their group list is ignored")
	require.Equal(t, PelicanShowcaseConfig{MaxItems: 8, RetentionDays: 5}, cfg)

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	require.True(t, PelicanShowcaseConfig{AutoCleanup: false, RetentionDays: 3}.retentionCutoff(now).IsZero())
	require.Equal(t, now.Add(-72*time.Hour), PelicanShowcaseConfig{AutoCleanup: true, RetentionDays: 3}.retentionCutoff(now))
}

func TestPelicanShowcasePublishesOnlyHTMLGroupAnswers(t *testing.T) {
	ctx := context.Background()
	failed := groupTestSuccess(1, "<html>")
	failed.Status = "failed"
	skipped := []struct {
		name     string
		settings *showcaseSettingsStub
		groupID  int64
		result   *PelicanGroupTestResult
	}{
		{"failed run", enabledShowcase(), 4, failed},
		{"answer without HTML", enabledShowcase(), 4, groupTestSuccess(1, "21")},
		{"unsaved result", enabledShowcase(), 4, groupTestSuccess(0, "<svg></svg>")},
		{"no group", enabledShowcase(), 0, groupTestSuccess(1, "<svg></svg>")},
		{"unreadable settings", &showcaseSettingsStub{err: errors.New("db down")}, 4, groupTestSuccess(1, "<svg></svg>")},
	}
	for _, tc := range skipped {
		repo := &showcaseRepoStub{}
		svc := &PelicanShowcaseService{repo: repo, settings: tc.settings}
		svc.PublishGroupResult(ctx, tc.groupID, tc.result)
		require.Empty(t, repo.published, tc.name)
	}

	repo := &showcaseRepoStub{}
	settings := enabledShowcase()
	settings.runtime.Enabled = false
	svc := &PelicanShowcaseService{repo: repo, settings: settings}
	result := groupTestSuccess(55, "<!DOCTYPE html><html><body><svg></svg></body></html>")
	svc.PublishGroupResult(ctx, 4, result)
	require.Equal(t, []publishCall{{snapshot: PelicanShowcaseSnapshot{
		GroupID: 4, SourceResultID: 55, ModelID: "gpt-6-astra", ReasoningEffort: "high",
		ResponseText: result.ResponseText, LatencyMs: 1200, GeneratedAt: result.StartedAt,
	}, maxItems: 12}}, repo.published, "a gallery opened later starts with recent answers")

	var nilService *PelicanShowcaseService
	require.NotPanics(t, func() { nilService.PublishGroupResult(ctx, 4, groupTestSuccess(1, "<svg></svg>")) })
}

func TestPelicanShowcaseSettingsGoThroughSettingService(t *testing.T) {
	ctx := context.Background()
	settings := enabledShowcase()
	svc := &PelicanShowcaseService{repo: &showcaseRepoStub{}, settings: settings}
	runtime, err := svc.Settings(ctx)
	require.NoError(t, err)
	require.True(t, runtime.Enabled)

	runtime, err = svc.UpdateSettings(ctx, false, PelicanShowcaseConfig{MaxItems: 30, RetentionDays: 9}, nil)
	require.NoError(t, err)
	require.Equal(t, PelicanShowcaseRuntime{APIEnabled: true, Config: PelicanShowcaseConfig{MaxItems: 30, RetentionDays: 9}}, runtime)
	require.Len(t, settings.saved, 1)
}

func TestPelicanShowcaseCleanupAppliesLimitsAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	repo := &showcaseRepoStub{}
	(&PelicanShowcaseService{repo: repo, settings: &showcaseSettingsStub{err: errors.New("corrupt")}}).Cleanup(ctx, now)
	require.Empty(t, repo.pruned, "unreadable settings must never delete snapshots")

	settings := enabledShowcase()
	settings.runtime.Enabled = false
	(&PelicanShowcaseService{repo: repo, settings: settings}).Cleanup(ctx, now)
	require.Equal(t, []pruneCall{{maxItems: 12, before: now.Add(-72 * time.Hour)}}, repo.pruned,
		"cleanup keeps running while the gallery is off")

	repo = &showcaseRepoStub{}
	settings.runtime.Config.AutoCleanup = false
	(&PelicanShowcaseService{repo: repo, settings: settings}).Cleanup(ctx, now)
	require.True(t, repo.pruned[0].before.IsZero(), "auto cleanup off keeps only the count limit")

	var nilService *PelicanShowcaseService
	require.NotPanics(t, func() { nilService.Cleanup(ctx, now) })
}

func TestPelicanShowcaseViewAndItemVisibility(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	repo := &showcaseRepoStub{}
	off := enabledShowcase()
	off.runtime.Enabled = false
	view, err := (&PelicanShowcaseService{repo: repo, settings: off}).View(ctx, now)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	require.False(t, view.APIEnabled)
	require.Empty(t, view.Groups)
	_, err = (&PelicanShowcaseService{repo: repo, settings: off}).Item(ctx, 1, now)
	require.ErrorIs(t, err, ErrPelicanShowcaseItemNotFound)

	repo = &showcaseRepoStub{
		groups: []*PelicanShowcaseGroup{{ID: 5, Name: "B"}, {ID: 3, Name: "A"}},
		items: []*PelicanShowcaseItem{
			{ID: 10, GroupID: 3}, {ID: 11, GroupID: 3}, {ID: 12, GroupID: 99},
		},
	}
	apiSettings := enabledShowcase()
	svc := &PelicanShowcaseService{repo: repo, settings: apiSettings}
	view, err = svc.View(ctx, now)
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.True(t, view.APIEnabled)
	require.Equal(t, 12, view.MaxItems)
	require.Equal(t, 3, view.RetentionDays)
	require.Equal(t, []int64{5, 3}, repo.listIDs, "items are only read for groups with a plan that still exist and are active")
	require.Equal(t, now.Add(-72*time.Hour), repo.listSince)
	require.Len(t, view.Groups, 2)
	require.Equal(t, "B", view.Groups[0].Name, "repository order (group sort order) is kept")
	require.NotNil(t, view.Groups[0].Items)
	require.Empty(t, view.Groups[0].Items, "a showcased group without snapshots is still listed")
	require.Len(t, view.Groups[1].Items, 2)

	apiSettings.runtime.APIEnabled = false
	view, err = svc.View(ctx, now)
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.False(t, view.APIEnabled)
	require.Len(t, view.Groups[1].Items, 2, "disabling API reads does not hide the JWT gallery")

	settings := enabledShowcase()
	settings.runtime.Config.AutoCleanup = false
	view, err = (&PelicanShowcaseService{repo: repo, settings: settings}).View(ctx, now)
	require.NoError(t, err)
	require.Zero(t, view.RetentionDays, "retention is reported as 0 when auto cleanup is off")

	_, err = svc.Item(ctx, 10, now)
	require.ErrorIs(t, err, ErrPelicanShowcaseItemNotFound, "a snapshot outside the gallery limits is not served")
	repo.item = &PelicanShowcaseItem{ID: 10, GroupID: 3, ResponseText: "<svg></svg>"}
	item, err := svc.Item(ctx, 10, now)
	require.NoError(t, err)
	require.Equal(t, "<svg></svg>", item.ResponseText)

	require.ErrorIs(t, svc.Remove(ctx, 10), ErrPelicanShowcaseItemNotFound)
	repo.deleted = true
	require.NoError(t, svc.Remove(ctx, 10))
}

func TestScheduledSaveResultNoLongerFeedsTheShowcase(t *testing.T) {
	results := &pelicanResults{}
	svc := NewScheduledTestService(&pelicanPlanRepo{}, results)
	result := &ScheduledTestResult{ID: 91, Status: "success", ResponseText: "<svg></svg>", PelicanConfig: &PelicanTestConfig{ModelID: "gpt-6-astra"}}
	require.NoError(t, svc.SaveResult(context.Background(), 7, 50, result))
	require.Equal(t, 50, results.pruned, "account plans keep their admin history; only group tests publish")
}
