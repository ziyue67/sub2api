package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	deepSeekCompactionModeKey     = "deepseek_compaction_bridge_mode"
	deepSeekCompactPreparedKey    = "deepseek_remote_compaction_prepared"
	deepSeekResponsesValidatedKey = "deepseek_responses_input_validated"

	deepSeekCompactEnvelopePrefix      = "sub2api.deepseek.compact.v1."
	deepSeekCompactCipherDomain        = "sub2api/deepseek-remote-compaction/v1"
	deepSeekCompactMaxSummaryBytes     = 128 * 1024
	deepSeekCompactMaxEnvelopeBytes    = 256 * 1024
	deepSeekCompactMaxSSEBytes         = 8 * 1024 * 1024
	deepSeekCompactMaxSSELineBytes     = 1024 * 1024
	deepSeekCompactSummaryMaxTokens    = 8192
	deepSeekCompactReasoningEffort     = "max"
	deepSeekCompactEnvelopeVersion     = 1
	deepSeekResponsesMaxJSONDepth      = 256
	deepSeekCompactInvalidStateMessage = "invalid DeepSeek compact encrypted_content"
)

type DeepSeekCompactionMode string

const (
	DeepSeekCompactionModeNone          DeepSeekCompactionMode = ""
	DeepSeekCompactionModeRemoteV2SSE   DeepSeekCompactionMode = "remote_v2_sse"
	DeepSeekCompactionModeLegacyBodySSE DeepSeekCompactionMode = "legacy_body_sse"
	DeepSeekCompactionModeLegacyUnary   DeepSeekCompactionMode = "legacy_unary"
)

var (
	ErrDeepSeekCompactInvalidEncryptedContent = errors.New(deepSeekCompactInvalidStateMessage)
	ErrDeepSeekResponsesDuplicateJSONKey      = errors.New("DeepSeek Responses request contains duplicate JSON object keys")
	ErrDeepSeekResponsesNonCanonicalJSONKey   = errors.New("DeepSeek Responses request contains non-canonical JSON object keys")
	ErrDeepSeekCompactRequestTooLarge         = errors.New("DeepSeek Responses request exceeds the supported size")
)

const deepSeekCompactCheckpointPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

var deepSeekCompactInstruction = strings.Join([]string{
	"You are now acting as a compaction engine for this AI coding assistant. Condense the conversation ABOVE into a structured checkpoint that lets another model resume the work with no loss of essential context.",
	"",
	"Output EXACTLY the Markdown structure below: keep every section, in order. Use terse bullets, not prose paragraphs. Write \"(none)\" for an empty section — never drop a section.",
	"",
	"## Primary Request and Intent",
	"- [the user's original and evolving goals; quote verbatim where the exact wording matters]",
	"",
	"## Key Technical Concepts",
	"- [technologies, frameworks, patterns, and conventions in play]",
	"",
	"## Files and Code",
	"- [exact path: why it matters, key changes or snippets]",
	"",
	"## Errors and Fixes",
	"- [error: how it was resolved, plus any related user feedback]",
	"",
	"## Pending Jobs",
	"- [explicitly requested work not yet completed]",
	"",
	"## Current Work",
	"- [precisely what was in progress at this checkpoint]",
	"",
	"## Next Step",
	"- [the single next action, directly in line with the most recent request, or \"(none)\"]",
	"",
	"## Critical Context",
	"- [decisions and their rationale, constraints, user preferences, open questions, data needed to continue]",
	"",
	"Rules:",
	"- Write concise English engineering prose. Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.",
	"- Capture user feedback and explicit instructions faithfully, especially corrections.",
	"- Do NOT mention this summarization request or that the context was compacted.",
	"- Output only the checkpoint text: do not call any tool or take any other action.",
	"- If the conversation already contains a <compacted-summary> block, it is a PRIOR checkpoint. Do not copy it forward verbatim: preserve still-true facts, drop stale ones, and merge newer information into a single consolidated summary under the same structure.",
}, "\n")

type deepSeekCompactEnvelope struct {
	Version    int    `json:"version"`
	Checkpoint string `json:"checkpoint"`
}

type deepSeekCompactStreamResult struct {
	Summary        string
	Usage          OpenAIUsage
	TotalTokens    int
	ResponseID     string
	FirstTokenMs   *int
	Completed      bool
	UpstreamFailed bool
}

type deepSeekCompactPreparedRequest struct {
	SourceHash     [32]byte
	ChatBody       []byte
	CompactedBytes int
}

// MarkDeepSeekCompaction records a server-validated bridge decision. The mode
// also preserves the client wire: remote/body streaming clients receive SSE,
// while legacy /responses/compact clients receive unary Responses JSON.
func MarkDeepSeekCompaction(c *gin.Context, mode DeepSeekCompactionMode) {
	if c == nil {
		return
	}
	switch mode {
	case DeepSeekCompactionModeRemoteV2SSE, DeepSeekCompactionModeLegacyBodySSE:
		c.Set(deepSeekCompactionModeKey, mode)
		MarkOpenAICompactClientStream(c)
	case DeepSeekCompactionModeLegacyUnary:
		c.Set(deepSeekCompactionModeKey, mode)
	}
}

// MarkDeepSeekRemoteCompactionV2 keeps the existing call site contract for the
// current Codex remote_compaction_v2 streaming wire.
func MarkDeepSeekRemoteCompactionV2(c *gin.Context) {
	MarkDeepSeekCompaction(c, DeepSeekCompactionModeRemoteV2SSE)
}

func DeepSeekCompactionModeFromContext(c *gin.Context) DeepSeekCompactionMode {
	if c == nil {
		return DeepSeekCompactionModeNone
	}
	value, _ := c.Get(deepSeekCompactionModeKey)
	mode, _ := value.(DeepSeekCompactionMode)
	return mode
}

func IsDeepSeekCompactionMarked(c *gin.Context) bool {
	return DeepSeekCompactionModeFromContext(c) != DeepSeekCompactionModeNone
}

func IsDeepSeekRemoteCompactionV2Marked(c *gin.Context) bool {
	return IsDeepSeekCompactionMarked(c)
}

// MarkDeepSeekResponsesInputValidated avoids repeating the strict input scan
// after the handler has restored gateway-owned state before policy inspection.
func MarkDeepSeekResponsesInputValidated(c *gin.Context) {
	if c != nil {
		c.Set(deepSeekResponsesValidatedKey, true)
	}
}

func IsDeepSeekResponsesInputValidated(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, _ := c.Get(deepSeekResponsesValidatedKey)
	validated, _ := value.(bool)
	return validated
}

func frameDeepSeekCompactSummary(summary string) string {
	return deepSeekCompactCheckpointPreamble + "\n\n<compacted-summary>" + summary + "</compacted-summary>"
}

func deepSeekCompactUserAAD(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("authenticated user context is required for DeepSeek remote compaction")
	}
	userID, ok := ctx.Value(ctxkey.UserID).(int64)
	if !ok || userID <= 0 {
		return nil, errors.New("authenticated user context is required for DeepSeek remote compaction")
	}
	return []byte(deepSeekCompactCipherDomain + ":user:" + strconv.FormatInt(userID, 10)), nil
}

func (s *OpenAIGatewayService) deepSeekCompactAEAD() (cipher.AEAD, error) {
	if s == nil || s.cfg == nil || strings.TrimSpace(s.cfg.JWT.Secret) == "" {
		return nil, errors.New("DeepSeek remote compaction requires a persistent JWT secret")
	}
	key := sha256.Sum256([]byte(deepSeekCompactCipherDomain + "\x00" + s.cfg.JWT.Secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("initialize DeepSeek compact cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize DeepSeek compact AEAD: %w", err)
	}
	return aead, nil
}

func (s *OpenAIGatewayService) sealDeepSeekCompactCheckpoint(ctx context.Context, checkpoint string) (string, error) {
	if !utf8.ValidString(checkpoint) || len(checkpoint) > deepSeekCompactMaxSummaryBytes {
		return "", errors.New("DeepSeek compact checkpoint exceeds the supported size")
	}
	payload, err := marshalOpenAIUpstreamJSON(deepSeekCompactEnvelope{
		Version:    deepSeekCompactEnvelopeVersion,
		Checkpoint: checkpoint,
	})
	if err != nil {
		return "", fmt.Errorf("encode DeepSeek compact checkpoint: %w", err)
	}
	aead, err := s.deepSeekCompactAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate DeepSeek compact nonce: %w", err)
	}
	aad, err := deepSeekCompactUserAAD(ctx)
	if err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, payload, aad)
	envelope := deepSeekCompactEnvelopePrefix + base64.RawURLEncoding.EncodeToString(sealed)
	if len(envelope) > deepSeekCompactMaxEnvelopeBytes {
		return "", errors.New("DeepSeek compact encrypted_content exceeds the supported size")
	}
	return envelope, nil
}

func (s *OpenAIGatewayService) openDeepSeekCompactCheckpoint(ctx context.Context, envelope string) (string, error) {
	invalid := func() (string, error) { return "", ErrDeepSeekCompactInvalidEncryptedContent }
	if !strings.HasPrefix(envelope, deepSeekCompactEnvelopePrefix) || len(envelope) > deepSeekCompactMaxEnvelopeBytes {
		return invalid()
	}
	encoded := strings.TrimPrefix(envelope, deepSeekCompactEnvelopePrefix)
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return invalid()
	}
	aead, err := s.deepSeekCompactAEAD()
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return invalid()
	}
	aad, err := deepSeekCompactUserAAD(ctx)
	if err != nil {
		return invalid()
	}
	payload, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil || len(payload) > deepSeekCompactMaxEnvelopeBytes || !utf8.Valid(payload) {
		return invalid()
	}
	var state deepSeekCompactEnvelope
	if err := json.Unmarshal(payload, &state); err != nil ||
		state.Version != deepSeekCompactEnvelopeVersion ||
		strings.TrimSpace(state.Checkpoint) == "" ||
		len(state.Checkpoint) > deepSeekCompactMaxSummaryBytes ||
		!utf8.ValidString(state.Checkpoint) {
		return invalid()
	}
	return state.Checkpoint, nil
}

type deepSeekResponsesJSONScanMode uint8

const (
	deepSeekResponsesJSONScanGeneric deepSeekResponsesJSONScanMode = iota
	deepSeekResponsesJSONScanRoot
	deepSeekResponsesJSONScanInput
	deepSeekResponsesJSONScanInputItem
	deepSeekResponsesJSONScanContent
	deepSeekResponsesJSONScanContentItem
)

type deepSeekResponsesJSONScan struct {
	hasCompactState bool
}

var deepSeekResponsesRootJSONKeys = [...]string{
	"model", "instructions", "input", "max_output_tokens", "temperature", "top_p", "stream", "tools",
	"include", "store", "parallel_tool_calls", "reasoning", "text", "tool_choice", "service_tier",
	"prompt_cache_key", "previous_response_id", "user",
}

var deepSeekResponsesInputItemJSONKeys = [...]string{
	"type", "role", "content", "encrypted_content", "call_id", "name", "arguments", "id", "output",
	"namespace", "summary", "text", "input", "tools",
}

var deepSeekResponsesContentItemJSONKeys = [...]string{"type", "text", "image_url"}

func deepSeekResponsesCanonicalJSONKey(key string, candidates []string) (string, uint64, bool) {
	for index, candidate := range candidates {
		if strings.EqualFold(key, candidate) {
			return candidate, uint64(1) << index, true
		}
	}
	return "", 0, false
}

func scanDeepSeekResponsesJSON(body []byte) (bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	scan := &deepSeekResponsesJSONScan{}
	if err := consumeDeepSeekResponsesJSONValue(decoder, deepSeekResponsesJSONScanRoot, 0, scan); err != nil {
		return false, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return false, errors.New("DeepSeek Responses request contains trailing JSON data")
		}
		return false, err
	}
	return scan.hasCompactState, nil
}

func consumeDeepSeekResponsesJSONValue(
	decoder *json.Decoder,
	mode deepSeekResponsesJSONScanMode,
	depth int,
	scan *deepSeekResponsesJSONScan,
) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	return consumeDeepSeekResponsesJSONToken(decoder, token, mode, depth, scan)
}

func consumeDeepSeekResponsesJSONToken(
	decoder *json.Decoder,
	token json.Token,
	mode deepSeekResponsesJSONScanMode,
	depth int,
	scan *deepSeekResponsesJSONScan,
) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= deepSeekResponsesMaxJSONDepth {
		return errors.New("DeepSeek Responses request exceeds the supported JSON nesting depth")
	}
	switch delim {
	case '{':
		switch mode {
		case deepSeekResponsesJSONScanInput:
			mode = deepSeekResponsesJSONScanInputItem
		case deepSeekResponsesJSONScanContent:
			mode = deepSeekResponsesJSONScanContentItem
		}
		var seen uint64
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("DeepSeek Responses request contains a non-string object key")
			}
			canonical := ""
			bit := uint64(0)
			recognized := false
			switch mode {
			case deepSeekResponsesJSONScanRoot:
				canonical, bit, recognized = deepSeekResponsesCanonicalJSONKey(key, deepSeekResponsesRootJSONKeys[:])
			case deepSeekResponsesJSONScanInputItem:
				canonical, bit, recognized = deepSeekResponsesCanonicalJSONKey(key, deepSeekResponsesInputItemJSONKeys[:])
			case deepSeekResponsesJSONScanContentItem:
				canonical, bit, recognized = deepSeekResponsesCanonicalJSONKey(key, deepSeekResponsesContentItemJSONKeys[:])
			}
			if recognized {
				if key != canonical {
					return ErrDeepSeekResponsesNonCanonicalJSONKey
				}
				if seen&bit != 0 {
					return ErrDeepSeekResponsesDuplicateJSONKey
				}
				seen |= bit
			}

			valueMode := deepSeekResponsesJSONScanGeneric
			if mode == deepSeekResponsesJSONScanRoot && canonical == "input" {
				valueMode = deepSeekResponsesJSONScanInput
			} else if mode == deepSeekResponsesJSONScanInputItem &&
				(canonical == "content" || canonical == "summary" || canonical == "output") {
				valueMode = deepSeekResponsesJSONScanContent
			}
			if mode == deepSeekResponsesJSONScanInputItem && canonical == "type" {
				valueToken, err := decoder.Token()
				if err != nil {
					return err
				}
				if itemType, ok := valueToken.(string); ok &&
					(itemType == "compaction" || itemType == "compaction_summary") {
					scan.hasCompactState = true
				}
				if err := consumeDeepSeekResponsesJSONToken(decoder, valueToken, valueMode, depth+1, scan); err != nil {
					return err
				}
				continue
			}
			if err := consumeDeepSeekResponsesJSONValue(decoder, valueMode, depth+1, scan); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("DeepSeek Responses request has an invalid object boundary")
		}
	case '[':
		childMode := deepSeekResponsesJSONScanGeneric
		switch mode {
		case deepSeekResponsesJSONScanInput:
			childMode = deepSeekResponsesJSONScanInputItem
		case deepSeekResponsesJSONScanContent:
			childMode = deepSeekResponsesJSONScanContentItem
		}
		for decoder.More() {
			if err := consumeDeepSeekResponsesJSONValue(decoder, childMode, depth+1, scan); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("DeepSeek Responses request has an invalid array boundary")
		}
	default:
		return errors.New("DeepSeek Responses request has an invalid JSON boundary")
	}
	return nil
}

// RestoreDeepSeekCompactInput turns gateway-owned compaction items back into
// provider-neutral user checkpoints at the same input position. Foreign or
// tampered ciphertext fails closed instead of silently dropping context.
func (s *OpenAIGatewayService) RestoreDeepSeekCompactInput(ctx context.Context, body []byte) ([]byte, bool, error) {
	if s.deepSeekCompactRequestTooLarge(body) {
		return nil, false, ErrDeepSeekCompactRequestTooLarge
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false, nil
	}
	hasCompactState, err := scanDeepSeekResponsesJSON(body)
	if err != nil {
		return nil, false, err
	}
	if !hasCompactState {
		return body, false, nil
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return body, false, nil
	}
	inputRaw, ok := request["input"]
	if !ok || len(bytes.TrimSpace(inputRaw)) == 0 || bytes.TrimSpace(inputRaw)[0] != '[' {
		return nil, false, ErrDeepSeekCompactInvalidEncryptedContent
	}
	var items []json.RawMessage
	if err := json.Unmarshal(inputRaw, &items); err != nil {
		return nil, false, ErrDeepSeekCompactInvalidEncryptedContent
	}
	changed := false
	for i, raw := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		var itemType string
		if err := json.Unmarshal(item["type"], &itemType); err != nil ||
			(itemType != "compaction" && itemType != "compaction_summary") {
			continue
		}
		var encryptedContent string
		if err := json.Unmarshal(item["encrypted_content"], &encryptedContent); err != nil || strings.TrimSpace(encryptedContent) == "" {
			return nil, false, ErrDeepSeekCompactInvalidEncryptedContent
		}
		checkpoint, err := s.openDeepSeekCompactCheckpoint(ctx, encryptedContent)
		if err != nil {
			return nil, false, ErrDeepSeekCompactInvalidEncryptedContent
		}
		restored, err := marshalOpenAIUpstreamJSON(map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": checkpoint,
			}},
		})
		if err != nil {
			return nil, false, ErrDeepSeekCompactInvalidEncryptedContent
		}
		items[i] = restored
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	restoredInput, err := marshalOpenAIUpstreamJSON(items)
	if err != nil {
		return nil, false, ErrDeepSeekCompactInvalidEncryptedContent
	}
	request["input"] = restoredInput
	restoredBody, err := marshalOpenAIUpstreamJSON(request)
	if err != nil {
		return nil, false, ErrDeepSeekCompactInvalidEncryptedContent
	}
	return restoredBody, true, nil
}

// NormalizeDeepSeekLegacyCompactRequest converts the standalone
// /responses/compact request shape into the internal body-signal shape used by
// the harness-backed bridge. Callers must restore gateway-owned checkpoints
// first so policy inspection and this normalization operate on plaintext.
func (s *OpenAIGatewayService) NormalizeDeepSeekLegacyCompactRequest(c *gin.Context, body []byte) ([]byte, error) {
	if s.deepSeekCompactRequestTooLarge(body) {
		return nil, ErrDeepSeekCompactRequestTooLarge
	}
	if !IsDeepSeekResponsesInputValidated(c) {
		if _, err := scanDeepSeekResponsesJSON(body); err != nil {
			return nil, err
		}
	}

	normalized, _, err := normalizeOpenAICompactRequestBody(body)
	if err != nil {
		return nil, fmt.Errorf("normalize DeepSeek legacy compact request: %w", err)
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &request); err != nil {
		return nil, fmt.Errorf("parse DeepSeek legacy compact request: %w", err)
	}
	inputRaw, ok := request["input"]
	if !ok {
		return nil, errors.New("DeepSeek legacy compact request requires input")
	}
	var items []json.RawMessage
	trimmedInput := bytes.TrimSpace(inputRaw)
	if len(trimmedInput) > 0 && trimmedInput[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmedInput, &text); err != nil || strings.TrimSpace(text) == "" {
			return nil, errors.New("DeepSeek legacy compact input string must not be empty")
		}
		message, err := marshalOpenAIUpstreamJSON(map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": text,
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("encode DeepSeek legacy compact string input: %w", err)
		}
		items = []json.RawMessage{message}
	} else if err := json.Unmarshal(inputRaw, &items); err != nil {
		return nil, errors.New("DeepSeek legacy compact input must be a string or array")
	}

	triggerIndex := -1
	for i, raw := range items {
		if strings.TrimSpace(gjson.GetBytes(raw, "type").String()) != "compaction_trigger" {
			continue
		}
		if triggerIndex >= 0 {
			return nil, errors.New("DeepSeek remote compaction requires exactly one compaction_trigger")
		}
		triggerIndex = i
	}
	if triggerIndex >= 0 && triggerIndex != len(items)-1 {
		return nil, errors.New("DeepSeek remote compaction requires a final compaction_trigger")
	}
	if triggerIndex < 0 {
		trigger, _ := marshalOpenAIUpstreamJSON(map[string]any{"type": "compaction_trigger"})
		items = append(items, trigger)
	}
	encodedInput, err := marshalOpenAIUpstreamJSON(items)
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek legacy compact input: %w", err)
	}
	request["input"] = encodedInput
	encoded, err := marshalOpenAIUpstreamJSON(request)
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek legacy compact request: %w", err)
	}
	if s.deepSeekCompactRequestTooLarge(encoded) {
		return nil, ErrDeepSeekCompactRequestTooLarge
	}
	return encoded, nil
}

// PrepareDeepSeekRemoteCompactionRequest performs account-independent
// conversion after the user concurrency and billing gates but before account
// scheduling. Forward reuses the cached body and only applies account model
// mapping, avoiding a second conversion of a potentially large history.
func (s *OpenAIGatewayService) PrepareDeepSeekRemoteCompactionRequest(c *gin.Context, body []byte) error {
	if s.deepSeekCompactRequestTooLarge(body) {
		return ErrDeepSeekCompactRequestTooLarge
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return errors.New("DeepSeek remote compaction requires a model")
	}
	chatBody, compactedBytes, err := deepSeekCompactChatRequest(body, model)
	if err != nil {
		return err
	}
	if c != nil {
		c.Set(deepSeekCompactPreparedKey, deepSeekCompactPreparedRequest{
			SourceHash:     sha256.Sum256(body),
			ChatBody:       chatBody,
			CompactedBytes: compactedBytes,
		})
	}
	return nil
}

func (s *OpenAIGatewayService) deepSeekCompactRequestTooLarge(body []byte) bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.MaxBodySize > 0 && int64(len(body)) > s.cfg.Gateway.MaxBodySize
}

func preparedDeepSeekCompactChatRequest(c *gin.Context, sourceBody []byte, upstreamModel string) ([]byte, int, bool) {
	if c == nil {
		return nil, 0, false
	}
	value, ok := c.Get(deepSeekCompactPreparedKey)
	if !ok {
		return nil, 0, false
	}
	prepared, ok := value.(deepSeekCompactPreparedRequest)
	if !ok || prepared.SourceHash != sha256.Sum256(sourceBody) || len(prepared.ChatBody) == 0 {
		return nil, 0, false
	}
	chatBody := prepared.ChatBody
	if currentModel := strings.TrimSpace(gjson.GetBytes(chatBody, "model").String()); currentModel != upstreamModel {
		chatBody = ReplaceModelInBody(chatBody, upstreamModel)
	}
	return chatBody, prepared.CompactedBytes, true
}

func deepSeekCompactContentContainsImage(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] == '"' {
		return false
	}
	parts := []json.RawMessage{trimmed}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &parts); err != nil {
			return false
		}
	}
	for _, part := range parts {
		partType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(part, "type").String()))
		switch partType {
		case "image", "input_image", "output_image", "image_url", "image_generation_call":
			return true
		}
	}
	return false
}

func deepSeekCompactInputItemContainsImage(raw json.RawMessage) bool {
	itemType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(raw, "type").String()))
	switch itemType {
	case "image", "input_image", "output_image", "image_url", "image_generation_call":
		return true
	case "message", "":
		return deepSeekCompactContentContainsImage(json.RawMessage(gjson.GetBytes(raw, "content").Raw))
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		return deepSeekCompactContentContainsImage(json.RawMessage(gjson.GetBytes(raw, "output").Raw))
	default:
		return false
	}
}

func deepSeekCompactChatChunkContainsImage(payload []byte) bool {
	if images := gjson.GetBytes(payload, "images"); images.Exists() && len(images.Array()) > 0 {
		return true
	}
	for _, choice := range gjson.GetBytes(payload, "choices").Array() {
		delta := choice.Get("delta")
		if imageURL := delta.Get("image_url"); imageURL.Exists() && imageURL.Type != gjson.Null {
			return true
		}
		if images := delta.Get("images"); images.Exists() && len(images.Array()) > 0 {
			return true
		}
		content := delta.Get("content")
		if (content.IsArray() || content.IsObject()) && deepSeekCompactContentContainsImage(json.RawMessage(content.Raw)) {
			return true
		}
	}
	return false
}

func normalizeDeepSeekCompactTextContent(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		empty, _ := json.Marshal("")
		return empty, true
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return raw, false
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return raw, false
	}
	var joined strings.Builder
	foundText := false
	for _, partRaw := range parts {
		var partText string
		if err := json.Unmarshal(partRaw, &partText); err == nil {
			_, _ = joined.WriteString(partText)
			foundText = true
			continue
		}
		var part map[string]json.RawMessage
		if err := json.Unmarshal(partRaw, &part); err != nil {
			continue
		}
		if err := json.Unmarshal(part["text"], &partText); err == nil {
			_, _ = joined.WriteString(partText)
			foundText = true
		}
	}
	if !foundText && len(parts) > 0 {
		return raw, false
	}
	encoded, _ := json.Marshal(joined.String())
	return encoded, true
}

func normalizeDeepSeekCompactToolOutput(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	var text string
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) ||
		(json.Unmarshal(trimmed, &text) == nil && text == "") {
		empty, _ := json.Marshal("(no output)")
		return empty, true
	}
	if json.Unmarshal(trimmed, &text) == nil {
		return raw, false
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return raw, false
	}
	if len(parts) == 0 {
		empty, _ := json.Marshal("(no output)")
		return empty, true
	}
	var joined strings.Builder
	for _, partRaw := range parts {
		if err := json.Unmarshal(partRaw, &text); err == nil {
			_, _ = joined.WriteString(text)
			continue
		}
		partType := strings.TrimSpace(gjson.GetBytes(partRaw, "type").String())
		if partType != "" && partType != "input_text" && partType != "output_text" && partType != "text" {
			return raw, false
		}
		partText := gjson.GetBytes(partRaw, "text")
		if partText.Type != gjson.String {
			return raw, false
		}
		_, _ = joined.WriteString(partText.String())
	}
	if joined.Len() == 0 {
		_, _ = joined.WriteString("(no output)")
	}
	encoded, _ := json.Marshal(joined.String())
	return encoded, true
}

func validateDeepSeekCompactToolPairs(items []json.RawMessage) error {
	pending := make(map[string]struct{})
	for _, raw := range items {
		itemType := strings.TrimSpace(gjson.GetBytes(raw, "type").String())
		switch itemType {
		case "function_call", "custom_tool_call", "tool_search_call":
			callID := strings.TrimSpace(gjson.GetBytes(raw, "call_id").String())
			if callID == "" {
				return errors.New("DeepSeek remote compaction contains a tool call without call_id")
			}
			if _, exists := pending[callID]; exists {
				return errors.New("DeepSeek remote compaction contains duplicate tool call ids")
			}
			pending[callID] = struct{}{}
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			callID := strings.TrimSpace(gjson.GetBytes(raw, "call_id").String())
			if _, exists := pending[callID]; callID == "" || !exists {
				return errors.New("DeepSeek remote compaction contains an unpaired tool result")
			}
			delete(pending, callID)
		default:
			if len(pending) > 0 {
				return errors.New("DeepSeek remote compaction contains an interrupted tool call/result sequence")
			}
		}
	}
	if len(pending) > 0 {
		return errors.New("DeepSeek remote compaction contains an unanswered tool call")
	}
	return nil
}

func deepSeekCompactReasoningHasVisibleSuccessor(items []json.RawMessage, index int) bool {
	for i := index + 1; i < len(items); i++ {
		itemType := strings.TrimSpace(gjson.GetBytes(items[i], "type").String())
		switch itemType {
		case "reasoning":
			continue
		case "function_call", "custom_tool_call", "tool_search_call":
			return true
		case "message":
			return strings.EqualFold(strings.TrimSpace(gjson.GetBytes(items[i], "role").String()), "assistant")
		default:
			return false
		}
	}
	return false
}

func normalizeDeepSeekCompactHistory(items []json.RawMessage) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items)+1)
	for index, raw := range items {
		if deepSeekCompactInputItemContainsImage(raw) {
			return nil, errors.New("DeepSeek remote compaction does not support image content")
		}
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			out = append(out, raw)
			continue
		}
		itemType := strings.TrimSpace(gjson.GetBytes(raw, "type").String())
		switch itemType {
		case "message", "":
			if content, ok := item["content"]; ok {
				if normalized, changed := normalizeDeepSeekCompactTextContent(content); changed {
					item["content"] = normalized
				}
			}
		case "reasoning":
			for _, field := range []string{"summary", "content"} {
				if value, ok := item[field]; ok {
					if normalized, changed := normalizeDeepSeekCompactTextContent(value); changed {
						item[field] = normalized
					}
				}
			}
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			if normalized, changed := normalizeDeepSeekCompactToolOutput(item["output"]); changed {
				item["output"] = normalized
			}
		}
		normalized, err := marshalOpenAIUpstreamJSON(item)
		if err != nil {
			return nil, fmt.Errorf("normalize DeepSeek compact history item: %w", err)
		}
		out = append(out, normalized)
		if itemType == "reasoning" && !deepSeekCompactReasoningHasVisibleSuccessor(items, index) {
			emptyAssistant, _ := marshalOpenAIUpstreamJSON(map[string]any{
				"type": "message", "role": "assistant", "content": "",
			})
			out = append(out, emptyAssistant)
		}
	}
	return out, nil
}

func normalizeDeepSeekCompactChatMessages(messages []apicompat.ChatMessage) {
	for i := range messages {
		switch messages[i].Role {
		case "assistant":
			content := bytes.TrimSpace(messages[i].Content)
			if len(content) == 0 || bytes.Equal(content, []byte("null")) {
				messages[i].Content = json.RawMessage(`""`)
			}
		case "tool":
			content := bytes.TrimSpace(messages[i].Content)
			if len(content) == 0 || bytes.Equal(content, []byte("null")) || bytes.Equal(content, []byte(`""`)) {
				messages[i].Content, _ = json.Marshal("(no output)")
			}
		}
	}
}

func deepSeekCompactChatRequest(body []byte, upstreamModel string) ([]byte, int, error) {
	var request apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, 0, fmt.Errorf("parse DeepSeek compact request: %w", err)
	}
	if strings.TrimSpace(request.PreviousResponseID) != "" {
		return nil, 0, errors.New("DeepSeek remote compaction does not support previous_response_id")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(request.Input, &items); err != nil {
		return nil, 0, errors.New("DeepSeek remote compaction input must be an array")
	}
	triggerIndex := -1
	for i, raw := range items {
		if strings.TrimSpace(gjson.GetBytes(raw, "type").String()) != "compaction_trigger" {
			continue
		}
		if triggerIndex >= 0 {
			return nil, 0, errors.New("DeepSeek remote compaction requires exactly one compaction_trigger")
		}
		triggerIndex = i
	}
	if triggerIndex < 0 || triggerIndex != len(items)-1 {
		return nil, 0, errors.New("DeepSeek remote compaction requires a final compaction_trigger")
	}
	if triggerIndex == 0 {
		return nil, 0, errors.New("DeepSeek remote compaction has no conversation history to summarize")
	}
	if err := validateDeepSeekCompactToolPairs(items[:triggerIndex]); err != nil {
		return nil, 0, err
	}
	compactedBytes := 2
	for index, raw := range items[:triggerIndex] {
		if index > 0 {
			compactedBytes++
		}
		compactedBytes += len(bytes.TrimSpace(raw))
	}
	normalizedHistory, err := normalizeDeepSeekCompactHistory(items[:triggerIndex])
	if err != nil {
		return nil, 0, err
	}
	request.Input, err = marshalOpenAIUpstreamJSON(normalizedHistory)
	if err != nil {
		return nil, 0, fmt.Errorf("encode normalized DeepSeek compact history: %w", err)
	}
	chatRequest, err := apicompat.ResponsesToChatCompletionsRequest(&request)
	if err != nil {
		return nil, 0, fmt.Errorf("convert DeepSeek compact history: %w", err)
	}
	if deepSeekCompactChatContainsImage(chatRequest) {
		return nil, 0, errors.New("DeepSeek remote compaction does not support image content")
	}
	normalizeDeepSeekCompactChatMessages(chatRequest.Messages)
	prompt, _ := json.Marshal(deepSeekCompactInstruction)
	chatRequest.Messages = append(chatRequest.Messages, apicompat.ChatMessage{Role: "user", Content: prompt})
	chatRequest.Model = upstreamModel
	chatRequest.Stream = true
	chatRequest.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	maxTokens := deepSeekCompactSummaryMaxTokens
	chatRequest.MaxTokens = &maxTokens
	chatRequest.MaxCompletionTokens = nil
	chatRequest.Temperature = nil
	chatRequest.TopP = nil
	chatRequest.ParallelToolCalls = nil
	chatRequest.ToolChoice = nil
	chatRequest.ReasoningEffort = deepSeekCompactReasoningEffort
	chatRequest.ServiceTier = ""
	chatRequest.Stop = nil
	chatRequest.ResponseFormat = nil
	encoded, err := marshalOpenAIUpstreamJSON(chatRequest)
	if err != nil {
		return nil, 0, fmt.Errorf("encode DeepSeek compact chat request: %w", err)
	}
	encoded, err = sjson.SetBytes(encoded, "thinking.type", "enabled")
	if err != nil {
		return nil, 0, fmt.Errorf("enable DeepSeek compact thinking: %w", err)
	}
	return encoded, compactedBytes, nil
}

func deepSeekCompactChatContainsImage(request *apicompat.ChatCompletionsRequest) bool {
	if request == nil {
		return false
	}
	for _, message := range request.Messages {
		content := bytes.TrimSpace(message.Content)
		if len(content) == 0 || content[0] != '[' {
			continue
		}
		var parts []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(content, &parts); err != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "image_url" || part.Type == "input_image" || part.Type == "image" {
				return true
			}
		}
	}
	return false
}

func (s *OpenAIGatewayService) readDeepSeekCompactChatStream(c *gin.Context, resp *http.Response, account *Account, startTime time.Time) (deepSeekCompactStreamResult, error) {
	var result deepSeekCompactStreamResult
	if resp == nil || resp.Body == nil {
		return result, errors.New("DeepSeek compact stream has no response body")
	}
	var summary strings.Builder
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	var dataLines []string
	currentEventType := ""
	var protocolErr error
	var rawBytes int
	sawDone := false
	var cyberMark *CyberPolicyMark
	defer func() {
		if cyberMark == nil {
			return
		}
		cyberMark.UpstreamInTok = result.Usage.InputTokens
		cyberMark.UpstreamOutTok = result.Usage.OutputTokens
		MarkOpsCyberPolicy(c, *cyberMark)
	}()

	processEvent := func() bool {
		eventType := currentEventType
		currentEventType = ""
		if len(dataLines) == 0 {
			return false
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" {
			return false
		}
		if payload == "[DONE]" {
			if eventType == "error" {
				protocolErr = errors.New("DeepSeek compact stream returned an error event")
				result.UpstreamFailed = true
				return false
			}
			sawDone = true
			return true
		}
		payload = string(redactDeepSeekAPIKey(account, []byte(payload)))
		if !gjson.Valid(payload) {
			protocolErr = errors.New("DeepSeek compact stream returned malformed JSON data")
			result.UpstreamFailed = true
			return false
		}
		observer.ObserveOpenAI([]byte(payload), "")
		if result.ResponseID == "" {
			result.ResponseID = strings.TrimSpace(gjson.Get(payload, "id").String())
		}
		if usage := extractCCStreamUsage(payload); usage != nil && hasBillableOpenAIUsage(*usage) {
			result.Usage = *usage
			if total := int(gjson.Get(payload, "usage.total_tokens").Int()); total > 0 {
				result.TotalTokens = total
			}
		}
		if hit, code, message := detectOpenAICyberPolicy([]byte(payload)); hit {
			cyberMark = &CyberPolicyMark{
				Code: code, Message: message, Body: truncateString(payload, 4096), UpstreamStatus: http.StatusOK,
			}
		}
		errorPayload := gjson.Get(payload, "error")
		if eventType == "error" || gjson.Get(payload, "type").String() == "error" ||
			(errorPayload.Exists() && errorPayload.Type != gjson.Null) {
			protocolErr = errors.New("DeepSeek compact stream returned an error event")
			result.UpstreamFailed = true
			return false
		}
		if deepSeekCompactChatChunkContainsImage([]byte(payload)) {
			protocolErr = errors.New("DeepSeek compact summary cannot contain image output")
			result.UpstreamFailed = true
		}
		for _, choice := range gjson.Get(payload, "choices").Array() {
			content := choice.Get("delta.content")
			if content.Exists() && content.Type != gjson.String && content.Type != gjson.Null {
				protocolErr = errors.New("DeepSeek compact summary cannot contain non-text output")
				result.UpstreamFailed = true
				continue
			}
			text := content.String()
			if text != "" {
				if result.FirstTokenMs == nil {
					elapsed := int(time.Since(startTime).Milliseconds())
					result.FirstTokenMs = &elapsed
				}
				if summary.Len()+len(text) > deepSeekCompactMaxSummaryBytes {
					protocolErr = errors.New("DeepSeek compact summary exceeds the supported size")
				} else if protocolErr == nil {
					_, _ = summary.WriteString(text)
				}
			}
			switch finishReason := strings.TrimSpace(choice.Get("finish_reason").String()); finishReason {
			case "", "stop", "tool_calls":
			case "length":
				protocolErr = errors.New("DeepSeek compact summary was truncated at the token cap")
			default:
				protocolErr = fmt.Errorf("DeepSeek compact summary stopped with finish_reason %q", finishReason)
			}
		}
		return false
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), deepSeekCompactMaxSSELineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		rawBytes += len(line) + 1
		if rawBytes > deepSeekCompactMaxSSEBytes {
			return result, errors.New("DeepSeek compact stream exceeds the supported size")
		}
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if processEvent() {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			dataLines = append(dataLines, data)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read DeepSeek compact stream: %w", err)
	}
	if !sawDone {
		return result, errors.New("DeepSeek compact stream ended before a blank-line-dispatched [DONE]")
	}
	if !hasBillableOpenAIUsage(result.Usage) {
		return result, errors.New(deepSeekMissingUsageMsg)
	}
	result.Completed = true
	if result.TotalTokens <= 0 {
		result.TotalTokens = result.Usage.InputTokens + result.Usage.OutputTokens
	}
	if protocolErr != nil {
		return result, protocolErr
	}
	result.Summary = string(redactDeepSeekAPIKey(account, []byte(summary.String())))
	if strings.TrimSpace(result.Summary) == "" {
		return result, errors.New("DeepSeek compaction produced no text summary content")
	}
	return result, nil
}

func deepSeekCompactResponsesJSON(responseID, itemID, model, encryptedContent string, usage OpenAIUsage, totalTokens int) ([]byte, error) {
	if totalTokens <= 0 {
		totalTokens = usage.InputTokens + usage.OutputTokens
	}
	return marshalOpenAIUpstreamJSON(map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"model":      model,
		"status":     "completed",
		"output": []any{map[string]any{
			"id":                itemID,
			"type":              "compaction",
			"status":            "completed",
			"encrypted_content": encryptedContent,
		}},
		"usage": map[string]any{
			"input_tokens": usage.InputTokens,
			"input_tokens_details": map[string]any{
				"cached_tokens": usage.CacheReadInputTokens,
			},
			"output_tokens": usage.OutputTokens,
			"output_tokens_details": map[string]any{
				"reasoning_tokens": usage.ReasoningTokens,
			},
			"total_tokens": totalTokens,
		},
	})
}

func (s *OpenAIGatewayService) handleCommittedDeepSeekCompactHTTPError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	resp *http.Response,
	body []byte,
	upstreamModel string,
) (*OpenAIForwardResult, error) {
	body = redactDeepSeekAPIKey(account, body)
	message := redactDeepSeekAPIKeyString(account, strings.TrimSpace(extractUpstreamErrorMessage(body)))
	detail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		detail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, message, detail)
	if hit, code, cyberMessage := detectOpenAICyberPolicy(body); hit {
		MarkOpsCyberPolicy(c, CyberPolicyMark{
			Code: code, Message: cyberMessage, Body: truncateString(string(body), 4096), UpstreamStatus: resp.StatusCode,
		})
	} else {
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, upstreamModel)
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  openAICompatibleUpstreamRequestID(resp.Header),
		Kind:               "http_error",
		Message:            message,
		Detail:             detail,
	})
	writeOpenAICompactSSEFailure(c, resp.StatusCode, body)
	return nil, fmt.Errorf("non-streaming openai protocol error: DeepSeek compact upstream HTTP %d", resp.StatusCode)
}

func (s *OpenAIGatewayService) forwardDeepSeekRemoteCompactionV2(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	billingModel string,
	upstreamModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if s.deepSeekCompactRequestTooLarge(body) {
		return nil, ErrDeepSeekCompactRequestTooLarge
	}
	if _, err := deepSeekCompactUserAAD(ctx); err != nil {
		return nil, err
	}
	if _, err := s.deepSeekCompactAEAD(); err != nil {
		return nil, err
	}
	chatBody, compactedBytes, prepared := preparedDeepSeekCompactChatRequest(c, body, upstreamModel)
	if !prepared {
		var err error
		chatBody, compactedBytes, err = deepSeekCompactChatRequest(body, upstreamModel)
		if err != nil {
			return nil, err
		}
	}
	token := account.GetDeepSeekAPIKey()
	if token == "" {
		return nil, fmt.Errorf("account %d missing api_key", account.ID)
	}
	targetURL, err := s.deepSeekEndpointURL(account, deepSeekChatCompletionsEndpoint)
	if err != nil {
		return nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, deepSeekChatCompletionsEndpoint)

	upstreamStart := time.Now()
	resp, err := s.sendCCUpstreamRequest(ctx, c, account, targetURL, chatBody, true, token, "", "")
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
		if StopOpenAICompactSSEKeepaliveCommitted(c) {
			return s.handleCommittedDeepSeekCompactHTTPError(ctx, c, account, resp, respBody, upstreamModel)
		}
		return s.handleErrorResponse(ctx, resp, c, account, chatBody, billingModel)
	}

	streamResult, streamErr := s.readDeepSeekCompactChatStream(c, resp, account, startTime)
	clientStream := openAICompactClientWantsStream(c)
	requestID := openAICompatibleUpstreamRequestID(resp.Header)
	terminalEvent := ""
	if streamResult.Completed && !streamResult.UpstreamFailed {
		terminalEvent = "response.completed"
	}
	result := &OpenAIForwardResult{
		RequestID:                     requestID,
		ResponseID:                    streamResult.ResponseID,
		Usage:                         streamResult.Usage,
		Model:                         originalModel,
		BillingModel:                  billingModel,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		UpstreamEndpoint:              deepSeekChatCompletionsEndpoint,
		ReasoningEffort:               optionalTrimmedStringPtr(deepSeekCompactReasoningEffort),
		UpstreamTerminalEvent:         terminalEvent,
		Stream:                        clientStream,
		Duration:                      time.Since(startTime),
		FirstTokenMs:                  streamResult.FirstTokenMs,
		ResponseHeaders:               resp.Header.Clone(),
	}
	if !streamResult.Completed {
		if result.HasBillableTokenUsage() {
			return result, streamErr
		}
		return nil, s.newOpenAIStreamFailoverError(c, account, false, requestID, nil, streamErr.Error(), resp.Header)
	}
	if streamErr != nil {
		return result, streamErr
	}

	checkpoint := frameDeepSeekCompactSummary(streamResult.Summary)
	if len(checkpoint) >= compactedBytes {
		return result, errors.New("DeepSeek compact checkpoint is not smaller than the summarized history")
	}
	encryptedContent, err := s.sealDeepSeekCompactCheckpoint(ctx, checkpoint)
	if err != nil {
		return result, err
	}
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	itemID := "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	finalJSON, err := deepSeekCompactResponsesJSON(responseID, itemID, originalModel, encryptedContent, streamResult.Usage, streamResult.TotalTokens)
	if err != nil {
		return result, fmt.Errorf("encode DeepSeek compact response: %w", err)
	}
	result.ResponseID = responseID
	s.bindHTTPResponseAccount(ctx, c, account, responseID)
	s.writeDeepSeekResponsesHeaders(c, resp, clientStream)
	if clientStream {
		if !writeOpenAICompactSSEBridge(c, http.StatusOK, finalJSON) {
			return result, errors.New("failed to synthesize DeepSeek remote compaction response")
		}
	} else {
		c.Header("Content-Type", "application/json")
		c.Data(http.StatusOK, "application/json", finalJSON)
	}
	return result, nil
}
