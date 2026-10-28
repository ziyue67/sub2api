package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const qualityRulePresetColumns = `id, name, model_id, cron_expression, enabled, max_results, pelican_config, created_at, updated_at`

func scanQualityRulePreset(row scannable) (*service.QualityRulePreset, error) {
	preset := &service.QualityRulePreset{}
	var config []byte
	if err := row.Scan(&preset.ID, &preset.Name, &preset.ModelID, &preset.CronExpression,
		&preset.Enabled, &preset.MaxResults, &config, &preset.CreatedAt, &preset.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(config, &preset.PelicanConfig); err != nil {
		return nil, err
	}
	return preset, nil
}

func (r *qualityRuleTemplateRepository) ListPresets(ctx context.Context) ([]*service.QualityRulePreset, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+qualityRulePresetColumns+` FROM quality_rule_presets ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	presets := []*service.QualityRulePreset{}
	for rows.Next() {
		preset, err := scanQualityRulePreset(rows)
		if err != nil {
			return nil, err
		}
		presets = append(presets, preset)
	}
	return presets, rows.Err()
}

func (r *qualityRuleTemplateRepository) CreatePreset(ctx context.Context, preset *service.QualityRulePreset) (*service.QualityRulePreset, error) {
	if err := validateQualityGroupReferences(ctx, r.db, preset.PelicanConfig); err != nil {
		return nil, err
	}
	return scanQualityRulePreset(r.db.QueryRowContext(ctx, `
		INSERT INTO quality_rule_presets (name, model_id, cron_expression, enabled, max_results, pelican_config)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+qualityRulePresetColumns,
		preset.Name, preset.ModelID, preset.CronExpression, preset.Enabled, preset.MaxResults, marshalPelicanConfig(preset.PelicanConfig)))
}

func (r *qualityRuleTemplateRepository) UpdatePreset(ctx context.Context, preset *service.QualityRulePreset) (*service.QualityRulePreset, error) {
	if err := validateQualityGroupReferences(ctx, r.db, preset.PelicanConfig); err != nil {
		return nil, err
	}
	saved, err := scanQualityRulePreset(r.db.QueryRowContext(ctx, `UPDATE quality_rule_presets SET name=$2,model_id=$3,
 cron_expression=$4,enabled=$5,max_results=$6,pelican_config=$7,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond')
 WHERE id=$1 RETURNING `+qualityRulePresetColumns, preset.ID, preset.Name, preset.ModelID, preset.CronExpression, preset.Enabled, preset.MaxResults, marshalPelicanConfig(preset.PelicanConfig)))
	if err == sql.ErrNoRows {
		return nil, infraerrors.NotFound("QUALITY_PRESET_NOT_FOUND", "preset not found")
	}
	return saved, err
}
