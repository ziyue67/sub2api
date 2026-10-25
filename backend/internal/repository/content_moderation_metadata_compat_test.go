package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestContentModerationRepositoryOptionalMetadataCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta *service.ContentModerationEngineMeta
		wire driver.Value
		read *service.ContentModerationEngineMeta
	}{
		{name: "legacy-null"},
		{
			name: "legacy-zero-value",
			meta: &service.ContentModerationEngineMeta{},
			wire: `{"engine":"","model":"","rules_version":"","skipped_images":0}`,
			read: &service.ContentModerationEngineMeta{},
		},
		{
			name: "legacy-empty-optional-fields",
			meta: &service.ContentModerationEngineMeta{
				Engine: "typesafe", Model: "jev-fixed", RulesVersion: "rules-v1", SkippedImages: 1,
				CategorySettings: map[string]service.ContentModerationCategorySetting{}, MatchedCategories: []string{},
			},
			wire: `{"engine":"typesafe","model":"jev-fixed","rules_version":"rules-v1","skipped_images":1}`,
			read: &service.ContentModerationEngineMeta{Engine: "typesafe", Model: "jev-fixed", RulesVersion: "rules-v1", SkippedImages: 1},
		},
		{
			name: "category-and-match-snapshot",
			meta: &service.ContentModerationEngineMeta{
				Engine: "typesafe", Model: "jev-fixed", RulesVersion: "content-audit-15-zh-v2",
				CategorySettings: map[string]service.ContentModerationCategorySetting{
					"jailbreak": {Enabled: true}, "control_bypass": {Enabled: false},
				},
				MatchedCategories: []string{"jailbreak"},
			},
			wire: `{"category_settings":{"control_bypass":{"enabled":false},"jailbreak":{"enabled":true}},"matched_categories":["jailbreak"],"engine":"typesafe","model":"jev-fixed","rules_version":"content-audit-15-zh-v2","skipped_images":0}`,
			read: &service.ContentModerationEngineMeta{
				Engine: "typesafe", Model: "jev-fixed", RulesVersion: "content-audit-15-zh-v2",
				CategorySettings: map[string]service.ContentModerationCategorySetting{
					"jailbreak": {Enabled: true}, "control_bypass": {Enabled: false},
				},
				MatchedCategories: []string{"jailbreak"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			args := make([]driver.Value, 27)
			for i := range args {
				args[i] = sqlmock.AnyArg()
			}
			args[15] = `{"control_bypass":1,"jailbreak":0.9}`
			args[16] = `{"control_bypass":0.8,"jailbreak":0.8}`
			args[25] = tc.wire
			now := time.Now()
			mock.ExpectQuery("INSERT INTO content_moderation_logs").WithArgs(args...).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(554, now))
			repo := NewContentModerationRepository(db)
			log := &service.ContentModerationLog{
				EngineMeta:        tc.meta,
				CategoryScores:    map[string]float64{"jailbreak": 0.9, "control_bypass": 1},
				ThresholdSnapshot: map[string]float64{"jailbreak": 0.8, "control_bypass": 0.8},
			}
			require.NoError(t, repo.CreateLog(context.Background(), log))
			require.Equal(t, int64(554), log.ID)
			mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			columns := []string{"id", "request_id", "user_id", "user_email", "api_key_id", "api_key_name", "group_id", "group_name", "endpoint", "provider", "model", "mode", "action", "flagged", "highest_category", "highest_score", "category_scores", "threshold_snapshot", "input_excerpt", "upstream_latency_ms", "error", "violation_count", "auto_banned", "email_sent", "status", "queue_delay_ms", "matched_keyword", "created_at", "engine_meta", "evidence_status"}
			mock.ExpectQuery("SELECT[\\s\\S]*l.engine_meta").WillReturnRows(sqlmock.NewRows(columns).AddRow(554, "req", nil, "", nil, "", nil, "", "/v1/responses", "business", "gpt-model", "pre_block", "block", true, "control_bypass", 1.0, args[15], args[16], "fixture", 10, "", 0, false, false, "active", nil, "", now, tc.wire, ""))
			logs, _, err := repo.ListLogs(context.Background(), service.ContentModerationLogFilter{})
			require.NoError(t, err)
			require.Len(t, logs, 1)
			require.Equal(t, tc.read, logs[0].EngineMeta)
			require.Equal(t, log.CategoryScores, logs[0].CategoryScores)
			require.Equal(t, log.ThresholdSnapshot, logs[0].ThresholdSnapshot)
			require.NoError(t, mock.ExpectationsWereMet())
			mock.ExpectClose()
			require.NoError(t, db.Close())
		})
	}
}
