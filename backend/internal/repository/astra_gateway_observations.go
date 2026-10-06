package repository

import (
	"slices"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Observations describe the routing host claimed by a Cookie, never a verified
// compute node. They contain no Cookie values and reset with the pool revision.
// All callers hold pool.mu. Keep history bounded even for long-lived revisions.
const maxAstraObservedGateways = 256

func (s *codexGatewayPinUpstream) observeGatewaySource(host string, id int64, passed bool, now time.Time) {
	if !astraGatewayHostPattern.MatchString(host) {
		s.unknownGatewaySamples++
		return
	}
	if s.gateways == nil {
		s.gateways = make(map[string]*service.AstraGatewayObservation)
	}
	row := s.gateways[host]
	if row == nil {
		if len(s.gateways) >= maxAstraObservedGateways {
			oldest := ""
			for key, candidate := range s.gateways {
				if oldest == "" || candidate.LastSeen.Before(s.gateways[oldest].LastSeen) {
					oldest = key
				}
			}
			delete(s.gateways, oldest)
		}
		row = &service.AstraGatewayObservation{Gateway: host, SourceAccountIDs: []int64{}}
		s.gateways[host] = row
	} else {
		row.RepeatedHits++
	}
	row.Samples++
	row.LastSeen = now
	if !slices.Contains(row.SourceAccountIDs, id) {
		row.SourceAccountIDs = append(row.SourceAccountIDs, id)
		slices.Sort(row.SourceAccountIDs)
	}
	if passed {
		row.SourcePasses++
	} else {
		row.SourceFailures++
	}
}

func (s *codexGatewayPinUpstream) observeGatewayTarget(host string, passed bool) {
	if row := s.gateways[host]; row != nil {
		if passed {
			row.TargetPasses++
		} else {
			row.TargetFailures++
		}
	}
}

func (s *codexGatewayPinUpstream) gatewayObservations() []service.AstraGatewayObservation {
	rows := make([]service.AstraGatewayObservation, 0, len(s.gateways))
	for _, row := range s.gateways {
		copy := *row
		copy.SourceAccountIDs = slices.Clone(row.SourceAccountIDs)
		rows = append(rows, copy)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Gateway < rows[j].Gateway })
	return rows
}
