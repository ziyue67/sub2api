//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/stretchr/testify/require"
)

func TestIssue594ReviewManagedCodexRejectsUnboundLegacyTicket(t *testing.T) {
	a := ticketTestAccount(41)
	a.AuthConnectionID = "synthetic-connection"
	a.AuthConnectionRevision = 7
	attachReadyCodexTicket(a, "gpt-6-astra")
	s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"gpt-6-astra"}}, nil)
	require.Nil(t, s.lookupOpenAICodexTicket(a, "gpt-6-astra"), "a managed grant must never inherit a legacy ticket with empty identity")
}
func TestIssue594ReviewManagedCodexIdentityChangesWithClientAndRevision(t *testing.T) {
	first := ticketTestAccount(41)
	first.AuthConnectionID = "synthetic-connection"
	first.AuthConnectionRevision = 7
	first.Credentials["client_id"] = "client-one"
	changed := *first
	changed.AuthConnectionRevision = 8
	require.NotEqual(t, ticketIdentity(first), ticketIdentity(&changed), "reauthorization or rotation must invalidate the old ticket identity")
	changed = *first
	changed.Credentials = shallowCopyMap(first.Credentials)
	changed.Credentials["client_id"] = "client-two"
	require.NotEqual(t, ticketIdentity(first), ticketIdentity(&changed), "client registrations cannot share ticket identity")
}
func TestIssue594ReviewBPSCannotAdvertiseBrokenBridges(t *testing.T) {
	caps := connectionCapabilities("bps", "openid offline_access")
	require.NotContains(t, caps, "chat_completions", "BPS may only advertise bridges that its real forwarding paths implement")
}
func TestIssue594ReviewBPSAttachmentFailsClosedWithoutGrantFence(t *testing.T) {
	calls := 0
	s := &OpenAIGatewayService{httpUpstream: &nativeAttachmentUpstream{do: func(*http.Request, string, int64, int) (*http.Response, error) { calls++; return nil, context.Canceled }}}
	a := excelAccount()
	a.AuthConnectionID = "synthetic-managed-bps"
	a.AuthConnectionRevision = 1
	_, err := s.uploadExcelBPSAttachment(context.Background(), a, "synthetic-token", "synthetic-workspace", "", basispoints.InlineAttachment{})
	require.Error(t, err)
	require.Zero(t, calls, "attachment egress must require a live connection fence")
}
