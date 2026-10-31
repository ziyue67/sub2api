package service

func (s *defaultOpenAIAccountScheduler) shouldEscapeStickyForRequest(req OpenAIAccountScheduleRequest, id int64, cfg openAIStickyEscapeConfig) (string, float64, float64, bool) {
	if !req.ObservationEffortScoped {
		return s.shouldEscapeStickyAccount(id, cfg)
	}
	if !cfg.enabled || s.stats == nil {
		return "", 0, 0, false
	}
	e := s.stats.attempts.snapshot(priorityRequestAttemptKey(req, id), s.stats.currentTime(), 10, 5)
	if e.LatencySamples >= 5 {
		ttft := e.LatencyTotalMS / float64(e.LatencySamples)
		if ttft > cfg.ttftMs {
			return "ttft", e.SmoothedRate, ttft, true
		}
	}
	if e.Successes+e.Failures >= 5 && e.SmoothedRate > cfg.errorRate {
		return "error_rate", e.SmoothedRate, 0, true
	}
	return "", 0, 0, false
}
