package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const siwcNotEligibleCode = "subscription_sharing_user_not_eligible"
const siwcNotEligibleMessage = "ChatGPT plan usage is unavailable for the selected user, workspace, or policy. Review SIWC eligibility before trying again."

// Only inspect known error objects, never arbitrary nested strings or echoed
// request data. A 502 wrapper must retain structured evidence of the rejection.
func isSIWCEligibilityRejection(account *Account, status int, body []byte) bool {
	if !account.IsSIWCEnabled() || (status != http.StatusForbidden && status != http.StatusBadGateway) {
		return false
	}
	var payload struct {
		Error struct {
			Code          string `json:"code"`
			Type          string `json:"type"`
			Message       string `json:"message"`
			UpstreamError struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"upstream_error"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	e := payload.Error
	if e.Code == siwcNotEligibleCode || e.Type == siwcNotEligibleCode || e.UpstreamError.Code == siwcNotEligibleCode || e.UpstreamError.Type == siwcNotEligibleCode {
		return true
	}
	// Legacy text is accepted only as a complete, unambiguous diagnostic in the
	// error message of a direct 403; it is never treated as evidence of risk flags.
	return status == http.StatusForbidden && strings.EqualFold(strings.TrimSpace(e.Message), "User is not eligible for subscription sharing.")
}

func writeSIWCEligibilityRejection(c *gin.Context, account *Account, resp *http.Response) error {
	MarkResponseCommitted(c)
	setOpsUpstreamError(c, resp.StatusCode, siwcNotEligibleCode, "")
	// Clear a previous attempt's optional detail, even when body logging is on.
	c.Set(OpsUpstreamErrorDetailKey, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"),
		Passthrough: true, Kind: "siwc_eligibility", Message: siwcNotEligibleCode,
	})
	body, _ := json.Marshal(gin.H{"error": gin.H{"type": "upstream_error", "code": siwcNotEligibleCode, "message": siwcNotEligibleMessage}})
	if !writeOpenAICompactSSEBridge(c, http.StatusBadGateway, body) {
		writeOpenAIPassthroughErrorHeaders(c.Writer.Header(), nil)
		c.Data(http.StatusBadGateway, "application/json; charset=utf-8", body)
	}
	return errors.New("SIWC eligibility rejected: " + siwcNotEligibleCode)
}
