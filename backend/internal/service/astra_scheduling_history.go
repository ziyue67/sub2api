package service

import (
	"sync"
	"time"
)

// Small process-local mirror of successful scheduling writes. No credentials.
type AstraSchedulingRecord struct {
	Mode        string    `json:"mode,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	AccountID   int64     `json:"account_id"`
	Schedulable bool      `json:"schedulable"`
	Reason      string    `json:"reason"`
}
type astraSchedulingHistory struct {
	mu   sync.Mutex
	rows []AstraSchedulingRecord
}

var astraRecentScheduling astraSchedulingHistory

func (h *astraSchedulingHistory) add(row AstraSchedulingRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rows = append([]AstraSchedulingRecord{row}, h.rows...)
	if len(h.rows) > 3 {
		h.rows = h.rows[:3]
	}
}
func (h *astraSchedulingHistory) snapshot() []AstraSchedulingRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]AstraSchedulingRecord{}, h.rows...)
}
