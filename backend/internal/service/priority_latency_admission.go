package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// A local admission rejection. It never implies an upstream failure or permits
// fail-open proxy routing, response-ID deletion, or conversation replay.
var ErrPriorityLatencyUnavailable = errors.New("latency scheduling temporarily unavailable")

func priorityHardStopApplies(c PrioritySchedulingConfig, req OpenAIAccountScheduleRequest) bool {
	return c.LatencyPenaltyMode == PriorityLatencyStop && req.Platform == PlatformOpenAI && req.UseUpstreamTokenCost && req.RequiredImageCapability == "" && priorityApplies(c, req)
}

func priorityLatencyAdmissionError(known bool) error {
	reason := "latency_penalty"
	if !known {
		reason = "latency_history_unavailable"
	}
	return &OpenAITurnAdmissionError{Reason: reason, cause: ErrPriorityLatencyUnavailable}
}

func priorityLatencyStopped(c PrioritySchedulingConfig, item openAIAccountCandidateScore, signal PrioritySchedulingSignal, now time.Time) bool {
	return c.LatencyPenaltyMode == PriorityLatencyStop && slices.Contains(scorePriorityCandidate(c, item, signal, now).Reasons, "latency_above_target")
}

// Hard mode needs an actual aggregate, including an explicitly empty one.
// Pending/unavailable history is a retryable admission failure, never a made-up
// latency measurement. All reads use the existing bounded asynchronous cache.
func (s *OpenAIGatewayService) priorityLatencyAdmission(req OpenAIAccountScheduleRequest, account *Account) error {
	c := s.prioritySchedulingRuntimeConfig()
	if account == nil || !priorityHardStopApplies(c, req) {
		return nil
	}
	pool := []openAIAccountCandidateScore{{account: account}}
	history := s.priorityHistory(req, c, pool)
	signal, known := history.signals[account.ID]
	if !known {
		return priorityLatencyAdmissionError(false)
	}
	if priorityLatencyStopped(c, pool[0], signal, time.Now()) {
		return priorityLatencyAdmissionError(true)
	}
	return nil
}

func priorityLatencyOwnershipBound(req OpenAIAccountScheduleRequest) bool {
	return strings.TrimSpace(req.PreviousResponseID) != "" || req.GuardianParentAccountID > 0 || req.DisableStickyEscape || req.PreserveStickyBinding || req.EncryptedReasoning
}

// Filter before protocol/subscription partitions, so a stopped preferred pool
// cannot hide a usable alternative pool. No stopped candidate enters a WaitPlan.
func (s *defaultOpenAIAccountScheduler) filterPriorityLatencyAccounts(req OpenAIAccountScheduleRequest, accounts []*Account, loads map[int64]*AccountLoadInfo) ([]*Account, error) {
	cfg := s.service.prioritySchedulingRuntimeConfig()
	if !priorityHardStopApplies(cfg, req) {
		return accounts, nil
	}
	pool := make([]openAIAccountCandidateScore, 0, len(accounts))
	for _, account := range accounts {
		pool = append(pool, openAIAccountCandidateScore{account: account, loadInfo: loads[account.ID], loadKnown: loads[account.ID] != nil})
	}
	history := s.service.priorityHistory(req, cfg, pool)
	allowed := make([]*Account, 0, len(accounts))
	knownAll := true
	now := time.Now()
	for i := range pool {
		item := &pool[i]
		signal, known := history.signals[item.account.ID]
		if !known {
			knownAll = false
		}
		item.priorityErrors = s.priorityErrorEvidence(req, cfg, item.account.ID, now)
		applyPriorityCandidate(cfg, item, signal, now)
		if known && !priorityLatencyStopped(cfg, *item, signal, now) {
			allowed = append(allowed, item.account)
		}
	}
	if len(allowed) != len(accounts) {
		s.recordPrioritySnapshot(req, cfg, pool, now)
	}
	if len(allowed) == 0 {
		return nil, priorityLatencyAdmissionError(knownAll)
	}
	return allowed, nil
}

type prioritySelectionRequestKey struct{}
type priorityLatencyTurnKey struct{}

func ownedPriorityScheduleRequest(req OpenAIAccountScheduleRequest) OpenAIAccountScheduleRequest {
	// Retain only identity and feature applicability, never session/response IDs.
	copy := OpenAIAccountScheduleRequest{Platform: req.Platform, RequestedModel: req.RequestedModel, HistoryModel: req.HistoryModel, InboundEndpoint: req.InboundEndpoint, UseUpstreamTokenCost: req.UseUpstreamTokenCost, RequiredImageCapability: req.RequiredImageCapability, RequireCompact: req.RequireCompact}
	if req.GroupID != nil {
		id := *req.GroupID
		copy.GroupID = &id
	}
	return copy
}

// Selection carries the exact public/routing/group identity through slot waits.
func ContextWithSelectionPriorityRequest(ctx context.Context, selection *AccountSelectionResult) context.Context {
	if selection == nil || selection.priorityRequest == nil {
		return ctx
	}
	return context.WithValue(ctx, prioritySelectionRequestKey{}, *selection.priorityRequest)
}
func (s *OpenAIGatewayService) CheckPriorityLatencySelection(ctx context.Context, selection *AccountSelectionResult) error {
	if selection == nil || selection.priorityRequest == nil {
		return nil
	}
	return s.priorityLatencyAdmission(*selection.priorityRequest, selection.Account)
}

// This boundary runs once before a logical turn starts. Nested forwarding and
// retries retain the marker; a penalty learned after output starts cannot abort
// or replay that already admitted turn. A new WS turn explicitly checks again.
func (s *OpenAIGatewayService) beginPriorityLatencyTurn(ctx context.Context, c *gin.Context, account *Account, model string) (context.Context, error) {
	if admitted, ok := ctx.Value(priorityLatencyTurnKey{}).(int64); ok && account != nil && admitted == account.ID {
		return ctx, nil
	}
	req, ok := ctx.Value(prioritySelectionRequestKey{}).(OpenAIAccountScheduleRequest)
	if !ok {
		identityCtx := ctx
		if c != nil && c.Request != nil {
			if _, captured := c.Request.Context().Value(priorityObservationContextKey{}).(priorityObservationIdentity); captured {
				identityCtx = c.Request.Context()
			}
		}
		identity := priorityObservationFromContext(identityCtx, model)
		req = OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: strings.TrimSpace(model), HistoryModel: identity.model, InboundEndpoint: identity.endpoint, UseUpstreamTokenCost: true}
		if account != nil {
			req.Platform = account.Platform
		}
		if identity.hasGroup {
			id := identity.groupID
			req.GroupID = &id
		}
		if forward, found := openAIForwardModelFromContext(identityCtx); found {
			req.RequestedModel = forward.model
		}
		if strings.HasPrefix(identity.endpoint, "/v1/images") || IsGPTImageGenerationModel(model) {
			req.UseUpstreamTokenCost = false
		}
		if c != nil {
			if image, known := getOpenAIImageIntentHint(c); known && image {
				req.UseUpstreamTokenCost = false
			}
		}
	}
	if c != nil && c.Request != nil && strings.EqualFold(c.Request.Header.Get("Upgrade"), "websocket") {
		identity := priorityObservationFromContext(c.Request.Context(), model)
		req.HistoryModel = identity.model
		req.RequestedModel = strings.TrimSpace(model)
		if forward, found := openAIForwardModelFromContext(c.Request.Context()); found {
			req.RequestedModel = forward.model
		}
		if identity.hasGroup {
			id := identity.groupID
			req.GroupID = &id
		}
	}
	if err := s.priorityLatencyAdmission(req, account); err != nil {
		return ctx, err
	}
	if account == nil {
		return ctx, nil
	}
	return context.WithValue(ctx, priorityLatencyTurnKey{}, account.ID), nil
}
