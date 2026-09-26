package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const bpsAccountProbeTool = "sub2api_probe_echo"
const bpsAccountProbeRequiredContextKey = "bps_account_probe_required"
const bpsAccountProbeMaxConcurrent = 3

type bpsAccountProbeResponse struct {
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
}

type bpsAccountProbeItem struct {
	Type                  string    `json:"type"`
	Role                  string    `json:"role"`
	Name                  string    `json:"name"`
	Namespace             string    `json:"namespace"`
	CallID                string    `json:"call_id"`
	Arguments             string    `json:"arguments"`
	EncryptedFunctionArgs *[]string `json:"encrypted_function_args"`
	Content               []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (s *AccountTestService) testExcelBPSToolRoundtrip(c *gin.Context, account *Account, modelID string) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	model := strings.TrimSpace(modelID)
	if model == "" {
		model = openai.DefaultTestModel
	}
	if !account.IsExcelBPSEnabledForModel(model) || s.openaiGatewayService == nil {
		return s.sendErrorAndEnd(c, "BPS tool probe requires this model to use an enabled Excel / BPS OAuth account")
	}
	release, ok := s.beginExcelBPSProbe(account.ID)
	if !ok {
		return s.sendErrorAndEnd(c, "BPS tool probe is already running for this account or at capacity")
	}
	defer release()

	nonce := uuid.NewString()
	tool := map[string]any{
		"type": "function", "name": bpsAccountProbeTool,
		"description": "Return the supplied nonce without an external side effect.",
		"parameters": map[string]any{
			"type": "object", "properties": map[string]any{"nonce": map[string]any{"type": "string"}},
			"required": []string{"nonce"}, "additionalProperties": false,
		},
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	textPrompt := "Reply with exactly this nonce and nothing else: " + nonce
	basic, err := s.runExcelBPSProbeStep(c, account, model, nonce, []any{bpsProbeUserMessage(textPrompt)}, nil)
	if err != nil || !bpsProbeExactText(basic, nonce) {
		return s.sendErrorAndEnd(c, "BPS text probe did not return the expected completed response")
	}
	s.sendEvent(c, TestEvent{Type: "status", Text: "BPS text response verified"})

	toolPrompt := "Call " + bpsAccountProbeTool + " exactly once with nonce " + nonce + ". After its result, reply with exactly that nonce."
	input := []any{bpsProbeUserMessage(toolPrompt)}
	called, err := s.runExcelBPSProbeStep(c, account, model, nonce, input, []any{tool})
	if err != nil {
		return s.sendErrorAndEnd(c, "BPS tool call did not complete")
	}
	callID, ok := bpsProbeExactCall(called, nonce)
	if !ok {
		return s.sendErrorAndEnd(c, "BPS did not return exactly one valid echo tool call")
	}
	s.sendEvent(c, TestEvent{Type: "status", Text: "BPS tool call verified"})

	for _, item := range called.Output {
		input = append(input, item)
	}
	input = append(input, map[string]any{"type": "function_call_output", "call_id": callID, "output": nonce})
	finished, err := s.runExcelBPSProbeStep(c, account, model, nonce, input, []any{tool})
	if err != nil || !bpsProbeExactText(finished, nonce) {
		return s.sendErrorAndEnd(c, "BPS tool result did not return the expected completed response")
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *AccountTestService) beginExcelBPSProbe(accountID int64) (func(), bool) {
	s.bpsProbeMu.Lock()
	defer s.bpsProbeMu.Unlock()
	if accountID <= 0 || len(s.bpsProbeAccounts) >= bpsAccountProbeMaxConcurrent {
		return nil, false
	}
	if _, active := s.bpsProbeAccounts[accountID]; active {
		return nil, false
	}
	if s.bpsProbeAccounts == nil {
		s.bpsProbeAccounts = make(map[int64]struct{})
	}
	s.bpsProbeAccounts[accountID] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.bpsProbeMu.Lock()
			delete(s.bpsProbeAccounts, accountID)
			s.bpsProbeMu.Unlock()
		})
	}, true
}

func bpsProbeUserMessage(text string) map[string]any {
	return map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": text},
	}}
}

func (s *AccountTestService) runExcelBPSProbeStep(c *gin.Context, account *Account, model, session string, input any, tools []any) (bpsAccountProbeResponse, error) {
	body := map[string]any{"model": model, "stream": true, "store": false, "input": input,
		"reasoning": map[string]any{"effort": "low"}}
	if tools != nil {
		body["tools"] = tools
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return bpsAccountProbeResponse{}, err
	}
	recorder := httptest.NewRecorder()
	probeCtx, _ := gin.CreateTestContext(recorder)
	probeCtx.Request = c.Request.Clone(c.Request.Context())
	probeCtx.Request.Header.Set("Session-Id", session)
	probeCtx.Set(bpsAccountProbeRequiredContextKey, true)
	result, err := s.openaiGatewayService.Forward(probeCtx, probeCtx, account, raw)
	if err != nil {
		return bpsAccountProbeResponse{}, fmt.Errorf("bps forwarding failed: %w", err)
	}
	if result == nil || result.ClientDisconnect || recorder.Code != http.StatusOK {
		return bpsAccountProbeResponse{}, errors.New("bps forwarding failed")
	}
	if GetActualOpenAIUpstreamEndpoint(probeCtx) != "/basispoints/api/responses" {
		return bpsAccountProbeResponse{}, errors.New("bps probe used another upstream")
	}
	return parseBPSAccountProbeResponse(recorder.Body.Bytes())
}

func parseBPSAccountProbeResponse(wire []byte) (bpsAccountProbeResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(wire))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	var completed *bpsAccountProbeResponse
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return bpsAccountProbeResponse{}, err
		}
		if event.Type == "response.failed" || event.Type == "response.incomplete" {
			return bpsAccountProbeResponse{}, errors.New("bps response failed")
		}
		if event.Type != "response.completed" {
			continue
		}
		if completed != nil {
			return bpsAccountProbeResponse{}, errors.New("duplicate BPS completion")
		}
		var response bpsAccountProbeResponse
		if err := json.Unmarshal(event.Response, &response); err != nil || response.Status != "completed" {
			return bpsAccountProbeResponse{}, errors.New("invalid BPS completion")
		}
		completed = &response
	}
	if err := scanner.Err(); err != nil {
		return bpsAccountProbeResponse{}, err
	}
	if completed == nil {
		return bpsAccountProbeResponse{}, errors.New("missing BPS completion")
	}
	return *completed, nil
}

func bpsProbeExactText(response bpsAccountProbeResponse, nonce string) bool {
	var text strings.Builder
	sawText := false
	for _, raw := range response.Output {
		var item bpsAccountProbeItem
		if json.Unmarshal(raw, &item) != nil {
			return false
		}
		if item.Type == "reasoning" {
			continue
		}
		if item.Type != "message" || item.Role != "assistant" {
			return false
		}
		for _, content := range item.Content {
			if content.Type != "output_text" {
				return false
			}
			sawText = true
			_, _ = text.WriteString(content.Text)
		}
	}
	return sawText && strings.TrimSpace(text.String()) == nonce
}

func bpsProbeExactCall(response bpsAccountProbeResponse, nonce string) (string, bool) {
	callID := ""
	for _, raw := range response.Output {
		var item bpsAccountProbeItem
		if json.Unmarshal(raw, &item) != nil {
			return "", false
		}
		if item.Type == "reasoning" {
			continue
		}
		if item.Type != "function_call" || callID != "" || item.Name != bpsAccountProbeTool || item.Namespace != "" || item.CallID == "" || item.EncryptedFunctionArgs == nil || len(*item.EncryptedFunctionArgs) != 0 {
			return "", false
		}
		var args map[string]json.RawMessage
		if json.Unmarshal([]byte(item.Arguments), &args) != nil || len(args) != 1 {
			return "", false
		}
		var returned string
		if json.Unmarshal(args["nonce"], &returned) != nil || returned != nonce {
			return "", false
		}
		callID = item.CallID
	}
	return callID, callID != ""
}
