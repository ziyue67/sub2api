package prismbridge

import (
	"context"
	"errors"
	"testing"
)

type memoryJournal struct {
	entries []Pending
	cleared bool
}

func (j *memoryJournal) Save(_ context.Context, p Pending) error {
	j.entries = append(j.entries, p)
	return nil
}
func (j *memoryJournal) Clear(_ context.Context) error { j.cleared = true; return nil }

type fakeTransport struct {
	starts    int
	statuses  int
	startErr  error
	statusErr error
}

func (f *fakeTransport) Start(_ context.Context, _ Request) (StartResult, error) {
	f.starts++
	if f.startErr != nil {
		return StartResult{}, f.startErr
	}
	return StartResult{RequestID: "r1", TurnState: "t1"}, nil
}
func (f *fakeTransport) Status(_ context.Context, _ string, state string) (StatusResult, error) {
	f.statuses++
	if f.statusErr != nil {
		return StatusResult{}, f.statusErr
	}
	return StatusResult{TurnState: state, Terminal: &Response{Text: "answer"}}, nil
}

func TestBuildPromptPreservesHistoryAndToolResults(t *testing.T) {
	system, user, err := BuildPrompt(Request{Instructions: "be precise", Input: []any{
		map[string]any{"type": "message", "role": "user", "content": "hello"},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "calc", "arguments": "{\"x\":1}"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "content": "2"},
	}, Tools: []Tool{{Name: "calc", Parameters: map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if system == "" || user == "" || !contains(user, "c1") || !contains(system, "calc") {
		t.Fatalf("history/tool lost: system=%q user=%q", system, user)
	}
}

func TestUnknownAndDuplicateCallsFailClosed(t *testing.T) {
	_, err := NormalizeInput([]any{map[string]any{"type": "function_call", "call_id": "x", "name": "a", "arguments": "{}"}, map[string]any{"type": "function_call", "call_id": "x", "name": "b", "arguments": "{}"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate call_id error=%v", err)
	}
	_, err = ParseOutput(`{"tool_call":{"name":"missing","arguments":{}}}`, []Tool{{Name: "known"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown tool error=%v", err)
	}
}

func TestClientDoesNotReplayAmbiguousStart(t *testing.T) {
	j := &memoryJournal{}
	f := &fakeTransport{startErr: errors.New("timeout")}
	c := &Client{Transport: f, Journal: j}
	_, err := c.Do(context.Background(), Request{Model: "prism-sol", Input: "hi"})
	if !errors.Is(err, ErrPending) || f.starts != 1 {
		t.Fatalf("err=%v starts=%d", err, f.starts)
	}
	if len(j.entries) != 1 || j.entries[0].Stage != "submitting" || j.cleared {
		t.Fatalf("journal=%+v cleared=%v", j.entries, j.cleared)
	}
}

func TestClientUpdatesTurnStateAndClearsOnlyOnTerminal(t *testing.T) {
	j := &memoryJournal{}
	f := &fakeTransport{}
	c := &Client{Transport: f, Journal: j, MaxPoll: 1}
	got, err := c.Do(context.Background(), Request{Model: "prism-sol", Input: "hi"})
	if err != nil || got.Text != "answer" || f.starts != 1 || f.statuses != 1 || !j.cleared {
		t.Fatalf("got=%+v err=%v transport=%+v journal=%+v", got, err, f, j)
	}
}

func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
