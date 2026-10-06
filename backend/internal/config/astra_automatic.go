package config

import "fmt"

// ResolveAstraDependencies returns a detached configuration. WS targets reuse
// qualified HTTP routes; sources remain donors rather than becoming consumers.
func ResolveAstraDependencies(value AstraRoutingSettings) (AstraRoutingSettings, error) {
	if value.CookiePool.RotateNodes {
		value.CookiePool.IPAffinity = true
	}
	value.SchedulingGroupIDs = append([]int64(nil), value.SchedulingGroupIDs...)
	value.CookiePool.SourceAccountIDs = append([]int64(nil), value.CookiePool.SourceAccountIDs...)
	value.CookiePool.TargetAccountIDs = append([]int64(nil), value.CookiePool.TargetAccountIDs...)
	value.WSSession.AccountIDs = append([]int64(nil), value.WSSession.AccountIDs...)
	if value.WSSession.Enabled {
		value.CookiePool.Enabled = true
		sources := map[int64]bool{}
		for _, id := range value.CookiePool.SourceAccountIDs {
			sources[id] = true
		}
		targets := map[int64]bool{}
		for _, id := range value.CookiePool.TargetAccountIDs {
			targets[id] = true
		}
		for _, id := range value.WSSession.AccountIDs {
			if !sources[id] && !targets[id] {
				value.CookiePool.TargetAccountIDs = append(value.CookiePool.TargetAccountIDs, id)
				targets[id] = true
			}
		}
	}
	if value.CookiePool.Enabled && len(value.CookiePool.SourceAccountIDs) == 0 {
		return value, fmt.Errorf("astra_source_required")
	}
	return value, value.Validate()
}
