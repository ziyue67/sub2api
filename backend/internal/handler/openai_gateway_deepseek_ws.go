package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type deepSeekResponsesWSTurnState struct {
	requestedModel  string
	upstreamModel   string
	channelMapping  service.ChannelMappingResult
	requestBodyHash string
	cyberBlockKey   string
	compositeRoute  service.CompositeRouteDecision
}

func deepSeekResponsesWSScheduleOutcome(result *service.OpenAIForwardResult, forwardErr, turnContextErr error) (bool, bool) {
	if turnContextErr != nil {
		return false, false
	}
	if forwardErr != nil {
		if service.IsDeepSeekWSAccountNeutralError(forwardErr) {
			return false, false
		}
		var clientCloseErr *service.OpenAIWSClientCloseError
		if errors.As(forwardErr, &clientCloseErr) {
			return false, false
		}
		return true, false
	}
	if result == nil {
		return false, false
	}
	return true, openAIForwardSucceededForScheduling(result)
}

func (h *OpenAIGatewayHandler) responsesDeepSeekWebSocket(
	c *gin.Context,
	connectionCtx context.Context,
	clientLifecycleCtx context.Context,
	wsConn *coderws.Conn,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	reqLog *zap.Logger,
	firstMessage []byte,
	initialModel string,
	clientIP string,
	userAgent string,
) {
	bridgeEnabled := service.DeepSeekResponsesWSHTTPBridgeEnabled(h.cfg)
	if !bridgeEnabled {
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "DeepSeek Responses WebSocket HTTP bridge is disabled")
		return
	}
	if err := service.ValidateDeepSeekAuthenticatedUserContext(connectionCtx); err != nil {
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "authenticated user context is required")
		return
	}

	setOpsRequestContext(c, initialModel, true)
	setOpsEndpointContext(c, "", int16(service.RequestTypeWSV2))
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	connectionSeed := openAIWSIngressFallbackSessionSeed(subject.UserID, apiKey.ID, apiKey.GroupID)

	var stateMu sync.Mutex
	turnStates := make(map[int]deepSeekResponsesWSTurnState)
	cyberBlockedThisConn := false

	storeTurnState := func(turn int, state deepSeekResponsesWSTurnState) {
		stateMu.Lock()
		turnStates[turn] = state
		stateMu.Unlock()
	}
	loadTurnState := func(turn int) (deepSeekResponsesWSTurnState, bool) {
		stateMu.Lock()
		defer stateMu.Unlock()
		state, ok := turnStates[turn]
		return state, ok
	}
	deleteTurnState := func(turn int) {
		stateMu.Lock()
		delete(turnStates, turn)
		stateMu.Unlock()
	}

	hooks := &service.DeepSeekWSIngressHooks{
		ClientLifecycleContext: clientLifecycleCtx,
		BeforeRequest: func(turn int, payload []byte, originalModel string) ([]byte, error) {
			// A previous turn installs a cancellable context on the shared Gin
			// request. Always start policy processing from the connection context.
			c.Request = c.Request.WithContext(connectionCtx)
			c.Set(securityAuditWSTurnContextKey, turn)
			service.MarkDeepSeekCompaction(c, service.DeepSeekCompactionModeNone)

			if !gjson.ValidBytes(payload) {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", errors.New("invalid JSON"))
			}
			if err := service.ValidateDeepSeekUserIdentityRequest(payload, service.DeepSeekUserIdentityResponses); err != nil {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid DeepSeek Responses request", err)
			}

			requestedModel := strings.TrimSpace(originalModel)
			if requestedModel == "" {
				requestedModel = strings.TrimSpace(gjson.GetBytes(payload, "model").String())
			}
			if requestedModel == "" {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "model is required in response.create payload", nil)
			}

			decision, targetPlatform, err := resolveResponsesWebSocketTarget(c, apiKey, connectionCtx, requestedModel)
			if err != nil {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "Responses WebSocket model route could not be resolved", err)
			}
			if targetPlatform != service.PlatformDeepSeek {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "model switch requires reconnect", errOpenAIWSUnsupportedModelSwitch)
			}

			upstreamModel := requestedModel
			if decision.Matched && strings.TrimSpace(decision.UpstreamModel) != "" {
				upstreamModel = strings.TrimSpace(decision.UpstreamModel)
			}
			mappedPayload := payload
			if upstreamModel != requestedModel {
				mappedPayload = service.ReplaceModelInBody(mappedPayload, upstreamModel)
			}
			channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(connectionCtx, apiKey.GroupID, upstreamModel)
			if channelMapping.Mapped && strings.TrimSpace(channelMapping.MappedModel) != "" {
				upstreamModel = strings.TrimSpace(channelMapping.MappedModel)
				mappedPayload = service.ReplaceModelInBody(mappedPayload, upstreamModel)
			}

			restoredPayload, _, err := h.gatewayService.RestoreDeepSeekCompactInputForTarget(connectionCtx, mappedPayload, service.PlatformDeepSeek)
			if err != nil {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid DeepSeek compact encrypted_content", err)
			}
			// ForwardDeepSeekResponsesWebSocketTurn may now skip its defensive
			// restore scan. Every turn reaches the strict validator above first.
			service.MarkDeepSeekResponsesInputValidated(c)

			// WS ingress is streaming even when response.create omits stream. Set it
			// before classifying legacy/remote-v2 compaction, then let the service
			// strip the envelope fields after this callback.
			classificationPayload, setErr := sjson.SetBytes(restoredPayload, "stream", true)
			if setErr != nil {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", setErr)
			}
			markDeepSeekRemoteCompactionV2Request(c, reqLog, classificationPayload, service.PlatformDeepSeek)

			stage := "subsequent_turn"
			if turn == 1 {
				stage = "first_turn"
			}
			if cyberBlockedThisConn {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, cyberSessionBlockedClientMsg, nil)
			}
			cyberBlockKey := service.CyberSessionBlockKey(apiKey.ID, c, restoredPayload)
			if cyberBlockKey != "" && h.gatewayService.IsCyberSessionBlocked(connectionCtx, cyberBlockKey) {
				writeCyberSessionBlockedWSError(connectionCtx, wsConn)
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "session blocked by cyber-security policy", nil)
			}
			if auditDecision := h.checkSecurityAuditStage(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIResponses, requestedModel, restoredPayload, stage); auditDecision != nil && !auditDecision.AllowNextStage {
				writeSecurityAuditWSError(connectionCtx, wsConn, auditDecision)
				return nil, service.NewOpenAIWSClientCloseError(securityAuditWSCloseStatus(auditDecision), securityAuditWSCloseReason(auditDecision), nil)
			}

			storeTurnState(turn, deepSeekResponsesWSTurnState{
				requestedModel:  requestedModel,
				upstreamModel:   upstreamModel,
				channelMapping:  channelMapping,
				requestBodyHash: service.HashUsageRequestPayload(restoredPayload),
				cyberBlockKey:   cyberBlockKey,
				compositeRoute:  decision,
			})
			return classificationPayload, nil
		},
		ExecuteTurn: func(turnCtx context.Context, turn int, payload []byte, writeClientMessage func([]byte) error) (*service.OpenAIForwardResult, error) {
			state, ok := loadTurnState(turn)
			if !ok {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusInternalError, "DeepSeek WebSocket turn state is missing", nil)
			}
			defer deleteTurnState(turn)
			defer clearCyberPolicyTurnState(c)

			effectiveTurnCtx := turnCtx
			if state.compositeRoute.Matched {
				effectiveTurnCtx = service.WithCompositeRouteDecision(effectiveTurnCtx, state.compositeRoute)
			}
			c.Request = c.Request.WithContext(effectiveTurnCtx)
			defer func() { c.Request = c.Request.WithContext(connectionCtx) }()

			if err := h.billingCacheService.CheckBillingEligibility(effectiveTurnCtx, apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(effectiveTurnCtx, apiKey)); err != nil {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "billing check failed", err)
			}

			userRelease, userAcquired, err := h.concurrencyHelper.TryAcquireUserSlotForAPIKey(effectiveTurnCtx, subject.UserID, subject.Concurrency, apiKey.ID)
			if err != nil {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusInternalError, "failed to acquire user concurrency slot", err)
			}
			if !userAcquired {
				return nil, service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "too many concurrent requests, please retry later", nil)
			}
			if userRelease != nil {
				defer userRelease()
			}

			pricingCtx, pricingAt := h.gatewayService.WithOpenAITurnPricingContext(effectiveTurnCtx, apiKey.GroupID)
			sessionHash := h.gatewayService.GenerateSessionHashWithFallback(c, payload, connectionSeed)
			failedAccountIDs := make(map[int64]struct{})
			maxSwitches := h.maxAccountSwitches
			if maxSwitches < 0 {
				maxSwitches = 0
			}
			switchCount := 0
			profitVetoCount := 0
			disabledAccountSeen := false
			var lastFailoverErr *service.UpstreamFailoverError

			for {
				if err := effectiveTurnCtx.Err(); err != nil {
					return nil, err
				}
				selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
					pricingCtx,
					apiKey.GroupID,
					"",
					sessionHash,
					state.upstreamModel,
					failedAccountIDs,
					service.OpenAIUpstreamTransportHTTPSSE,
					service.OpenAIEndpointCapabilityResponses,
					false,
					true,
					true,
					service.PlatformDeepSeek,
				)
				if err != nil || selection == nil || selection.Account == nil {
					if disabledAccountSeen && lastFailoverErr == nil {
						return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "websocket mode is disabled for available DeepSeek accounts", err)
					}
					if lastFailoverErr != nil {
						return nil, service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "DeepSeek upstream is temporarily unavailable", lastFailoverErr)
					}
					return nil, service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "no available DeepSeek account", err)
				}

				account := selection.Account
				accountRelease := selection.ReleaseFunc
				if account.ResolveDeepSeekResponsesWebSocketMode(bridgeEnabled) != service.DeepSeekResponsesWebSocketModeHTTPBridge {
					if accountRelease != nil {
						accountRelease()
					}
					disabledAccountSeen = true
					failedAccountIDs[account.ID] = struct{}{}
					continue
				}

				admissionCtx := service.ContextWithSelectionProfitGate(pricingCtx, selection)
				if !selection.Acquired {
					if selection.WaitPlan == nil {
						failedAccountIDs[account.ID] = struct{}{}
						continue
					}
					var accountAcquired bool
					accountRelease, accountAcquired, err = h.concurrencyHelper.TryAcquireAccountSlot(admissionCtx, account.ID, selection.WaitPlan.MaxConcurrency)
					if err != nil {
						return nil, service.NewOpenAIWSClientCloseError(coderws.StatusInternalError, "failed to acquire account concurrency slot", err)
					}
					if !accountAcquired {
						failedAccountIDs[account.ID] = struct{}{}
						continue
					}
				}

				latestAccount, vetoed, reason := h.gatewayService.ProfitControlVetoLatest(admissionCtx, account)
				if vetoed {
					if accountRelease != nil {
						accountRelease()
					}
					reqLog.Debug("deepseek.websocket_account_slot_profit_vetoed", zap.Int("turn", turn), zap.Int64("account_id", account.ID), zap.String("reason", reason))
					if !recordOpenAIProfitVeto(failedAccountIDs, account.ID, &profitVetoCount) {
						return nil, service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "no eligible DeepSeek account", nil)
					}
					continue
				}
				account = latestAccount
				if account.ResolveDeepSeekResponsesWebSocketMode(bridgeEnabled) != service.DeepSeekResponsesWebSocketModeHTTPBridge {
					if accountRelease != nil {
						accountRelease()
					}
					disabledAccountSeen = true
					failedAccountIDs[account.ID] = struct{}{}
					continue
				}

				setOpsSelectedAccount(c, account.ID, account.Platform)
				if err := h.gatewayService.BindStickySessionAfterProfitAdmission(admissionCtx, apiKey.GroupID, sessionHash, account.ID); err != nil {
					reqLog.Warn("deepseek.websocket_bind_sticky_session_failed", zap.Int("turn", turn), zap.Int64("account_id", account.ID), zap.Error(err))
				}
				token, _, credentialErr := h.gatewayService.GetRequestCredential(admissionCtx, c, account)
				if credentialErr != nil {
					if accountRelease != nil {
						accountRelease()
					}
					var failoverErr *service.UpstreamFailoverError
					if errors.As(credentialErr, &failoverErr) && failoverErr.ShouldRetryNextAccount() && switchCount < maxSwitches {
						if failoverErr.ShouldReportAccountScheduleFailure() {
							h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, account.GetMappedModel(state.upstreamModel), false, nil)
						}
						h.gatewayService.RecordOpenAIAccountSwitch()
						failedAccountIDs[account.ID] = struct{}{}
						lastFailoverErr = failoverErr
						switchCount++
						continue
					}
					return nil, credentialErr
				}

				reqLog.Debug("deepseek.websocket_account_selected",
					zap.Int("turn", turn),
					zap.Int64("account_id", account.ID),
					zap.String("schedule_layer", scheduleDecision.Layer),
					zap.Int("candidate_count", scheduleDecision.CandidateCount),
				)
				c.Request = c.Request.WithContext(admissionCtx)
				result, forwardErr := h.gatewayService.ForwardDeepSeekResponsesWebSocketTurn(admissionCtx, c, account, token, payload, turn, writeClientMessage)
				if accountRelease != nil {
					accountRelease()
				}

				var failoverErr *service.UpstreamFailoverError
				if !service.IsDeepSeekWSAccountNeutralError(forwardErr) && errors.As(forwardErr, &failoverErr) {
					if failoverErr.ShouldReportAccountScheduleFailure() {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, account.GetMappedModel(state.upstreamModel), false, nil)
					}
					if failoverErr.ShouldRetryNextAccount() && switchCount < maxSwitches && effectiveTurnCtx.Err() == nil {
						h.gatewayService.RecordOpenAIAccountSwitch()
						failedAccountIDs[account.ID] = struct{}{}
						lastFailoverErr = failoverErr
						switchCount++
						continue
					}
					return result, forwardErr
				}

				actualUpstreamModel := strings.TrimSpace(account.GetMappedModel(state.upstreamModel))
				if result != nil {
					if strings.TrimSpace(result.UpstreamModel) != "" {
						actualUpstreamModel = strings.TrimSpace(result.UpstreamModel)
					}
					result.BillingModel = openAIWSTurnBillingModel(result, state.channelMapping, state.requestedModel, actualUpstreamModel)
				}
				quotaPlatform := service.QuotaPlatform(admissionCtx, apiKey)
				turnUsageFields := state.channelMapping.ToUsageFields(state.requestedModel, actualUpstreamModel)
				h.recordCyberPolicyIfMarkedWithUsage(c, apiKey, account, subscription, state.requestedModel, forwardErr != nil, state.cyberBlockKey, turnUsageFields, state.requestBodyHash, result, quotaPlatform, pricingAt)
				if service.GetOpsCyberPolicy(c) != nil {
					cyberBlockedThisConn = true
				}

				if reportSchedule, scheduleSucceeded := deepSeekResponsesWSScheduleOutcome(result, forwardErr, effectiveTurnCtx.Err()); reportSchedule {
					var firstTokenMs *int
					if scheduleSucceeded && result != nil {
						firstTokenMs = result.FirstTokenMs
					}
					h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, actualUpstreamModel, scheduleSucceeded, firstTokenMs)
				}

				if result != nil && result.HasBillableTokenUsage() && service.GetOpsCyberPolicy(c) == nil {
					inboundEndpoint := GetInboundEndpoint(c)
					upstreamEndpoint := resolveOpenAIUpstreamEndpoint(c, account, result)
					sessionID := service.ExtractClientSessionID(c)
					usageFields := turnUsageFields
					h.submitOpenAIUsageRecordTask(admissionCtx, result, func(recordCtx context.Context) {
						if err := h.gatewayService.RecordUsage(recordCtx, &service.OpenAIRecordUsageInput{
							Result:             result,
							APIKey:             apiKey,
							User:               apiKey.User,
							Account:            account,
							Subscription:       subscription,
							InboundEndpoint:    inboundEndpoint,
							UpstreamEndpoint:   upstreamEndpoint,
							UserAgent:          userAgent,
							IPAddress:          clientIP,
							RequestPayloadHash: state.requestBodyHash,
							APIKeyService:      h.apiKeyService,
							QuotaPlatform:      quotaPlatform,
							SessionID:          sessionID,
							ChannelUsageFields: usageFields,
							PricingAt:          pricingAt,
							CyberBlocked:       false,
						}); err != nil {
							reqLog.Error("deepseek.websocket_record_usage_failed", zap.Int("turn", turn), zap.Int64("account_id", account.ID), zap.Error(err))
						}
					})
				}
				return result, forwardErr
			}
		},
	}

	err := h.gatewayService.ProxyDeepSeekResponsesWebSocket(connectionCtx, c, wsConn, firstMessage, hooks)
	if err == nil {
		reqLog.Info("deepseek.websocket_ingress_closed")
		return
	}
	if errors.Is(context.Cause(connectionCtx), service.ErrOpenAIWSIngressLeaseLost) {
		closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "websocket ingress capacity lease lost; please reconnect")
		return
	}
	var closeErr *service.OpenAIWSClientCloseError
	if errors.As(err, &closeErr) {
		closeOpenAIClientWS(wsConn, closeErr.StatusCode(), closeErr.Reason())
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	reqLog.Warn("deepseek.websocket_proxy_failed", zap.Error(fmt.Errorf("DeepSeek Responses WebSocket bridge: %w", err)))
	closeOpenAIClientWS(wsConn, coderws.StatusInternalError, "DeepSeek Responses WebSocket bridge failed")
}
