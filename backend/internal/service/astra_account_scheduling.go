package service

import (
	"context"
	"go.uber.org/zap"
	"reflect"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Read existing validation state only: never prepare routes or issue model requests.
func (s *AccountTestService) startAstraAccountScheduling() func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			cycle, stop := context.WithTimeout(ctx, 5*time.Second)
			s.syncAstraAccountScheduling(cycle)
			stop()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

func astraAccountSchedulingReady(settings config.AstraRoutingSettings, snapshot AstraGatewayRuntime, id int64, now time.Time) bool {
	if !settings.CookiePool.Enabled || snapshot.Revision != settings.Revision {
		return false
	}
	for _, row := range snapshot.Targets {
		if row.AccountID == id {
			return row.State == "ready" && row.Reason == "target_probe_passed" && row.ExpiresAt != nil && now.Before(*row.ExpiresAt)
		}
	}
	return false
}

func (s *AccountTestService) syncAstraAccountScheduling(ctx context.Context) {
	settings := s.cfg.AstraRouting(ctx)
	if !settings.AccountScheduling {
		return
	}
	provider, ok := s.httpUpstream.(AstraGatewayRuntimeProvider)
	snapshot := AstraGatewayRuntime{}
	if ok {
		snapshot = provider.AstraGatewaySnapshot(ctx)
	}
	// Serialize writes with local saves; stale work cannot act on removed targets.
	if s.settingService != nil {
		s.settingService.astraRoutingMu.Lock()
		defer s.settingService.astraRoutingMu.Unlock()
		if current := s.settingService.astraRoutingCache; current == nil || !reflect.DeepEqual(*current, settings) {
			return
		}
	}
	for _, id := range settings.CookiePool.TargetAccountIDs {
		if ctx.Err() != nil {
			return
		}
		account, err := s.accountRepo.GetByID(ctx, id)
		if err != nil || account == nil {
			continue
		}
		ready := astraAccountSchedulingReady(settings, snapshot, id, time.Now())
		ready = ready && account.IsOpenAIOAuthLike() && account.Status == StatusActive && account.ParentAccountID == nil && (account.ExpiresAt == nil || time.Now().Before(*account.ExpiresAt))
		fields := astraSchedulingLogFields(settings, snapshot, account, ready, time.Now())
		action, err := applyAstraScheduling(ctx, s.accountRepo, account, settings, ready)
		if err != nil {
			code := "write_failed"
			switch err.Error() {
			case "astra_restore_conflict", "configuration_changed", "astra_model_mapping_invalid":
				code = err.Error()
			}
			logger.L().Warn("astra_account_scheduling_update_failed", append(fields, zap.String("action_error", code), zap.String("mode", settings.EffectiveSchedulingMode()))...)
			continue
		}
		if !action.Changed {
			continue
		}
		fields = append(fields, zap.String("mode", action.Action))
		logger.L().Info("astra_account_scheduling_changed", fields...)
		reason := "target_not_verified"
		for _, field := range fields {
			if field.Key == "reason" {
				reason = field.String
			}
		}
		astraRecentScheduling.add(AstraSchedulingRecord{CheckedAt: time.Now(), AccountID: id, Schedulable: ready, Reason: reason, Mode: action.Action})
	}
}

// Only explicit non-secret fields: no account credentials, cookies, proxy URLs
// or upstream errors. unchanged states never call this logger.
func astraSchedulingLogFields(settings config.AstraRoutingSettings, snapshot AstraGatewayRuntime, account *Account, ready bool, now time.Time) []zap.Field {
	reason := "target_not_verified"
	gateway := ""
	var expiry *time.Time
	for _, row := range snapshot.Targets {
		if row.AccountID == account.ID {
			gateway, expiry = row.Gateway, row.ExpiresAt
			if row.Reason != "" {
				reason = row.Reason
			}
			if row.State != "ready" && reason == "target_probe_passed" {
				reason = "target_not_verified"
			}
			break
		}
	}
	if ready {
		reason = "target_probe_passed"
	} else if !settings.CookiePool.Enabled {
		reason = "cookie_pool_disabled"
	} else if snapshot.Revision != settings.Revision {
		reason = "configuration_changed"
	} else if !account.IsOpenAIOAuthLike() || account.Status != StatusActive || account.ParentAccountID != nil || (account.ExpiresAt != nil && !now.Before(*account.ExpiresAt)) {
		reason = "account_ineligible"
	} else if expiry != nil && !now.Before(*expiry) {
		reason = "route_expired"
	}
	remaining := int64(0)
	fields := []zap.Field{zap.Int64("account_id", account.ID), zap.Bool("previous_schedulable", account.Schedulable), zap.Bool("schedulable", ready), zap.String("reason", reason), zap.String("gateway", gateway), zap.String("revision", settings.Revision), zap.String("trigger", "astra_state_sync")}
	if expiry != nil {
		fields = append(fields, zap.Time("route_expires_at", *expiry))
		if now.Before(*expiry) {
			remaining = int64(expiry.Sub(now).Seconds())
		}
	}
	return append(fields, zap.Int64("route_remaining_seconds", remaining))
}
