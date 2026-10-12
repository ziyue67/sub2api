package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const harvestCoverageInterval = 5 * time.Second

// Coverage is sampled by the background worker, never by dashboard requests.
// Only observed intervals count; restart, scope changes and long sampling gaps
// must not invent historical no-ticket time.
type CodexHarvestCoverage struct {
	Accounts              map[int64]CodexHarvestAccountCoverage `json:"-"`
	StartedAt             *time.Time                            `json:"started_at,omitempty"`
	UpdatedAt             *time.Time                            `json:"updated_at,omitempty"`
	AccountCount          int                                   `json:"account_count"`
	AverageReadySeconds   float64                               `json:"average_ready_seconds"`
	AverageMissingSeconds float64                               `json:"average_missing_seconds"`
	AveragePercent        *float64                              `json:"average_percent"`
	SampleIntervalSeconds int                                   `json:"sample_interval_seconds"`
}

type CodexHarvestAccountCoverage struct {
	StartedAt      time.Time `json:"started_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ReadySeconds   float64   `json:"ready_seconds"`
	MissingSeconds float64   `json:"missing_seconds"`
	Percent        *float64  `json:"percent"`
}

type harvestCoverageAccount struct {
	started        time.Time
	last           time.Time
	readyUntil     time.Time
	ready, missing time.Duration
}
type harvestCoverageTracker struct {
	mu               sync.Mutex
	signature        string
	started, updated time.Time
	accounts         map[int64]harvestCoverageAccount
}

func (t *harvestCoverageTracker) observe(now time.Time, signature string, readyUntil map[int64]time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.accounts == nil || signature != t.signature {
		t.accounts = make(map[int64]harvestCoverageAccount)
		t.signature = signature
		t.started = now
	}
	for id := range t.accounts {
		if _, ok := readyUntil[id]; !ok {
			delete(t.accounts, id)
		}
	}
	for id, until := range readyUntil {
		row := t.accounts[id]
		if row.started.IsZero() {
			row.started = now
		}
		elapsed := now.Sub(row.last)
		if !row.last.IsZero() && elapsed > 0 && elapsed <= 3*harvestCoverageInterval {
			end := now
			if row.readyUntil.Before(end) {
				end = row.readyUntil
			}
			ready := end.Sub(row.last)
			if ready < 0 {
				ready = 0
			}
			row.ready += ready
			row.missing += elapsed - ready
		}
		row.last, row.readyUntil = now, until
		t.accounts[id] = row
	}
	t.updated = now
}

func (t *harvestCoverageTracker) snapshot() CodexHarvestCoverage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := CodexHarvestCoverage{SampleIntervalSeconds: int(harvestCoverageInterval / time.Second), Accounts: make(map[int64]CodexHarvestAccountCoverage)}
	if t.started.IsZero() {
		return out
	}
	start, updated := t.started, t.updated
	out.StartedAt, out.UpdatedAt = &start, &updated
	var percent float64
	for id, row := range t.accounts {
		total := row.ready + row.missing
		account := CodexHarvestAccountCoverage{StartedAt: row.started, UpdatedAt: t.updated, ReadySeconds: row.ready.Seconds(), MissingSeconds: row.missing.Seconds()}
		if total > 0 {
			p := 100 * float64(row.ready) / float64(total)
			account.Percent = &p
		}
		out.Accounts[id] = account
		if total <= 0 {
			continue
		}
		out.AccountCount++
		out.AverageReadySeconds += row.ready.Seconds()
		out.AverageMissingSeconds += row.missing.Seconds()
		percent += 100 * float64(row.ready) / float64(total)
	}
	if out.AccountCount > 0 {
		n := float64(out.AccountCount)
		out.AverageReadySeconds /= n
		out.AverageMissingSeconds /= n
		percent /= n
		out.AveragePercent = &percent
	}
	return out
}

func (t *harvestCoverageTracker) interrupt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, row := range t.accounts {
		row.last = time.Time{}
		t.accounts[id] = row
	}
}

func (s *OpenAIGatewayService) observeHarvestCoverage(ctx context.Context) {
	if s.codexHarvest == nil || s.accountRepo == nil {
		return
	}
	observed := false
	defer func() {
		if !observed {
			s.codexHarvest.coverage.interrupt()
		}
	}()
	cfg := s.harvestTicketConfig(ctx)
	scope, err := s.settingService.GetCodexTicketHarvestScope(ctx)
	if err != nil {
		return
	}
	controls, _ := s.harvestControls(ctx)
	// No credentials, account names or ticket state are retained by this tracker.
	enabled := s.openAICodexTicketEnabledContext(ctx)
	signature, err := json.Marshal(struct {
		Enabled      bool
		Models       []string
		Scope        CodexTicketHarvestScope
		Transport    string
		TargetLength int
	}{enabled, cfg.Models, scope, controls.Transport, cfg.TargetLength})
	if err != nil {
		return
	}
	until := make(map[int64]time.Time)
	if enabled {
		accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
		if err != nil {
			return
		}
		now := time.Now()
		for i := range accounts {
			account := &accounts[i]
			if !isOpenAICodexTicketAccount(account) || openAICodexSkipHarvest(account) || !scope.includes(account) || !scope.allowsAccount(account) {
				continue
			}
			var expiry time.Time
			supported := false
			for _, model := range cfg.Models {
				model = normalizeOpenAICodexTicketModel(model)
				if model == "" || !isOpenAICodexTicketAccount(account, model) {
					continue
				}
				supported = true
				ticket := s.lookupOpenAICodexTicket(account, model)
				if ticket.valid(now, openAICodexTicketTargetLength(account, cfg)) && (ticket.Length != 780 || ticket.Transport == controls.Transport) {
					end := openAICodexTicketRemainingUntil(ticket)
					if end.After(expiry) {
						expiry = end
					}
				}
			}
			if supported {
				until[account.ID] = expiry
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.codexHarvest.coverage.observe(time.Now(), string(signature), until)
	observed = true
}

func (s *OpenAIGatewayService) harvestCoverageLoop(ctx context.Context) {
	ticker := time.NewTicker(harvestCoverageInterval)
	defer ticker.Stop()
	for {
		cycle, cancel := context.WithTimeout(ctx, 4*time.Second)
		s.observeHarvestCoverage(cycle)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
