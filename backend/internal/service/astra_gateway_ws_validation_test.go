package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type astraValidationAccountRepo struct{ *turnAdmissionRepo }

func (r *astraValidationAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, r.err
}

func TestAstraGatewayWSValidationUsesActualGroup(t *testing.T) {
	gateway, account := anchorTestService()
	account.Status = StatusActive
	account.Schedulable = true
	account.GroupIDs = []int64{11}
	repo := &astraValidationAccountRepo{turnAdmissionRepo: &turnAdmissionRepo{account: account}}
	gateway.accountRepo = repo
	gateway.requireLatestTurnAdmission = true
	gateway.cache = &stubGatewayCache{}
	gateway.toolCorrector = NewCodexToolCorrector()
	gateway.cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	gateway.cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	gateway.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 2
	event := func(id string) []byte {
		return []byte(`{"type":"response.completed","response":{"id":"` + id + `","model":"gpt-6-astra","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"21"}]}],"usage":{"input_tokens":5,"output_tokens":2}}}`)
	}
	conn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.output_text.delta","delta":"21"}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_validation_1","model":"gpt-6-astra","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2}}}`),
		event("resp_validation_2"),
	}}
	dialer := &openAIWSCaptureDialer{conn: conn}
	pool := newOpenAIWSConnPool(gateway.cfg)
	pool.setClientDialerForTest(dialer)
	gateway.openaiWSPool = pool
	t.Cleanup(pool.Close)
	svc := &AccountTestService{accountRepo: repo, cfg: gateway.cfg, openaiGatewayService: gateway}
	question := astraQuestion{Prompt: "Reply with OK only."}
	output, err := svc.testAstraWS(t.Context(), account.ID, 1, question)
	require.NoError(t, err)
	require.Equal(t, "21", output)
	require.Equal(t, 1, dialer.DialCount())
	require.Len(t, conn.writes, 2)
	require.Equal(t, "resp_validation_1", conn.writes[1]["previous_response_id"])
	require.Greater(t, repo.reads, 2, "real admission must run on each send")
	require.Empty(t, gateway.codexWSAnchors.entries, "validation cleans up its session")
}
func TestAstraGatewayWSValidationFailureReasons(t *testing.T) {
	require.Equal(t, "ws_group_membership_changed", astraWSTestFailureReason(&OpenAITurnAdmissionError{Reason: "group_membership_changed"}))
	require.Equal(t, "ws_test_timeout", astraWSTestFailureReason(context.DeadlineExceeded))
	require.Equal(t, "ws_test_failed", astraWSTestFailureReason(errors.New("secret upstream data")))
}

func TestAstraGatewayWSValidationText(t *testing.T) {
	stream := `data: {"type":"response.output_text.delta","delta":"3"}` + "\n\n" + `data: {"type":"response.output_text.delta","delta":"23"}` + "\n\n" + `data: {"type":"response.completed","response":{"output":[]}}` + "\n\n"
	require.Equal(t, "323", astraWSValidationText(stream))
	terminal := `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"323"}]}]}}`
	require.Equal(t, "323", astraWSValidationText(terminal))
	require.Equal(t, "323", astraWSValidationText(stream+terminal))
	require.Empty(t, astraWSValidationText(strings.ReplaceAll(terminal, "323", "")))
}
