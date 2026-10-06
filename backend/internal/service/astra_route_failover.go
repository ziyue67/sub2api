package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// Route admission failures have no user output. Evict the target and let the
// handler select another account rather than committing a terminal response.
func (s *OpenAIGatewayService) astraRouteFailover(ctx context.Context, account *Account, err error) error {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return err
	}
	disable := true
	switch err.Error() {
	case "astra_rotation_unavailable", "astra_rotation_use_once", "astra_rotation_no_nodes", "astra_rotation_node_unavailable", "astra_rotation_exhausted", "astra_rotation_cooling", "target_probe_degraded", "target_route_changed", "target_probe_failed", "target_not_verified", "no_qualified_source_route", "source_probe_cooldown", "astra_route_not_ready":
	case "target_validation_in_progress", "preparation_in_progress", "configuration_changed":
		disable = false // Another request/configuration may already have a valid route.
	default:
		return err
	}
	settings := s.cfg.AstraRouting(ctx)
	if account == nil || (!settings.CookiePool.Enabled && !settings.AccountScheduling) || !slices.Contains(settings.CookiePool.TargetAccountIDs, account.ID) {
		return err
	}
	if disable && settings.AccountScheduling && s.accountRepo != nil {
		action, writeErr := applyAstraScheduling(ctx, s.accountRepo, account, settings, false)
		if writeErr != nil {
			logger.L().Warn("astra_account_scheduling_update_failed", zap.Int64("account_id", account.ID), zap.Bool("schedulable", false), zap.String("reason", err.Error()), zap.String("trigger", "request_failover"), zap.String("mode", settings.EffectiveSchedulingMode()))
		} else if action.Changed {
			astraRecentScheduling.add(AstraSchedulingRecord{CheckedAt: time.Now(), AccountID: account.ID, Schedulable: false, Reason: err.Error(), Mode: action.Action})
			logger.L().Info("astra_account_scheduling_changed", zap.Int64("account_id", account.ID), zap.Bool("previous_schedulable", true), zap.Bool("schedulable", false), zap.String("reason", err.Error()), zap.String("revision", settings.Revision), zap.String("trigger", "request_failover"), zap.String("mode", settings.EffectiveSchedulingMode()))
		}
	}
	return &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: []byte(`{"error":{"type":"upstream_error","message":"No verified Astra route available"}}`)}
}

func (s *OpenAIGatewayService) checkAstraSchedulingRoute(ctx context.Context, account *Account) error {
	settings := s.cfg.AstraRouting(ctx)
	if !settings.AccountScheduling || account == nil || !slices.Contains(settings.CookiePool.TargetAccountIDs, account.ID) {
		return nil
	}
	snapshot := AstraGatewayRuntime{}
	if provider, ok := s.httpUpstream.(AstraGatewayRuntimeProvider); ok {
		snapshot = provider.AstraGatewaySnapshot(ctx)
	}
	if astraAccountSchedulingReady(settings, snapshot, account.ID, time.Now()) {
		return nil
	}
	return s.astraRouteFailover(ctx, account, errors.New("astra_route_not_ready"))
}
