package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

type AstraNodeState string

const (
	AstraNodeCandidate AstraNodeState = "candidate"
	AstraNodeReady     AstraNodeState = "ready"
	AstraNodeBackup    AstraNodeState = "backup"
	AstraNodeCooling   AstraNodeState = "cooling"
	AstraNodeExpired   AstraNodeState = "expired"
	AstraNodeRejected  AstraNodeState = "rejected"
)

type AstraNodeTargetCheck struct {
	AccountID                        int64
	Passed                           bool
	CheckedAt, ExpiresAt, RetryAfter time.Time
	Reason                           string
}
type AstraNodeRecord struct {
	ID, CookieValue, CookieName                      string
	SourceAccountID                                  int64
	GatewayHost                                      string
	State                                            AstraNodeState
	AcquiredAt, ExpiresAt, LastUsedAt, LastCheckedAt time.Time
	SourcePassed                                     bool
	SourceReason                                     string
	Failures                                         int
	RetryAfter                                       time.Time
	TargetChecks                                     map[int64]AstraNodeTargetCheck
}

func AstraNodeFingerprint(cookie string) string {
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:8])
}

type AstraNodeStore struct {
	mu      sync.Mutex
	nodes   map[string]*AstraNodeRecord
	primary string
}

func NewAstraNodeStore() *AstraNodeStore {
	return &AstraNodeStore{nodes: map[string]*AstraNodeRecord{}}
}
func (s *AstraNodeStore) Upsert(cookie string, source int64, host string, expires, now time.Time) *AstraNodeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := AstraNodeFingerprint(cookie)
	n := s.nodes[id]
	if n == nil {
		n = &AstraNodeRecord{ID: id, CookieValue: cookie, CookieName: "__oailb", TargetChecks: map[int64]AstraNodeTargetCheck{}}
		s.nodes[id] = n
	}
	n.SourceAccountID = source
	n.GatewayHost = host
	n.AcquiredAt = now
	n.ExpiresAt = expires
	n.SourcePassed = true
	n.SourceReason = "qualified"
	if n.State == AstraNodeRejected {
		n.State = AstraNodeCandidate
	}
	return cloneAstraNode(n)
}
func (s *AstraNodeStore) MarkSource(cookie string, passed bool, reason string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.nodes[AstraNodeFingerprint(cookie)]
	if n == nil {
		return
	}
	n.LastCheckedAt = now
	n.SourcePassed = passed
	n.SourceReason = reason
	if !passed {
		n.State = AstraNodeRejected
		n.Failures++
	}
}
func (s *AstraNodeStore) MarkTarget(cookie string, account int64, passed bool, reason string, checked, expires, retry time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.nodes[AstraNodeFingerprint(cookie)]
	if n == nil {
		return
	}
	n.TargetChecks[account] = AstraNodeTargetCheck{account, passed, checked, expires, retry, reason}
	n.LastCheckedAt = checked
	if passed {
		n.State = AstraNodeReady
	} else {
		n.State = AstraNodeRejected
		n.Failures++
	}
}
func (s *AstraNodeStore) Select(account int64, now time.Time) (*AstraNodeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c []*AstraNodeRecord
	for _, n := range s.nodes {
		if now.After(n.ExpiresAt) || !n.SourcePassed {
			if now.After(n.ExpiresAt) {
				n.State = AstraNodeExpired
			}
			continue
		}
		check, ok := n.TargetChecks[account]
		if !ok || !check.Passed || now.After(check.ExpiresAt) {
			continue
		}
		c = append(c, n)
	}
	sort.Slice(c, func(i, j int) bool {
		if c[i].LastUsedAt.Equal(c[j].LastUsedAt) {
			return c[i].AcquiredAt.Before(c[j].AcquiredAt)
		}
		return c[i].LastUsedAt.Before(c[j].LastUsedAt)
	})
	if len(c) == 0 {
		return nil, false
	}
	n := c[0]
	n.LastUsedAt = now
	s.primary = n.ID
	for _, b := range c[1:] {
		if b.ID != n.ID {
			b.State = AstraNodeBackup
		}
	}
	n.State = AstraNodeReady
	return cloneAstraNode(n), true
}
func (s *AstraNodeStore) Snapshot(now time.Time) []AstraNodeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := make([]AstraNodeRecord, 0, len(s.nodes))
	for _, n := range s.nodes {
		if now.After(n.ExpiresAt) {
			n.State = AstraNodeExpired
		}
		o = append(o, *cloneAstraNode(n))
	}
	sort.Slice(o, func(i, j int) bool { return o[i].ID < o[j].ID })
	return o
}
func cloneAstraNode(n *AstraNodeRecord) *AstraNodeRecord {
	c := *n
	c.TargetChecks = map[int64]AstraNodeTargetCheck{}
	for k, v := range n.TargetChecks {
		c.TargetChecks[k] = v
	}
	return &c
}
