package service

import (
	"math"
	"strings"
	"time"
)

// Teams recovery uses one paid account window, not a rolling window. A new
// purchase must have a new explicit start/expiry; time never silently resets it.
type PriorityTeamsConfig struct {
	Enabled           bool    `json:"enabled"`
	CostCNY           float64 `json:"cost_cny"`
	WindowHours       int     `json:"window_hours"`
	CNYPerBillingUnit float64 `json:"cny_per_billing_unit"`
	WindowSource      string  `json:"window_source"`
}

type PriorityTeamsWindow struct {
	AccountID int64      `json:"account_id"`
	Start     *time.Time `json:"start_at"`
	End       *time.Time `json:"end_at"`
}

type PriorityTeamsRecovery struct {
	WindowStart               time.Time `json:"window_start"`
	WindowEnd                 time.Time `json:"window_end"`
	RevenueCNY                float64   `json:"revenue_cny"`
	CostCNY                   float64   `json:"cost_cny"`
	ProfitCNY                 float64   `json:"profit_cny"`
	ShortfallCNY              float64   `json:"shortfall_cny"`
	RemainingSeconds          int64     `json:"remaining_seconds"`
	NeedsRecovery             bool      `json:"needs_recovery"`
	RequiredRevenuePerHourCNY float64   `json:"required_revenue_per_hour_cny"`
}

func isPriorityTeamsAccount(a *Account) bool {
	if a == nil || a.IsShadow() || !a.IsOpenAIOAuth() {
		return false
	}
	plan := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(a.GetCredential("plan_type"))))
	switch plan {
	case "team", "teams", "business", "chatgptteam", "chatgptbusiness":
		return true
	}
	return false
}

func priorityTeamsWindows(c PriorityTeamsConfig, accounts []openAIAccountCandidateScore) []PriorityTeamsWindow {
	windows := make([]PriorityTeamsWindow, 0)
	if !c.Enabled {
		return windows
	}
	duration := time.Duration(c.WindowHours) * time.Hour
	for _, item := range accounts {
		a := item.account
		if !isPriorityTeamsAccount(a) {
			continue
		}
		w := PriorityTeamsWindow{AccountID: a.ID}
		switch c.WindowSource {
		case "expiry":
			if a.ExpiresAt == nil {
				continue
			}
			end := *a.ExpiresAt
			start := end.Add(-duration)
			w.Start = &start
			w.End = &end
		case "explicit":
			raw, ok := a.Extra["priority_teams_window_start"].(string)
			if !ok {
				continue
			}
			start, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				continue
			}
			end := start.Add(duration)
			w.Start = &start
			w.End = &end
		case "first_usage": // Repository resolves the earliest recorded request.
		default:
			continue
		}
		windows = append(windows, w)
	}
	return windows
}

func scorePriorityTeams(c PriorityTeamsConfig, signal PrioritySchedulingSignal, now time.Time) *PriorityTeamsRecovery {
	if signal.TeamsWindowStart == nil || signal.TeamsWindowEnd == nil || now.Before(*signal.TeamsWindowStart) || !now.Before(*signal.TeamsWindowEnd) {
		return nil
	}
	revenue := signal.TeamsRevenue * c.CNYPerBillingUnit
	remaining := signal.TeamsWindowEnd.Sub(now).Seconds()
	shortfall := math.Max(0, c.CostCNY-revenue)
	return &PriorityTeamsRecovery{WindowStart: *signal.TeamsWindowStart, WindowEnd: *signal.TeamsWindowEnd, RevenueCNY: revenue, CostCNY: c.CostCNY, ProfitCNY: revenue - c.CostCNY, ShortfallCNY: shortfall, RemainingSeconds: int64(remaining), NeedsRecovery: revenue <= c.CostCNY, RequiredRevenuePerHourCNY: shortfall / math.Max(remaining, 1) * 3600}
}
