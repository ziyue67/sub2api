package service

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

type bpsTestResponseObserverKey struct{}
type bpsTestResponseObserver func(TestEvent)

var bpsTestDiagnosticIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,160}$`)

// This observer exists only on an administrator's explicit test request.
func (s *AccountTestService) withBPSTestEvidence(ctx context.Context, c *gin.Context) context.Context {
	return context.WithValue(ctx, bpsTestResponseObserverKey{}, bpsTestResponseObserver(func(event TestEvent) { s.sendEvent(c, event) }))
}

func observeBPSTestResponse(ctx context.Context, resp *http.Response, model, token, code string) {
	observer, ok := ctx.Value(bpsTestResponseObserverKey{}).(bpsTestResponseObserver)
	if !ok || resp == nil {
		return
	}
	clean := func(value string) string {
		value = strings.TrimSpace(value)
		if !bpsTestDiagnosticIdentifier.MatchString(value) || (token != "" && strings.Contains(value, token)) {
			return ""
		}
		return value
	}
	observer(TestEvent{Type: "upstream_response", UpstreamStatus: resp.StatusCode, UpstreamModel: clean(model), RequestID: clean(resp.Header.Get("x-request-id")), UpstreamErrorCode: clean(code)})
}
