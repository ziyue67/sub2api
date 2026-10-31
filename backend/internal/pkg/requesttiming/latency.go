package requesttiming

import "math"

// Measurement distinguishes a measured zero from an unavailable boundary.
type Measurement struct {
	MS     *float64 `json:"ms"`
	Status string   `json:"status"`
}

type AttemptLatency struct {
	Number       int         `json:"number"`
	Transition   string      `json:"transition"`
	Upload       Measurement `json:"body_upload"`
	SemanticWait Measurement `json:"post_upload_first_content_wait"`
	VisibleWait  Measurement `json:"post_upload_first_visible_wait"`
	ResponseWait Measurement `json:"post_upload_response_wait"`
	RequestBytes int64       `json:"request_bytes"`
	Failed       bool        `json:"failed"`
}

type LatencySummary struct {
	Version                int              `json:"version"`
	Status                 string           `json:"status"`
	Attempts               []AttemptLatency `json:"attempts"`
	AccountSwitches        int              `json:"account_switches"`
	SameAccountRetries     int              `json:"same_account_retries"`
	UnknownTransitions     int              `json:"unknown_transitions"`
	LargeBody              bool             `json:"large_body"`
	ExtraUploadComponentMS *float64         `json:"extra_upload_component_ms"`
	ExtraWaitComponentMS   *float64         `json:"extra_wait_component_ms"`
	CostStatus             string           `json:"cost_status"`
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func attemptInterval(a Attempt, start, end string) Measurement {
	s, sok := a.Events[start]
	e, eok := a.Events[end]
	if !sok || !eok {
		return Measurement{Status: "not_collected"}
	}
	if !finite(s) || !finite(e) || !finite(a.StartMS) || a.StartMS < 0 || s < a.StartMS || e < s ||
		a.EndMS == nil || !finite(*a.EndMS) || e > *a.EndMS {
		return Measurement{Status: "invalid_boundary"}
	}
	v := e - s
	return Measurement{MS: &v, Status: "measured"}
}

// DeriveLatency uses only physical HTTP attempts, never the request-wide event
// map or enclosing egress parents. Component sums are not elapsed wall time.
// It also supports the #599 request_written name and the wrote_request alias.
func DeriveLatency(s Snapshot) LatencySummary {
	out := LatencySummary{Version: 1, Status: "measured", Attempts: []AttemptLatency{}, CostStatus: "not_applicable", LargeBody: s.BodyBytes > 1<<20}
	var previous *Attempt
	var upload, wait float64
	costComplete, overlap := true, false
	parents := make(map[int]bool)
	for _, a := range s.Attempts {
		if a.Kind == "http" && a.Parent > 0 {
			parents[a.Parent] = true
		}
	}
	unobservedEgress := false
	for _, a := range s.Attempts {
		if a.Kind == "egress" && !parents[a.Number] {
			unobservedEgress = true
		}
	}
	for i := range s.Attempts {
		a := &s.Attempts[i]
		if a.Kind != "http" {
			continue
		}
		if a.RequestBytes > 1<<20 {
			out.LargeBody = true
		}
		written := "request_written"
		if _, ok := a.Events[written]; !ok {
			written = "wrote_request"
		}
		item := AttemptLatency{Number: a.Number, Transition: "initial", RequestBytes: a.RequestBytes, Failed: a.Error != "" || a.Status >= 400 || a.Terminal == "failed" || a.Terminal == "incomplete"}
		item.Upload = attemptInterval(*a, "headers_written", written)
		if _, failed := a.Events["request_write_failed"]; failed {
			item.Upload = Measurement{Status: "write_failed"}
		}
		item.SemanticWait = attemptInterval(*a, written, "first_semantic")
		item.VisibleWait = attemptInterval(*a, written, "first_visible")
		// Network EOF can precede parsing buffered SSE data. Parser timestamps
		// remain bounded by the completed request, not the transport read EOF.
		if a.EndMS != nil && finite(*a.EndMS) && finite(s.TotalMS) && s.TotalMS > *a.EndMS {
			parsed := *a
			end := s.TotalMS
			parsed.EndMS = &end
			item.SemanticWait = attemptInterval(parsed, written, "first_semantic")
			item.VisibleWait = attemptInterval(parsed, written, "first_visible")
		}
		item.ResponseWait = attemptInterval(*a, written, "response_headers")
		if item.Upload.Status != "measured" {
			item.SemanticWait = Measurement{Status: item.Upload.Status}
			item.VisibleWait = Measurement{Status: item.Upload.Status}
			item.ResponseWait = Measurement{Status: item.Upload.Status}
		}
		if previous != nil {
			switch {
			case previous.AccountID <= 0 || a.AccountID <= 0:
				item.Transition = "unknown"
				out.UnknownTransitions++
			case previous.AccountID == a.AccountID:
				item.Transition = "same_account_retry"
				out.SameAccountRetries++
			default:
				item.Transition = "account_switch"
				out.AccountSwitches++
			}
			if previous.EndMS == nil || !finite(*previous.EndMS) || !finite(a.StartMS) {
				costComplete = false
			} else if a.StartMS < *previous.EndMS {
				overlap = true
			}
			if item.Upload.MS == nil {
				costComplete = false
			} else {
				upload += *item.Upload.MS
			}
			// Failed attempts have no first content: response-header wait remains
			// a distinct, measurable network/upstream response component.
			w := item.SemanticWait.MS
			if w == nil && a.Status >= 400 {
				w = item.ResponseWait.MS
			}
			if w == nil {
				costComplete = false
			} else {
				wait += *w
			}
		}
		out.Attempts = append(out.Attempts, item)
		previous = a
	}
	if len(out.Attempts) == 0 {
		out.Status = "not_collected"
	}
	if s.Truncated || unobservedEgress || overlap || out.UnknownTransitions > 0 {
		out.Status = "partial"
	}
	if out.LargeBody && len(out.Attempts) > 1 {
		switch {
		case overlap:
			out.CostStatus = "overlap"
		case !costComplete || s.Truncated || unobservedEgress || out.UnknownTransitions > 0:
			out.CostStatus = "partial"
		default:
			out.CostStatus = "measured_components"
			out.ExtraUploadComponentMS, out.ExtraWaitComponentMS = &upload, &wait
		}
	}
	return out
}
