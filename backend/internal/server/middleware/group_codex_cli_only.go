package middleware

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const groupCodexFrameCheckKey = "group_codex_frame_check"

type groupCodexFrameCheck func(body []byte, headersOnly bool) service.CodexClientRestrictionDetectionResult

// GroupCodexCLIOnly checks employee requests after API key authentication and
// before composite routing, account selection or upstream header normalization.
// Admin account tests, IQ tests and quality judging use internal service calls;
// they never enter this middleware and need no caller-controlled bypass header.
func GroupCodexCLIOnly(settings *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil || key.Group == nil || !key.Group.CodexCLIOnly || c.Request == nil {
			c.Next()
			return
		}
		// Preserve the authenticated employee group across composite routing.
		group := *key.Group
		// WS forwarding can add upstream headers between turns. Always identify
		// the employee using the headers received at ingress.
		clientContext := c.Copy()
		clientContext.Request = c.Request.Clone(c.Request.Context())
		check := groupCodexFrameCheck(func(body []byte, headersOnly bool) service.CodexClientRestrictionDetectionResult {
			policy := service.CodexRestrictionPolicy{EngineFingerprintSignals: openai.DefaultEngineFingerprintSignals}
			if settings != nil {
				policy = settings.GetCodexRestrictionPolicy(c.Request.Context())
			}
			if headersOnly {
				// Discovery requests and WS handshakes have no generation body.
				// WS fingerprints are checked on the original response.create frame.
				policy.EngineFingerprintSignals = nil
			}
			return service.DetectCodexGroupClient(clientContext, &group, policy, body)
		})

		var body []byte
		headersOnly := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodDelete
		if isResponsesWebSocketRoute(c) {
			c.Set(groupCodexFrameCheckKey, check)
		} else if !headersOnly {
			var readOK bool
			body, readOK = readAdmissionRequestBody(c)
			if !readOK {
				return
			}
		}
		result := check(body, headersOnly)
		if !result.Matched {
			markGroupCodexRejected(c)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"type": "permission_error", "code": "codex_client_required", "message": GroupCodexClientMessage(result),
			}})
			return
		}
		c.Next()
	}
}

// CheckGroupCodexClientFrame is called before the first and subsequent WS turns
// reach account selection or upstream writes. Only trusted gateway middleware
// can install its checker; request headers and JSON cannot grant an exemption.
func CheckGroupCodexClientFrame(c *gin.Context, body []byte) service.CodexClientRestrictionDetectionResult {
	if c != nil {
		if value, ok := c.Get(groupCodexFrameCheckKey); ok {
			if check, ok := value.(groupCodexFrameCheck); ok {
				result := check(body, false)
				if !result.Matched {
					markGroupCodexRejected(c)
				}
				return result
			}
		}
	}
	return service.CodexClientRestrictionDetectionResult{Reason: service.CodexClientRestrictionReasonDisabled}
}

func GroupCodexClientMessage(result service.CodexClientRestrictionDetectionResult) string {
	if result.Reason == service.CodexClientRestrictionReasonVersionTooLow || result.Reason == service.CodexClientRestrictionReasonVersionTooHigh {
		return service.CodexClientRestrictionMessage(result)
	}
	return "This group only allows Codex clients"
}

func markGroupCodexRejected(c *gin.Context) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	MarkIngressRejected(c, IngressRejectCodexClientRequired)
}
