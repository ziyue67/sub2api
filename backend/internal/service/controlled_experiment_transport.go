package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

type controlledExperimentContextKey struct{}
type controlledExperimentMode struct {
	channel       string
	submissions   atomic.Int32
	actualChannel string
	prismIdentity string
	prismTurnID   string
	httpStatus    atomic.Int32
}

func controlledMode(ctx context.Context) *controlledExperimentMode {
	mode, _ := ctx.Value(controlledExperimentContextKey{}).(*controlledExperimentMode)
	return mode
}

func isControlledExperiment(ctx context.Context) bool { return controlledMode(ctx) != nil }

func controlledHTTPResponse(ctx context.Context, response *http.Response) {
	if mode := controlledMode(ctx); mode != nil && response != nil {
		mode.httpStatus.Store(int32(response.StatusCode))
	}
}

// The persistent caller reserves one slot before Forward. Every generation send
// below must pass this guard; compatibility recovery cannot spend a second slot.
func controlledSubmission(ctx context.Context, channel string) error {
	mode := controlledMode(ctx)
	if mode == nil {
		return nil
	}
	if mode.channel != channel {
		return errors.New("controlled experiment channel changed before submission")
	}
	if !mode.submissions.CompareAndSwap(0, 1) {
		return errors.New("controlled experiment blocks an additional submission")
	}
	mode.actualChannel = channel
	return nil
}

func (s *OpenAIGatewayService) controlledRouteReason(ctx context.Context, account *Account, model, channel string) string {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth && account.Type != AccountTypeAPIKey {
		return "unsupported_account_type"
	}
	if account.Status != StatusActive || !account.Schedulable {
		return "account_not_active_or_schedulable"
	}
	if account.IsOpenAIAgentIdentity() || account.IsOpenAIPersonalAccessToken() || account.IsCopilotSDKEnabled() || account.IsOpenAIPassthroughEnabled() {
		return "unsupported_credential_or_adapter"
	}
	if s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(account) {
		return "plugin_route_not_supported"
	}
	prism := account.IsPrismBrowserEnabledForModel(model) && s.prismBrowserGloballyEnabled(ctx)
	bps := account.IsExcelBPSEnabledForModel(model) && s.excelBPSGloballyEnabled(ctx)
	switch channel {
	case "prism":
		if !prism {
			return "prism_not_enabled_for_account_model"
		}
	case "bps":
		if prism || !bps {
			return "bps_not_enabled_for_account_model"
		}
	case "native_http", "native_ws":
		if prism || bps {
			return "account_model_uses_another_adapter"
		}
		if channel == "native_ws" && s.getOpenAIWSProtocolResolver().Resolve(account).Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
			return "websocket_not_enabled"
		}
	default:
		return "unknown_channel"
	}
	return ""
}

func (s *OpenAIGatewayService) checkControlledRoute(ctx context.Context, c *gin.Context, account *Account, bodyModel string) error {
	mode := controlledMode(ctx)
	if mode == nil {
		return nil
	}
	if reason := s.controlledRouteReason(ctx, account, strings.TrimSpace(bodyModel), mode.channel); reason != "" {
		return errors.New(reason)
	}
	if mode.channel == "bps" {
		c.Set(bpsAccountProbeRequiredContextKey, true)
	}
	return nil
}
