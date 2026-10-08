package service

import "fmt"

func buildControlledReport(run *ControlledExperiment, attempts []*ControlledAttempt, preflight []ControlledPreflight) *ControlledExperimentReport {
	report := &ControlledExperimentReport{Run: run, Attempts: attempts, Preflight: preflight, Routes: make([]ControlledRouteSummary, len(run.Spec.Routes)), Comparisons: make([]ControlledComparison, 0)}
	final := make([]map[string]*ControlledAttempt, len(report.Routes))
	score := make([]float64, len(final))
	for i := range final {
		final[i] = map[string]*ControlledAttempt{}
		report.Routes[i] = ControlledRouteSummary{RouteIndex: i, Eligibility: "untested"}
	}
	for _, p := range preflight {
		if p.RouteIndex >= 0 && p.RouteIndex < len(final) && !p.Available {
			report.Routes[p.RouteIndex].Eligibility = "preflight_blocked"
		}
	}
	for _, a := range attempts {
		if a.RouteIndex < 0 || a.RouteIndex >= len(final) {
			continue
		}
		r := &report.Routes[a.RouteIndex]
		r.Calls++
		switch a.Status {
		case "completed":
			r.CompletedCalls++
		case "unknown", "reserved":
			r.UnknownCalls++
		default:
			r.ProtocolFailures++
		}
		if a.Diagnostic.CostUSD != nil {
			r.CostUSD += *a.Diagnostic.CostUSD
		}
		r.CostIncomplete = r.CostIncomplete || a.Diagnostic.CostIncomplete || a.Status == "unknown" || a.Status == "reserved"
		if a.Phase == "eligibility" {
			r.Eligibility = "failed"
			if a.Status == "completed" && a.Grade != nil && a.Grade.Passed {
				r.Eligibility = "verified"
			}
			continue
		}
		key := fmt.Sprintf("%s/%d", a.TaskID, a.Repetition)
		final[a.RouteIndex][key] = a
		if a.Status == "completed" && a.Grade != nil {
			r.GradedTasks++
			score[a.RouteIndex] += a.Grade.Score
			if a.Grade.Passed {
				r.PassedTasks++
			}
		}
	}
	for i := range final {
		r := &report.Routes[i]
		if r.GradedTasks > 0 {
			mean := score[i] / float64(r.GradedTasks)
			r.MeanScore = &mean
		}
	}
	for left := 0; left < len(final); left++ {
		for right := left + 1; right < len(final); right++ {
			comparison := ControlledComparison{Left: left, Right: right}
			var lsum, rsum float64
			for _, task := range run.Spec.Tasks {
				for repeat := 1; repeat <= run.Spec.Repetitions; repeat++ {
					key := fmt.Sprintf("%s/%d", task.ID, repeat)
					l, r := final[left][key], final[right][key]
					if !controlledComparable(l, r) {
						comparison.ExcludedTasks++
						continue
					}
					comparison.PairedTasks++
					lsum += l.Grade.Score
					rsum += r.Grade.Score
				}
			}
			if comparison.PairedTasks > 0 {
				lmean, rmean := lsum/float64(comparison.PairedTasks), rsum/float64(comparison.PairedTasks)
				difference := rmean - lmean
				comparison.LeftMean = &lmean
				comparison.RightMean = &rmean
				comparison.MeanDifference = &difference
			}
			report.Comparisons = append(report.Comparisons, comparison)
		}
	}
	return report
}

func controlledComparable(left, right *ControlledAttempt) bool {
	if left == nil || right == nil || left.Status != "completed" || right.Status != "completed" || left.Grade == nil || right.Grade == nil {
		return false
	}
	l, r := left.Diagnostic, right.Diagnostic
	return l.IdentityStable && r.IdentityStable && l.ResponseModel != "" && l.ResponseModel == r.ResponseModel &&
		l.EffectiveEffort != "" && l.EffectiveEffort == r.EffectiveEffort && l.EffortEvidence == "upstream_declared" && r.EffortEvidence == "upstream_declared"
}
