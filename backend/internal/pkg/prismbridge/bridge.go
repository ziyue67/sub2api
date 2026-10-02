// Package prismbridge contains the protocol boundary between OpenAI Responses
// requests and Prism's start/poll agent protocol. It deliberately has no
// browser, credential, or local-tool implementation.
package prismbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrPending = errors.New("prism request outcome is unknown; pending journal retained")
	ErrInvalid = errors.New("invalid Prism bridge request")
)

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Custom      bool           `json:"custom,omitempty"`
}

type Request struct {
	Model        string
	Instructions string
	Input        any
	Tools        []Tool
}

type Item struct {
	Type      string
	Role      string
	Text      string
	CallID    string
	Name      string
	Arguments string
	Input     string
}

type ToolCall struct {
	CallID    string
	Name      string
	Arguments string
	Custom    bool
	Input     string
}

type Response struct {
	Model    string
	Text     string
	ToolCall *ToolCall
	// Usage is intentionally nil. Prism does not expose authoritative usage.
	Usage any
}

func NormalizeInput(input any) ([]Item, error) {
	if input == nil {
		return nil, ErrInvalid
	}
	if text, ok := input.(string); ok {
		return []Item{{Type: "message", Role: "user", Text: text}}, nil
	}
	items, ok := input.([]any)
	if !ok {
		if typed, ok2 := input.([]Item); ok2 {
			return validateItems(typed)
		}
		return nil, ErrInvalid
	}
	out := make([]Item, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, ErrInvalid
		}
		item := Item{Type: stringValue(obj["type"]), Role: stringValue(obj["role"]), CallID: stringValue(obj["call_id"]), Name: stringValue(obj["name"]), Arguments: stringValue(obj["arguments"]), Input: stringValue(obj["input"])}
		if item.Type == "" {
			item.Type = "message"
		}
		item.Text = contentText(obj["content"])
		if item.Text == "" {
			item.Text = stringValue(obj["text"])
		}
		out = append(out, item)
	}
	return validateItems(out)
}

func validateItems(items []Item) ([]Item, error) {
	seen := map[string]bool{}
	for i := range items {
		if items[i].Type == "message" {
			if items[i].Role == "" {
				items[i].Role = "user"
			}
			if items[i].Role != "system" && items[i].Role != "developer" && items[i].Role != "user" && items[i].Role != "assistant" {
				return nil, ErrInvalid
			}
		} else if items[i].Type == "function_call" || items[i].Type == "custom_tool_call" {
			if items[i].CallID == "" || items[i].Name == "" || (items[i].Arguments == "" && items[i].Input == "") {
				return nil, ErrInvalid
			}
			if seen[items[i].CallID] {
				return nil, fmt.Errorf("%w: duplicate call_id", ErrInvalid)
			}
			seen[items[i].CallID] = true
		} else if items[i].Type == "function_call_output" || items[i].Type == "custom_tool_call_output" {
			if items[i].CallID == "" {
				return nil, ErrInvalid
			}
		} else if items[i].Type != "additional_tools" {
			return nil, fmt.Errorf("%w: unsupported item type %s", ErrInvalid, items[i].Type)
		}
	}
	return items, nil
}

func BuildPrompt(req Request) (string, string, error) {
	items, err := NormalizeInput(req.Input)
	if err != nil {
		return "", "", err
	}
	var system []string
	if strings.TrimSpace(req.Instructions) != "" {
		system = append(system, req.Instructions)
	}
	var transcript []string
	for _, item := range items {
		switch item.Type {
		case "message":
			transcript = append(transcript, "["+item.Role+"]\n"+item.Text)
		case "function_call":
			transcript = append(transcript, "[assistant function_call "+item.Name+"]\n"+item.Arguments)
		case "custom_tool_call":
			transcript = append(transcript, "[assistant custom_tool_call "+item.Name+"]\n"+item.Input)
		case "function_call_output", "custom_tool_call_output":
			transcript = append(transcript, "[tool result "+item.CallID+"]\n"+item.Text)
		}
	}
	if len(req.Tools) > 0 {
		encoded, _ := json.Marshal(req.Tools)
		system = append(system, "Available tools (the executor owns execution; emit one JSON action and never execute locally):\n"+string(encoded))
	}
	return strings.Join(system, "\n\n"), strings.Join(transcript, "\n\n"), nil
}

func ParseOutput(text string, tools []Tool) (Response, error) {
	trimmed := strings.TrimSpace(text)
	var envelope struct {
		ToolCall *struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
			Input     string `json:"input"`
			CallID    string `json:"call_id"`
			Custom    bool   `json:"custom"`
		} `json:"tool_call"`
		Done string `json:"done"`
	}
	if json.Unmarshal([]byte(trimmed), &envelope) == nil && envelope.ToolCall != nil {
		call := envelope.ToolCall
		for _, tool := range tools {
			if tool.Name == call.Name {
				args := ""
				if call.Arguments != nil {
					encoded, _ := json.Marshal(call.Arguments)
					args = string(encoded)
				}
				if tool.Custom {
					args = call.Input
				}
				return Response{ToolCall: &ToolCall{CallID: call.CallID, Name: call.Name, Arguments: args, Input: call.Input, Custom: tool.Custom || call.Custom}}, nil
			}
		}
		return Response{}, fmt.Errorf("%w: unknown tool", ErrInvalid)
	}
	if envelope.Done != "" {
		return Response{Text: envelope.Done}, nil
	}
	return Response{Text: text}, nil
}

type StartResult struct {
	RequestID string
	TurnState string
	Terminal  *Response
}

type StatusResult struct {
	TurnState string
	Terminal  *Response
}

type Transport interface {
	Start(context.Context, Request) (StartResult, error)
	Status(context.Context, string, string) (StatusResult, error)
}

type Journal interface {
	Save(context.Context, Pending) error
	Clear(context.Context) error
}

type Pending struct {
	RequestID string `json:"request_id"`
	TurnState string `json:"turn_state"`
	Stage     string `json:"stage"`
}

type Client struct {
	Transport Transport
	Journal   Journal
	PollEvery time.Duration
	MaxPoll   int
}

func (c *Client) Do(ctx context.Context, req Request) (Response, error) {
	if c == nil || c.Transport == nil || c.Journal == nil {
		return Response{}, ErrInvalid
	}
	// Persist before start. Any ambiguous start failure therefore fails closed.
	if err := c.Journal.Save(ctx, Pending{Stage: "submitting"}); err != nil {
		return Response{}, err
	}
	started, err := c.Transport.Start(ctx, req)
	if err != nil {
		return Response{}, ErrPending
	}
	if started.Terminal != nil {
		_ = c.Journal.Clear(ctx)
		return *started.Terminal, nil
	}
	if started.RequestID == "" || started.TurnState == "" {
		return Response{}, ErrPending
	}
	if err := c.Journal.Save(ctx, Pending{RequestID: started.RequestID, TurnState: started.TurnState, Stage: "polling"}); err != nil {
		return Response{}, err
	}
	max := c.MaxPoll
	if max <= 0 {
		max = 120
	}
	wait := c.PollEvery
	if wait <= 0 {
		wait = 500 * time.Millisecond
	}
	turnState := started.TurnState
	for i := 0; i < max; i++ {
		status, statusErr := c.Transport.Status(ctx, started.RequestID, turnState)
		if statusErr != nil {
			return Response{}, ErrPending
		}
		if status.TurnState != "" {
			turnState = status.TurnState
			if err := c.Journal.Save(ctx, Pending{RequestID: started.RequestID, TurnState: turnState, Stage: "polling"}); err != nil {
				return Response{}, err
			}
		}
		if status.Terminal != nil {
			_ = c.Journal.Clear(ctx)
			return *status.Terminal, nil
		}
		select {
		case <-ctx.Done():
			return Response{}, ErrPending
		case <-time.After(wait):
		}
	}
	return Response{}, ErrPending
}

func stringValue(v any) string { s, _ := v.(string); return s }

func contentText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	parts, ok := v.([]any)
	if !ok {
		return ""
	}
	var out []string
	for _, raw := range parts {
		if obj, ok := raw.(map[string]any); ok {
			if s := stringValue(obj["text"]); s != "" {
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, "")
}
