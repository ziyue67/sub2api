package service

import (
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// OAuthInitialQualityRule is a saved copy, independent of its source account.
// It deliberately excludes account IDs, runtime state and test results.
type OAuthInitialQualityRule struct {
	ModelID        string             `json:"model_id"`
	CronExpression string             `json:"cron_expression"`
	Enabled        bool               `json:"enabled"`
	MaxResults     int                `json:"max_results"`
	PelicanConfig  *PelicanTestConfig `json:"pelican_config"`
}

func (r *OAuthInitialQualityRule) plan(platform string) (*ScheduledTestPlan, error) {
	if r == nil {
		return nil, nil
	}
	bad := func(message string) (*ScheduledTestPlan, error) {
		return nil, infraerrors.BadRequest("AUTO_CONFIG_QUALITY_INVALID", message)
	}
	if r.PelicanConfig == nil || r.PelicanConfig.Quality == nil {
		return bad("select a quality operations rule")
	}
	if isOpenAICodexStateProbePlan(r.PelicanConfig) && platform != PlatformOpenAI {
		return bad("state probe rules require OpenAI OAuth accounts")
	}
	p := &ScheduledTestPlan{ModelID: r.ModelID, CronExpression: r.CronExpression,
		Enabled: r.Enabled, MaxResults: r.MaxResults, PelicanConfig: cloneQualityPelicanConfig(r.PelicanConfig)}
	p.PelicanConfig.TriggerSource = ""
	next, err := nextPlanRun(p, time.Now())
	if err != nil {
		return bad(err.Error())
	}
	p.NextRunAt = &next
	return p, nil
}
