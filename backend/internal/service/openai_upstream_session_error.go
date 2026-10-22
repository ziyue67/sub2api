package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// An upstream relay may freeze an individual conversation after a cyber-policy
// rejection. This says nothing about the relay credential's health. Keep this
// distinct from a new cyber_policy hit so local risk counters are not charged
// again for a session that the upstream already blocked.
func isOpenAIUpstreamSessionBlocked(body []byte) bool {
	code := gjson.GetBytes(body, "error.code").String()
	return strings.EqualFold(strings.TrimSpace(code), "session_blocked_by_cyber_policy")
}
