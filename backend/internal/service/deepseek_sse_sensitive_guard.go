package service

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	deepSeekSSESensitiveHoldMaxEvents = 256
	deepSeekSSESensitiveHoldMaxBytes  = 1 << 20
)

var errDeepSeekSSESensitiveData = errors.New("DeepSeek SSE emitted split sensitive data")

type deepSeekSSESensitiveProtocol uint8

const (
	deepSeekSSESensitiveProtocolChat deepSeekSSESensitiveProtocol = iota + 1
	deepSeekSSESensitiveProtocolResponses
	deepSeekSSESensitiveProtocolAnthropic
)

type deepSeekSSESensitiveFragment struct {
	streamKey string
	value     string
}

type deepSeekSSESensitiveEventAnalysis struct {
	fragments       []deepSeekSSESensitiveFragment
	endStreamPrefix []string
	endStreamExact  []string
	terminal        bool
}

type deepSeekSSESensitiveEventGuard struct {
	account        *Account
	protocol       deepSeekSSESensitiveProtocol
	secrets        []string
	current        [][]byte
	currentBytes   int
	pending        [][][]byte
	pendingBytes   int
	prefix         string
	prefixStream   string
	streamPrefixes map[string]string
	blockedErr     error
}

func newDeepSeekSSESensitiveEventGuard(account *Account, protocol deepSeekSSESensitiveProtocol) *deepSeekSSESensitiveEventGuard {
	guard := &deepSeekSSESensitiveEventGuard{
		account:  account,
		protocol: protocol,
	}
	if account != nil {
		if apiKey := account.GetDeepSeekAPIKey(); apiKey != "" {
			guard.secrets = append(guard.secrets, apiKey)
		}
	}
	return guard
}

func deepSeekSSEWireLineContent(wire []byte) []byte {
	end := len(wire)
	if end > 0 && wire[end-1] == '\n' {
		end--
	}
	if end > 0 && wire[end-1] == '\r' {
		end--
	}
	return wire[:end]
}

func cloneDeepSeekSSEWireEvent(event [][]byte) [][]byte {
	cloned := make([][]byte, len(event))
	for i := range event {
		cloned[i] = append([]byte(nil), event[i]...)
	}
	return cloned
}

func (g *deepSeekSSESensitiveEventGuard) PushWireLine(wire []byte, emit func([]byte) error) error {
	if g == nil {
		return emit(wire)
	}
	if g.blockedErr != nil {
		return g.blockedErr
	}
	additionalEvent := 0
	if len(g.current) == 0 {
		additionalEvent = 1
	}
	if len(g.pending)+additionalEvent > deepSeekSSESensitiveHoldMaxEvents ||
		g.pendingBytes+g.currentBytes+len(wire) > deepSeekSSESensitiveHoldMaxBytes {
		return g.block(fmt.Errorf("%w: current event limit exceeded", errDeepSeekSSESensitiveData))
	}
	safeWire := append([]byte(nil), redactDeepSeekAPIKey(g.account, wire)...)
	if g.pendingBytes+g.currentBytes+len(safeWire) > deepSeekSSESensitiveHoldMaxBytes {
		return g.block(fmt.Errorf("%w: current event limit exceeded", errDeepSeekSSESensitiveData))
	}
	g.current = append(g.current, safeWire)
	g.currentBytes += len(safeWire)
	if len(bytes.TrimSpace(deepSeekSSEWireLineContent(safeWire))) != 0 {
		return nil
	}
	err := g.finishCurrentEvent(emit)
	if errors.Is(err, errDeepSeekSSESensitiveData) {
		return g.block(err)
	}
	return err
}

func (g *deepSeekSSESensitiveEventGuard) Finish(emit func([]byte) error) error {
	if g == nil {
		return nil
	}
	if g.blockedErr != nil {
		return g.blockedErr
	}
	if len(g.current) > 0 {
		err := g.finishCurrentEvent(emit)
		if errors.Is(err, errDeepSeekSSESensitiveData) {
			return g.block(err)
		}
		return err
	}
	return nil
}

func (g *deepSeekSSESensitiveEventGuard) block(err error) error {
	if g.blockedErr == nil {
		g.blockedErr = err
	}
	g.current = nil
	g.currentBytes = 0
	g.pending = nil
	g.pendingBytes = 0
	g.prefix = ""
	g.prefixStream = ""
	g.streamPrefixes = nil
	return g.blockedErr
}

func (g *deepSeekSSESensitiveEventGuard) finishCurrentEvent(emit func([]byte) error) error {
	event := g.current
	g.current = nil
	g.currentBytes = 0
	analysis := analyzeDeepSeekSSESensitiveEvent(g.protocol, event)
	hadPending := len(g.pending) > 0 || g.hasPendingPrefix()
	for _, fragment := range analysis.fragments {
		if err := g.observeFragment(fragment); err != nil {
			return err
		}
	}
	for _, prefix := range analysis.endStreamPrefix {
		g.endStreams(prefix)
	}
	for _, streamKey := range analysis.endStreamExact {
		g.endStream(streamKey)
	}

	if hadPending || g.hasPendingPrefix() {
		if err := g.appendPending(event); err != nil {
			return err
		}
	} else if err := emitDeepSeekSSEWireEvent(event, emit); err != nil {
		return err
	}

	if analysis.terminal || (len(g.pending) > 0 && !g.hasPendingPrefix()) {
		return g.flushPending(emit)
	}
	return nil
}

func (g *deepSeekSSESensitiveEventGuard) appendPending(event [][]byte) error {
	eventBytes := 0
	for _, wire := range event {
		eventBytes += len(wire)
	}
	if len(g.pending) >= deepSeekSSESensitiveHoldMaxEvents || g.pendingBytes+eventBytes > deepSeekSSESensitiveHoldMaxBytes {
		return fmt.Errorf("%w: pending event limit exceeded", errDeepSeekSSESensitiveData)
	}
	g.pending = append(g.pending, cloneDeepSeekSSEWireEvent(event))
	g.pendingBytes += eventBytes
	return nil
}

func emitDeepSeekSSEWireEvent(event [][]byte, emit func([]byte) error) error {
	for _, wire := range event {
		if err := emit(wire); err != nil {
			return err
		}
	}
	return nil
}

func (g *deepSeekSSESensitiveEventGuard) flushPending(emit func([]byte) error) error {
	for _, event := range g.pending {
		if err := emitDeepSeekSSEWireEvent(event, emit); err != nil {
			return err
		}
	}
	g.pending = nil
	g.pendingBytes = 0
	g.prefix = ""
	g.prefixStream = ""
	g.streamPrefixes = nil
	return nil
}

func (g *deepSeekSSESensitiveEventGuard) hasPendingPrefix() bool {
	return g.prefix != "" || len(g.streamPrefixes) > 0
}

func (g *deepSeekSSESensitiveEventGuard) observeFragment(fragment deepSeekSSESensitiveFragment) error {
	streamKey := strings.TrimSpace(fragment.streamKey)
	if streamKey == "" {
		streamKey = "default"
	}
	globalPrefix, err := g.advancePrefix(g.prefix, fragment.value)
	if err != nil {
		return err
	}
	streamPrefix, err := g.advancePrefix(g.streamPrefixes[streamKey], fragment.value)
	if err != nil {
		return err
	}
	g.prefix = globalPrefix
	if globalPrefix == "" {
		g.prefixStream = ""
	} else {
		g.prefixStream = streamKey
	}
	if streamPrefix == "" {
		delete(g.streamPrefixes, streamKey)
	} else {
		if g.streamPrefixes == nil {
			g.streamPrefixes = make(map[string]string)
		}
		g.streamPrefixes[streamKey] = streamPrefix
	}
	return nil
}

func (g *deepSeekSSESensitiveEventGuard) endStreams(prefix string) {
	for streamKey := range g.streamPrefixes {
		if strings.HasPrefix(streamKey, prefix) {
			delete(g.streamPrefixes, streamKey)
		}
	}
	if strings.HasPrefix(g.prefixStream, prefix) {
		g.prefix = ""
		g.prefixStream = ""
	}
}

func (g *deepSeekSSESensitiveEventGuard) endStream(streamKey string) {
	delete(g.streamPrefixes, streamKey)
	if g.prefixStream == streamKey {
		g.prefix = ""
		g.prefixStream = ""
	}
}

func (g *deepSeekSSESensitiveEventGuard) advancePrefix(prefix, delta string) (string, error) {
	candidate := delta
	if prefix != "" {
		candidate = prefix + delta
	}
	if redactDeepSeekDerivedUserIDsString(candidate) != candidate {
		return "", errDeepSeekSSESensitiveData
	}
	for _, secret := range g.secrets {
		if strings.Contains(candidate, secret) {
			return "", errDeepSeekSSESensitiveData
		}
	}
	for _, secret := range g.secrets {
		if strings.HasPrefix(secret, candidate) {
			return candidate, nil
		}
	}
	if deepSeekSSEDerivedIDPrefixPossible(candidate) {
		return candidate, nil
	}
	return g.longestSensitivePrefixSuffix(candidate), nil
}

func (g *deepSeekSSESensitiveEventGuard) longestSensitivePrefixSuffix(value string) string {
	longest := ""
	for _, secret := range g.secrets {
		if len(secret) <= 1 {
			continue
		}
		maxPrefix := min(len(value), len(secret)-1)
		for size := maxPrefix; size > len(longest); size-- {
			if strings.HasSuffix(value, secret[:size]) {
				longest = secret[:size]
				break
			}
		}
	}
	if derivedPrefix := deepSeekSSELongestDerivedIDPrefixSuffix(value); len(derivedPrefix) > len(longest) {
		longest = derivedPrefix
	}
	return longest
}

func deepSeekSSEDerivedIDPrefixPossible(value string) bool {
	if value == "" {
		return false
	}
	if len(value) <= len(deepSeekUserIDPrefix) {
		return strings.HasPrefix(deepSeekUserIDPrefix, value)
	}
	if !strings.HasPrefix(value, deepSeekUserIDPrefix) || len(value) >= len(deepSeekUserIDPrefix)+deepSeekUserIDEncodedDigestBytes {
		return false
	}
	for _, char := range value[len(deepSeekUserIDPrefix):] {
		if !isDeepSeekDerivedUserIDByte(char) {
			return false
		}
	}
	return true
}

func deepSeekSSELongestDerivedIDPrefixSuffix(value string) string {
	maxLength := len(deepSeekUserIDPrefix) + deepSeekUserIDEncodedDigestBytes - 1
	if len(value) > maxLength {
		value = value[len(value)-maxLength:]
	}
	for start := 0; start < len(value); start++ {
		candidate := value[start:]
		if deepSeekSSEDerivedIDPrefixPossible(candidate) {
			return candidate
		}
	}
	return ""
}

func analyzeDeepSeekSSESensitiveEvent(protocol deepSeekSSESensitiveProtocol, event [][]byte) deepSeekSSESensitiveEventAnalysis {
	eventName := ""
	dataLines := make([]string, 0, 1)
	for _, wire := range event {
		line := strings.TrimSpace(string(deepSeekSSEWireLineContent(wire)))
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if data, ok := extractOpenAISSEDataLine(string(deepSeekSSEWireLineContent(wire))); ok {
			dataLines = append(dataLines, data)
		}
	}
	payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
	switch protocol {
	case deepSeekSSESensitiveProtocolChat:
		return analyzeDeepSeekChatSSESensitiveEvent(payload)
	case deepSeekSSESensitiveProtocolResponses:
		return analyzeDeepSeekResponsesSSESensitiveEvent(eventName, payload)
	case deepSeekSSESensitiveProtocolAnthropic:
		return analyzeDeepSeekAnthropicSSESensitiveEvent(eventName, payload)
	default:
		return deepSeekSSESensitiveEventAnalysis{}
	}
}

func analyzeDeepSeekChatSSESensitiveEvent(payload string) deepSeekSSESensitiveEventAnalysis {
	var analysis deepSeekSSESensitiveEventAnalysis
	if payload == "[DONE]" {
		analysis.terminal = true
		return analysis
	}
	if !gjson.Valid(payload) {
		return analysis
	}
	for choiceOffset, choice := range gjson.Get(payload, "choices").Array() {
		choiceIndex := choice.Get("index")
		index := fmt.Sprintf("%d", choiceOffset)
		if choiceIndex.Exists() {
			index = choiceIndex.Raw
		}
		choicePrefix := "chat:choice:" + index + ":"
		for _, field := range []string{"content", "reasoning_content", "refusal"} {
			if value := choice.Get("delta." + field); value.Type == gjson.String {
				analysis.fragments = append(analysis.fragments, deepSeekSSESensitiveFragment{
					streamKey: choicePrefix + field,
					value:     value.String(),
				})
			}
		}
		for toolOffset, toolCall := range choice.Get("delta.tool_calls").Array() {
			toolIndex := toolCall.Get("index")
			index := fmt.Sprintf("%d", toolOffset)
			if toolIndex.Exists() {
				index = toolIndex.Raw
			}
			for _, field := range []string{"name", "arguments"} {
				if value := toolCall.Get("function." + field); value.Type == gjson.String {
					analysis.fragments = append(analysis.fragments, deepSeekSSESensitiveFragment{
						streamKey: choicePrefix + "tool:" + index + ":" + field,
						value:     value.String(),
					})
				}
			}
		}
		finishReason := choice.Get("finish_reason")
		if finishReason.Exists() && finishReason.Type != gjson.Null && strings.TrimSpace(finishReason.String()) != "" {
			analysis.endStreamPrefix = append(analysis.endStreamPrefix, choicePrefix)
		}
	}
	return analysis
}

func deepSeekResponsesSSESensitiveStreamKey(eventType string, payload []byte) string {
	itemID := strings.TrimSpace(gjson.GetBytes(payload, "item_id").String())
	if itemID == "" {
		itemID = strings.TrimSpace(gjson.GetBytes(payload, "item.id").String())
	}
	identityKind := "item"
	identity := itemID
	if identity == "" {
		identityKind = "call"
		identity = strings.TrimSpace(gjson.GetBytes(payload, "call_id").String())
	}
	if identity == "" {
		identityKind = "none"
	}
	index := func(path string) string {
		value := gjson.GetBytes(payload, path)
		if !value.Exists() {
			return "0"
		}
		return value.Raw
	}
	family := strings.TrimSuffix(strings.TrimSuffix(eventType, ".delta"), ".done")
	return fmt.Sprintf(
		"responses|family:%q|identity-kind:%q|identity:%q|output:%q|content:%q|summary:%q",
		family,
		identityKind,
		identity,
		index("output_index"),
		index("content_index"),
		index("summary_index"),
	)
}

func analyzeDeepSeekResponsesSSESensitiveEvent(eventName, payload string) deepSeekSSESensitiveEventAnalysis {
	var analysis deepSeekSSESensitiveEventAnalysis
	if !gjson.Valid(payload) {
		return analysis
	}
	payloadBytes := []byte(payload)
	eventType := strings.TrimSpace(gjson.GetBytes(payloadBytes, "type").String())
	if eventType == "" {
		eventType = strings.TrimSpace(eventName)
	}
	streamKey := deepSeekResponsesSSESensitiveStreamKey(eventType, payloadBytes)
	if strings.HasSuffix(eventType, ".delta") {
		if delta := gjson.GetBytes(payloadBytes, "delta"); delta.Type == gjson.String {
			analysis.fragments = append(analysis.fragments, deepSeekSSESensitiveFragment{streamKey: streamKey, value: delta.String()})
		}
	}
	if strings.HasSuffix(eventType, ".done") {
		analysis.endStreamExact = append(analysis.endStreamExact, streamKey)
	}
	analysis.terminal = openAIStreamEventTypeIsTerminal(eventType)
	return analysis
}

func analyzeDeepSeekAnthropicSSESensitiveEvent(eventName, payload string) deepSeekSSESensitiveEventAnalysis {
	var analysis deepSeekSSESensitiveEventAnalysis
	if !gjson.Valid(payload) {
		return analysis
	}
	eventType := strings.TrimSpace(gjson.Get(payload, "type").String())
	if eventType == "" {
		eventType = strings.TrimSpace(eventName)
	}
	index := "0"
	if value := gjson.Get(payload, "index"); value.Exists() {
		index = value.Raw
	}
	streamPrefix := "anthropic:block:" + index + ":"
	if eventType == "content_block_delta" {
		for _, field := range []string{"text", "thinking", "partial_json", "signature"} {
			if value := gjson.Get(payload, "delta."+field); value.Type == gjson.String {
				analysis.fragments = append(analysis.fragments, deepSeekSSESensitiveFragment{
					streamKey: streamPrefix + field,
					value:     value.String(),
				})
			}
		}
	}
	if eventType == "content_block_stop" {
		analysis.endStreamPrefix = append(analysis.endStreamPrefix, streamPrefix)
	}
	analysis.terminal = anthropicStreamEventIsTerminal(eventType, payload)
	return analysis
}
