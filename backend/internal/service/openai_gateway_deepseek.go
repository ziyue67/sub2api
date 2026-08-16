package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	deepSeekChatCompletionsEndpoint = "/chat/completions"
	deepSeekResponsesEndpoint       = "/responses"
)

// buildDeepSeekEndpointURL appends a native DeepSeek endpoint to the configured
// API root. Unlike the generic OpenAI-compatible builder, it never inserts /v1.
func buildDeepSeekEndpointURL(root, endpoint string) string {
	normalized := strings.TrimSpace(root)
	endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
	parsed, err := url.Parse(normalized)
	if err != nil {
		return strings.TrimRight(normalized, "/") + endpoint
	}

	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, endpoint) {
		path += endpoint
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String()
}

func buildDeepSeekChatCompletionsURL(root string) string {
	return buildDeepSeekEndpointURL(root, deepSeekChatCompletionsEndpoint)
}

func buildDeepSeekResponsesURL(root string) string {
	return buildDeepSeekEndpointURL(root, deepSeekResponsesEndpoint)
}

func (s *OpenAIGatewayService) deepSeekEndpointURL(account *Account, endpoint string) (string, error) {
	if account == nil || !account.IsDeepSeekAPIKey() {
		return "", fmt.Errorf("deepseek endpoint requires a DeepSeek API key account")
	}
	root, err := normalizeDeepSeekBaseURL(account.GetDeepSeekBaseURL())
	if err != nil {
		return "", fmt.Errorf("invalid deepseek base_url: %w", err)
	}
	root, err = s.validateUpstreamBaseURL(root)
	if err != nil {
		return "", fmt.Errorf("invalid deepseek base_url: %w", err)
	}
	switch endpoint {
	case deepSeekChatCompletionsEndpoint:
		return buildDeepSeekChatCompletionsURL(root), nil
	case deepSeekResponsesEndpoint:
		return buildDeepSeekResponsesURL(root), nil
	default:
		return "", fmt.Errorf("unsupported deepseek endpoint: %s", endpoint)
	}
}

type deepSeekResponsesRelayResult struct {
	usage            *OpenAIUsage
	firstTokenMs     *int
	responseID       string
	terminalEvent    string
	clientDisconnect bool
}

func deepSeekResponsesTerminalFromJSON(body []byte) string {
	if eventType := strings.TrimSpace(gjson.GetBytes(body, "type").String()); openAIStreamEventTypeIsTerminal(eventType) {
		return eventType
	}
	switch strings.TrimSpace(gjson.GetBytes(body, "status").String()) {
	case "completed":
		return "response.completed"
	case "incomplete":
		return "response.incomplete"
	case "failed":
		return "response.failed"
	case "cancelled", "canceled":
		return "response.cancelled"
	default:
		return ""
	}
}

func deepSeekResponsesRequiresUsage(terminalEvent string) bool {
	switch terminalEvent {
	case "response.completed", "response.done", "response.incomplete":
		return true
	default:
		return false
	}
}

func (s *OpenAIGatewayService) writeDeepSeekResponsesHeaders(c *gin.Context, resp *http.Response, stream bool) {
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	if requestID := openAICompatibleUpstreamRequestID(resp.Header); requestID != "" {
		c.Writer.Header().Set("x-request-id", requestID)
		if vendorID := strings.TrimSpace(resp.Header.Get("x-deepseek-request-id")); vendorID != "" {
			c.Writer.Header().Set("x-deepseek-request-id", vendorID)
		}
	}
	if stream {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
	} else if strings.TrimSpace(c.Writer.Header().Get("Content-Type")) == "" {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
}

func (s *OpenAIGatewayService) handleDeepSeekResponsesJSON(
	resp *http.Response,
	c *gin.Context,
	account *Account,
) (*deepSeekResponsesRelayResult, error) {
	sanitizeDeepSeekResponseHeadersInPlace(account, resp.Header)
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	body = redactDeepSeekAPIKey(account, body)
	if !gjson.ValidBytes(body) {
		return nil, s.newOpenAIStreamFailoverError(
			c,
			account,
			true,
			openAICompatibleUpstreamRequestID(resp.Header),
			body,
			"DeepSeek Responses returned invalid JSON",
			resp.Header,
		)
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	observer.ObserveOpenAI(body, strings.TrimSpace(gjson.GetBytes(body, "type").String()))

	usage := &OpenAIUsage{}
	if parsed, ok := extractOpenAIUsageFromJSONBytes(body); ok {
		*usage = parsed
	}
	terminalEvent := deepSeekResponsesTerminalFromJSON(body)
	if terminalEvent == "" {
		return nil, s.newOpenAIStreamFailoverError(
			c,
			account,
			true,
			openAICompatibleUpstreamRequestID(resp.Header),
			body,
			"DeepSeek Responses returned JSON without a terminal status or type",
			resp.Header,
		)
	}
	if deepSeekResponsesRequiresUsage(terminalEvent) && !hasBillableOpenAIUsage(*usage) {
		return nil, newDeepSeekMissingUsageFailoverError(c, account, openAICompatibleUpstreamRequestID(resp.Header))
	}

	s.writeDeepSeekResponsesHeaders(c, resp, false)
	c.Data(resp.StatusCode, c.Writer.Header().Get("Content-Type"), body)
	result := &deepSeekResponsesRelayResult{
		usage:         usage,
		responseID:    extractOpenAIResponseIDFromJSONBytes(body),
		terminalEvent: terminalEvent,
	}
	if terminalEvent == "response.failed" {
		return result, errors.New("DeepSeek Responses upstream returned response.failed")
	}
	return result, nil
}

func (s *OpenAIGatewayService) handleDeepSeekResponsesStream(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	startTime time.Time,
) (*deepSeekResponsesRelayResult, error) {
	sanitizeDeepSeekResponseHeadersInPlace(account, resp.Header)
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	usage := &OpenAIUsage{}
	var firstTokenMs *int
	responseID := ""
	terminalEvent := ""
	pendingTerminalEvent := ""
	currentEventType := ""
	clientDisconnected := false
	clientOutputStarted := false
	pendingLine := make([]byte, 0, 64*1024)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	requestID := openAICompatibleUpstreamRequestID(resp.Header)

	result := func() *deepSeekResponsesRelayResult {
		return &deepSeekResponsesRelayResult{
			usage:            usage,
			firstTokenMs:     firstTokenMs,
			responseID:       responseID,
			terminalEvent:    terminalEvent,
			clientDisconnect: clientDisconnected,
		}
	}
	processLine := func(rawLine []byte) bool {
		line := bytes.TrimSuffix(rawLine, []byte{'\r'})
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			atTerminalBoundary := pendingTerminalEvent != ""
			if atTerminalBoundary {
				terminalEvent = pendingTerminalEvent
			}
			pendingTerminalEvent = ""
			currentEventType = ""
			return atTerminalBoundary
		}
		if strings.HasPrefix(trimmed, "event:") {
			currentEventType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			return false
		}
		data, ok := extractOpenAISSEDataLine(string(line))
		if !ok {
			return false
		}
		payload := strings.TrimSpace(data)
		if payload == "" || payload == "[DONE]" {
			return false
		}
		dataBytes := []byte(data)
		eventType := strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
		if eventType == "" {
			eventType = currentEventType
		}
		observer.ObserveOpenAI(dataBytes, eventType)
		s.parseSSEUsageBytes(dataBytes, usage)
		if responseID == "" {
			responseID = extractOpenAIResponseIDFromJSONBytes(dataBytes)
		}
		if firstTokenMs == nil && openAIStreamDataStartsVisibleOutput(payload, eventType) {
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		if openAIStreamEventTypeIsTerminal(eventType) {
			pendingTerminalEvent = eventType
		}
		return false
	}
	missingTerminal := func(readErr error) (*deepSeekResponsesRelayResult, error) {
		message := "DeepSeek Responses stream ended before a terminal event"
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			message = "DeepSeek Responses stream read failed before a terminal event: " + readErr.Error()
		}
		if !clientOutputStarted {
			return nil, s.newOpenAIStreamFailoverError(c, account, true, requestID, nil, message, resp.Header)
		}
		return result(), errors.New(message)
	}
	finishTerminal := func() (*deepSeekResponsesRelayResult, error) {
		if deepSeekResponsesRequiresUsage(terminalEvent) && !hasBillableOpenAIUsage(*usage) {
			_ = newDeepSeekMissingUsageFailoverError(c, account, requestID)
			return result(), errors.New(deepSeekMissingUsageMsg)
		}
		if terminalEvent == "response.failed" {
			return result(), errors.New("DeepSeek Responses upstream returned response.failed")
		}
		return result(), nil
	}
	writeWire := func(wire []byte) {
		if clientDisconnected || len(wire) == 0 {
			return
		}
		wire = redactDeepSeekAPIKey(account, wire)
		if _, writeErr := c.Writer.Write(wire); writeErr != nil {
			clientDisconnected = true
			return
		}
		clientOutputStarted = true
		flusher.Flush()
	}

	s.writeDeepSeekResponsesHeaders(c, resp, true)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			pendingLine = append(pendingLine, chunk...)
			for {
				newline := bytes.IndexByte(pendingLine, '\n')
				if newline < 0 {
					break
				}
				wireLine := pendingLine[:newline+1]
				line := wireLine[:newline]
				pendingLine = pendingLine[newline+1:]
				atTerminalBoundary := processLine(line)
				writeWire(wireLine)
				if atTerminalBoundary {
					return finishTerminal()
				}
			}
			if len(pendingLine) > maxLineSize {
				return result(), fmt.Errorf("DeepSeek Responses SSE line exceeds max size %d", maxLineSize)
			}
		}
		if readErr != nil {
			if len(pendingLine) > 0 {
				_ = processLine(pendingLine)
				writeWire(pendingLine)
			}
			if terminalEvent != "" {
				return finishTerminal()
			}
			if ctx.Err() != nil {
				return result(), fmt.Errorf("DeepSeek Responses stream interrupted: %w", ctx.Err())
			}
			return missingTerminal(readErr)
		}
	}
}

// forwardDeepSeekResponses forwards the native Responses wire without passing
// through OpenAI OAuth/Codex transforms, fast policy, image bridging, or the
// Chat Completions compatibility bridge.
func (s *OpenAIGatewayService) forwardDeepSeekResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if account == nil || !account.IsDeepSeekAPIKey() {
		return nil, fmt.Errorf("deepseek responses requires a DeepSeek API key account")
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("parse DeepSeek Responses request: invalid JSON")
	}

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		return nil, fmt.Errorf("parse DeepSeek Responses request: model is required")
	}
	clientStream := gjson.GetBytes(body, "stream").Bool()
	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = ReplaceModelInBody(body, upstreamModel)
	}

	token := account.GetDeepSeekAPIKey()
	if token == "" {
		return nil, fmt.Errorf("account %d missing api_key", account.ID)
	}
	targetURL, err := s.deepSeekEndpointURL(account, deepSeekResponsesEndpoint)
	if err != nil {
		return nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, deepSeekResponsesEndpoint)

	upstreamStart := time.Now()
	resp, err := s.sendCCUpstreamRequest(ctx, c, account, targetURL, upstreamBody, clientStream, token, "", "")
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	sanitizeDeepSeekResponseHeadersInPlace(account, resp.Header)

	if resp.StatusCode >= http.StatusBadRequest {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		if failoverErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); failoverErr != nil {
			return nil, failoverErr
		}
		return s.handleErrorResponse(ctx, resp, c, account, upstreamBody, billingModel)
	}

	reasoningEffort := extractOpenAIReasoningEffortFromBodyForAccount(account, upstreamBody, upstreamModel, billingModel, originalModel)
	serviceTier := extractOpenAIServiceTierFromBody(upstreamBody)
	var relayResult *deepSeekResponsesRelayResult
	if clientStream {
		relayResult, err = s.handleDeepSeekResponsesStream(ctx, resp, c, account, startTime)
	} else {
		relayResult, err = s.handleDeepSeekResponsesJSON(resp, c, account)
	}
	if relayResult == nil {
		return nil, err
	}
	usage := relayResult.usage
	responseID := strings.TrimSpace(relayResult.responseID)
	s.bindHTTPResponseAccount(ctx, c, account, responseID)
	if usage == nil {
		usage = &OpenAIUsage{}
	}

	result := &OpenAIForwardResult{
		RequestID:                     openAICompatibleUpstreamRequestID(resp.Header),
		ResponseID:                    responseID,
		Usage:                         *usage,
		Model:                         originalModel,
		BillingModel:                  billingModel,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		UpstreamEndpoint:              deepSeekResponsesEndpoint,
		UpstreamTerminalEvent:         relayResult.terminalEvent,
		ServiceTier:                   serviceTier,
		ReasoningEffort:               reasoningEffort,
		Stream:                        clientStream,
		Duration:                      time.Since(startTime),
		FirstTokenMs:                  relayResult.firstTokenMs,
		ClientDisconnect:              relayResult.clientDisconnect,
		ResponseHeaders:               resp.Header.Clone(),
	}
	return result, err
}
