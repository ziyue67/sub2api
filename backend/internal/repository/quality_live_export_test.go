//go:build integration

package repository

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Test-only bridge lets the external test package run real HTTP handlers over
// the existing isolated fixture without importing handlers into repository.
type QualityLiveFixture struct {
	Plans   service.ScheduledTestPlanRepository
	Plan    *service.ScheduledTestPlan
	Account int64
	Until   time.Time
	Round   func(string, string) string
	Exec    func(string, ...any)
	Count   func(string) int
	Extra   func() map[string]any
}

func NewQualityLiveFixture(t *testing.T) (*QualityLiveFixture, int64, int64) {
	f, group, other := newQualityIsolationFixture(t)
	return &QualityLiveFixture{Plans: f.plans, Plan: f.plan, Account: f.account, Until: f.until, Round: func(sol, astra string) string { return isolationRound(f, sol, astra) }, Exec: f.exec, Count: f.count, Extra: f.extra}, group, other
}
func QualityLiveDB() *sql.DB { return integrationDB }
