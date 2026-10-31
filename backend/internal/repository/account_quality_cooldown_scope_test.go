package repository

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityCooldownActionGuardRetainsAllOwnershipLayouts(t *testing.T) {
	concurrency := 20
	for name, state := range map[string]qualityState{
		"active action":        {Action: service.QualityActionRemoveModel},
		"applied entry":        {ModelApplied: map[string]json.RawMessage{"sol": json.RawMessage(`{}`)}},
		"previous entry":       {ModelRateLimits: map[string]json.RawMessage{"sol": json.RawMessage(`null`)}},
		"previous concurrency": {PreviousConcurrency: &concurrency},
		"applied concurrency":  {AppliedConcurrency: &concurrency},
		"recovery ramp":        {RecoveryTarget: &concurrency},
	} {
		t.Run(name, func(t *testing.T) {
			for _, action := range []string{service.QualityActionObserveOnly, "disable_scheduling", service.QualityActionEnableBPS, service.QualityActionIsolateModels} {
				plan := &service.ScheduledTestPlan{PelicanConfig: &service.PelicanTestConfig{Quality: &service.QualityPolicy{Action: action}}}
				require.Error(t, checkQualityIsolationScope(plan, &state), "cooldown ownership must block action changes: "+action)
				plan.PelicanConfig.Quality.Action = service.QualityActionRemoveModel
				require.NoError(t, checkQualityIsolationScope(plan, &state), "ordinary edits retain the action")
			}
			require.Error(t, checkQualityIsolationScope(&service.ScheduledTestPlan{}, &state))
		})
	}
}
