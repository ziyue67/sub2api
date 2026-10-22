package service

import (
	"context"
	"net/http"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type siwcRequestIdentityKey struct{}
type siwcRequestIdentity struct{ SessionID, RequestID string }

func withSIWCRequestIdentity(req *http.Request, c *gin.Context, account *Account) *http.Request {
	identity := siwcRequestIdentity{RequestID: uuid.NewString()}
	if c != nil && c.Request != nil {
		// Accept only one source. Conflicting aliases or duplicate headers cannot
		// smuggle a second conversation identity through compatibility adapters.
		var raw string
		count := 0
		for _, name := range []string{"Session-Id", "session_id"} {
			for _, value := range c.Request.Header.Values(name) {
				count++
				raw = value
			}
		}
		if count == 1 && validSIWCSessionHeader(raw) {
			identity.SessionID = generateSessionUUID(isolateOpenAIUpstreamSessionID(getAPIKeyIDFromContext(c), account, raw))
		}
	}
	return req.WithContext(context.WithValue(req.Context(), siwcRequestIdentityKey{}, identity))
}

func validSIWCSessionHeader(raw string) bool {
	if len(raw) == 0 || len(raw) > 256 {
		return false
	}
	for _, r := range raw {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func applySIWCRequestHeaders(req *http.Request) {
	authorization := req.Header.Get("Authorization")
	identity, _ := req.Context().Value(siwcRequestIdentityKey{}).(siwcRequestIdentity)
	if identity.RequestID == "" {
		identity.RequestID = uuid.NewString()
	}
	// Reconstruct at the send boundary. Never trust headers that adapters added
	// after building this request, including cookies and other OAuth identities.
	req.Header = make(http.Header)
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "Sub2API ("+runtime.GOOS+"; "+runtime.GOARCH+")")
	req.Header.Set("X-Client-Request-Id", identity.RequestID)
	if strings.TrimSpace(identity.SessionID) != "" {
		req.Header.Set("session_id", identity.SessionID)
	}
}
