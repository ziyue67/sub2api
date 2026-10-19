package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const groupSubagentFrameCheckKey = "group_subagent_frame_check"
const GroupSubagentsBlockedMessage = "Subagent requests are disabled for this group"
const groupSubagentRejectionKey = "group_subagent_rejection"

// GroupSubagentRejection retains only the denied frame's metadata and original
// employee identity. Prompts and preceding WS turns must not enter the error row.
type GroupSubagentRejection struct {
	APIKey    *service.APIKey
	Model     string
	Stream    bool
	WebSocket bool
}

func GetGroupSubagentRejection(c *gin.Context) (GroupSubagentRejection, bool) {
	value, ok := c.Get(groupSubagentRejectionKey)
	if !ok {
		return GroupSubagentRejection{}, false
	}
	rejection, ok := value.(GroupSubagentRejection)
	return rejection, ok
}

type groupSubagentFrameCheck func([]byte) bool

// GroupBlockSubagents applies to authenticated employee ingress before routing
// or upstream normalization. Internal admin tests and quality jobs do not enter
// this chain and do not require a caller-controlled bypass.
func GroupBlockSubagents() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil || key.Group == nil || !key.Group.BlockSubagents || c.Request == nil {
			c.Next()
			return
		}
		original := c.Copy()
		original.Request = c.Request.Clone(c.Request.Context())
		websocket := isResponsesWebSocketRoute(c)
		check := groupSubagentFrameCheck(func(body []byte) bool {
			if !service.IsCodexSubagentRequest(original, body) {
				return false
			}
			markGroupSubagentRejected(c, original, body, websocket)
			return true
		})
		var body []byte
		if websocket {
			c.Set(groupSubagentFrameCheckKey, check)
		} else if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodDelete {
			var readOK bool
			body, readOK = readAdmissionRequestBody(c)
			if !readOK {
				return
			}
		}
		if check(body) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"type": "permission_error", "code": "subagents_blocked", "message": GroupSubagentsBlockedMessage,
			}})
			return
		}
		c.Next()
	}
}

// CheckGroupSubagentFrame rejects marked first and subsequent WS frames using
// the ingress header snapshot, even after composite routing changes the group.
func CheckGroupSubagentFrame(c *gin.Context, body []byte) bool {
	if c != nil {
		if value, ok := c.Get(groupSubagentFrameCheckKey); ok {
			if check, ok := value.(groupSubagentFrameCheck); ok && check(body) {
				return true
			}
		}
	}
	return false
}

func markGroupSubagentRejected(c, original *gin.Context, body []byte, websocket bool) {
	key, _ := GetAPIKeyFromContext(original)
	payload := body
	if websocket {
		if response := gjson.GetBytes(body, "response"); response.IsObject() {
			payload = []byte(response.Raw)
		}
	}
	model := requestmodel.FromBodyForRoute(original.Request.URL.Path, original.GetHeader("Content-Type"), payload)
	if model == "" {
		model = original.Query("model")
	}
	c.Set(groupSubagentRejectionKey, GroupSubagentRejection{
		APIKey: key, Model: strings.TrimSpace(model),
		Stream: websocket || gjson.GetBytes(payload, "stream").Bool(), WebSocket: websocket,
	})
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	MarkIngressRejected(c, IngressRejectSubagentsBlocked)
}
