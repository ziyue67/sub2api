package apicompat

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const deepSeekToolTextLimit = 1 << 20

// DeepSeekToolTextDecoder repairs a complete DSML tool block leaked into content.
// It is opt-in for DeepSeek requests with tools, and accepts only declared names.
// Text before the first '<' streams normally; a possible markup suffix is held
// until EOF so truncated or ambiguous calls can never be completed for execution.
type DeepSeekToolTextDecoder struct {
	declared map[string]bool
	prefix   strings.Builder
	pending  strings.Builder
	holding  bool
	native   bool
	overflow bool
}

func NewDeepSeekToolTextDecoder(tools []ChatTool) *DeepSeekToolTextDecoder {
	d := &DeepSeekToolTextDecoder{declared: make(map[string]bool)}
	for _, tool := range tools {
		if tool.Type == "function" && tool.Function != nil {
			d.declared[tool.Function.Name] = true
		}
	}
	return d
}

// Push leaves native tool deltas and usage intact. It never mutates the caller's
// chunk, which may also be consumed by accounting or diagnostic code.
func (d *DeepSeekToolTextDecoder) Push(chunk *ChatCompletionsChunk) *ChatCompletionsChunk {
	out := *chunk
	out.Choices = append([]ChatChunkChoice(nil), chunk.Choices...)
	for i := range out.Choices {
		choice := &out.Choices[i]
		if choice.Index != 0 {
			continue
		}
		if len(choice.Delta.ToolCalls) > 0 {
			d.native = true
		}
		if choice.Delta.Content == nil {
			continue
		}
		text := *choice.Delta.Content
		if !d.holding {
			if idx := strings.IndexByte(text, '<'); idx >= 0 {
				d.holding = true
				_, _ = d.prefix.WriteString(text[:idx])
				d.appendPending(text[idx:])
				text = text[:idx]
			} else {
				_, _ = d.prefix.WriteString(text)
			}
		} else {
			d.appendPending(text)
			text = ""
		}
		choice.Delta.Content = &text
	}
	return &out
}

func (d *DeepSeekToolTextDecoder) appendPending(text string) {
	if d.overflow {
		return
	}
	if d.pending.Len()+len(text) > deepSeekToolTextLimit {
		d.overflow = true
		d.pending.Reset()
		return
	}
	_, _ = d.pending.WriteString(text)
}

// Finish returns only content not already emitted by Push, plus recovered calls.
// A native call alongside text markup is ambiguous and must not be duplicated.
func (d *DeepSeekToolTextDecoder) Finish(finishReason string) (*ChatCompletionsChunk, error) {
	if d.overflow {
		return nil, fmt.Errorf("DeepSeek tool markup exceeded buffer limit")
	}
	pending := d.pending.String()
	text, calls, err := d.recoverText(d.prefix.String(), pending)
	if err != nil {
		return nil, err
	}
	if len(calls) > 0 && (d.native || (finishReason != "stop" && finishReason != "tool_calls")) {
		return nil, fmt.Errorf("ambiguous or incomplete DeepSeek text tool calls")
	}
	if text == "" && len(calls) == 0 {
		return nil, nil
	}
	reason := finishReason
	if len(calls) > 0 {
		reason = "tool_calls"
	}
	return &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Index: 0, Delta: ChatDelta{Content: &text, ToolCalls: calls}, FinishReason: &reason}}}, nil
}

// DSML values are raw strings, not XML escaped character data. Parse only the
// protocol delimiters instead of using an XML decoder (which corrupts '<' and
// '&' in command/code arguments). No unescaping or guessing missing names/types.
var (
	dsToolOpen    = regexp.MustCompile(`^<(?:｜DSML｜|\|DSML\|)?tool_calls>\s*`)
	dsToolClose   = regexp.MustCompile(`^</(?:｜DSML｜|\|DSML\|)?tool_calls>`)
	dsInvokeOpen  = regexp.MustCompile(`^<(?:｜DSML｜|\|DSML\|)?invoke name="([^"]+)">\s*`)
	dsInvokeClose = regexp.MustCompile(`^</(?:｜DSML｜|\|DSML\|)?invoke>\s*`)
	dsParamOpen   = regexp.MustCompile(`^<(?:｜DSML｜|\|DSML\|)?parameter name="([^"]+)" string="(true|false)">`)
	dsParamClose  = regexp.MustCompile(`</(?:｜DSML｜|\|DSML\|)?parameter>`)
)

func (d *DeepSeekToolTextDecoder) recoverText(prefix, pending string) (string, []ChatToolCall, error) {
	// Code samples are text, never executable tools. Recognize only standalone
	// protocol lines outside Markdown fences, including orphan parameter fragments.
	fenceChar, fenceLen := byte(0), 0
	offset := 0
	all := prefix + pending
	for _, line := range strings.SplitAfter(all, "\n") {
		trim := strings.TrimLeft(line, " ")
		// Four-space and tab-indented code blocks are examples, too.
		indent := len(line) - len(trim)
		if indent <= 3 && (strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~")) {
			ch := trim[0]
			n := 0
			for n < len(trim) && trim[n] == ch {
				n++
			}
			if fenceChar == 0 {
				fenceChar, fenceLen = ch, n
			} else if ch == fenceChar && n >= fenceLen && strings.TrimSpace(trim[n:]) == "" {
				fenceChar, fenceLen = 0, 0
			}
		}
		if fenceChar == 0 && indent <= 3 && strings.HasPrefix(trim, "<") && isDeepSeekToolMarkup(trim) {
			start := offset + strings.Index(line, "<") - len(prefix)
			if start < 0 {
				return "", nil, fmt.Errorf("invalid DeepSeek tool markup boundary")
			}
			calls, err := d.parseCalls(strings.TrimSpace(pending[start:]))
			if err != nil {
				return "", nil, err
			}
			return pending[:start], calls, nil
		}
		offset += len(line)
	}
	return pending, nil, nil
}

func isDeepSeekToolMarkup(text string) bool {
	if strings.Contains(strings.SplitN(text, ">", 2)[0], "DSML") {
		return true
	}
	for _, tag := range []string{"tool_calls", "invoke", "parameter"} {
		if strings.HasPrefix(text, "<"+tag+">") || strings.HasPrefix(text, "<"+tag+" ") || strings.HasPrefix(text, "</"+tag+">") {
			return true
		}
	}
	return false
}

func (d *DeepSeekToolTextDecoder) parseCalls(text string) ([]ChatToolCall, error) {
	invalid := func() ([]ChatToolCall, error) {
		return nil, fmt.Errorf("malformed or undeclared DeepSeek text tool call")
	}
	opening := dsToolOpen.FindString(text)
	if opening == "" {
		return invalid()
	}
	text = text[len(opening):]
	var calls []ChatToolCall
	for {
		if closing := dsToolClose.FindString(text); closing != "" {
			if len(calls) == 0 || strings.TrimSpace(text[len(closing):]) != "" {
				return invalid()
			}
			return calls, nil
		}
		invoke := dsInvokeOpen.FindStringSubmatch(text)
		if invoke == nil || !d.declared[invoke[1]] {
			return invalid()
		}
		name := invoke[1]
		text = text[len(invoke[0]):]
		args := make(map[string]json.RawMessage)
		for {
			if closing := dsInvokeClose.FindString(text); closing != "" {
				text = text[len(closing):]
				break
			}
			param := dsParamOpen.FindStringSubmatch(text)
			if param == nil || param[1] == "" {
				return invalid()
			}
			if _, duplicate := args[param[1]]; duplicate {
				return invalid()
			}
			text = text[len(param[0]):]
			closing := dsParamClose.FindStringIndex(text)
			if closing == nil {
				return invalid()
			}
			value := text[:closing[0]]
			if param[2] == "true" {
				encoded, _ := json.Marshal(value)
				args[param[1]] = encoded
			} else {
				if !json.Valid([]byte(value)) {
					return invalid()
				}
				args[param[1]] = json.RawMessage(value)
			}
			text = strings.TrimLeft(text[closing[1]:], " \t\r\n")
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return invalid()
		}
		idx := len(calls)
		calls = append(calls, ChatToolCall{Index: &idx, ID: "call_" + generateItemID(), Type: "function", Function: ChatFunctionCall{Name: name, Arguments: string(encoded)}})
	}
}

// RecoverMessage applies the same policy to a non-streaming Chat response.
func (d *DeepSeekToolTextDecoder) RecoverMessage(message *ChatMessage, finishReason string) error {
	text := chatMessageContentText(message.Content)
	d.native = len(message.ToolCalls) > 0
	d.appendPending(text)
	chunk, err := d.Finish(finishReason)
	if err != nil {
		return err
	}
	if chunk == nil {
		return nil
	}
	delta := chunk.Choices[0].Delta
	if len(delta.ToolCalls) > 0 {
		message.Content, _ = json.Marshal(*delta.Content)
		message.ToolCalls = delta.ToolCalls
	}
	return nil
}
