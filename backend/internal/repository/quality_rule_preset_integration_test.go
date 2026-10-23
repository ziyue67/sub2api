//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityPresetPersistsWithoutDistributingPlans(t *testing.T) {
	ctx := context.Background()
	repo := NewQualityRuleTemplateRepository(integrationEntClient, integrationDB)
	svc := service.ProvideScheduledTestService(nil, nil, repo, nil)
	var plansBefore, templatesBefore int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_plans`).Scan(&plansBefore))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM quality_rule_templates`).Scan(&templatesBefore))
	saved, err := svc.CreateQualityPreset(ctx, &service.QualityRulePreset{Name: "Persisted template", ModelID: "test-model", CronExpression: "*/30 * * * *", Enabled: true, MaxResults: 80,
		PelicanConfig: &service.PelicanTestConfig{QuestionKind: "candy", Prompt: "Test question", ReasoningEffort: "high", ParallelCount: 2,
			Quality: &service.QualityPolicy{Action: "disable_scheduling", ExpectedAnswer: "42", AutoRestore: true}}})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM quality_rule_presets WHERE id=$1`, saved.ID)
	})
	// A separate repository/service instance must read persisted data, not a cache.
	reloaded := service.ProvideScheduledTestService(nil, nil, NewQualityRuleTemplateRepository(integrationEntClient, integrationDB), nil)
	presets, err := reloaded.ListQualityPresets(ctx)
	require.NoError(t, err)
	var found *service.QualityRulePreset
	for _, preset := range presets {
		if preset.ID == saved.ID {
			found = preset
		}
	}
	require.Equal(t, saved, found)
	require.False(t, saved.CreatedAt.IsZero())
	var plansAfter, templatesAfter int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_plans`).Scan(&plansAfter))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM quality_rule_templates`).Scan(&templatesAfter))
	require.Equal(t, plansBefore, plansAfter)
	require.Equal(t, templatesBefore, templatesAfter)
}
