package cyberevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSnapshotPreservesVisibleStructuredInput(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"system","content":"system context"},{"role":"assistant","tool_calls":[{"id":"call_1","function":{"name":"lookup","arguments":"{\"token\":\"secret-value\",\"query\":\"synthetic\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"synthetic result"},{"role":"user","content":"current input"}],"api_key":"never-store"}`,
		`{"instructions":"system context","input":[{"role":"user","content":[{"type":"input_text","text":"current input"}]},{"type":"function_call_output","call_id":"call_1","output":"synthetic result"}],"previous_response_id":"resp_synthetic"}`,
	} {
		p := Snapshot([]byte(body), "upstream cyber_policy reason")
		if !json.Valid([]byte(p.Input)) || !strings.Contains(p.Input, "current input") || !strings.Contains(p.Input, "call_1") {
			t.Fatalf("lost structured input: %s", p.Input)
		}
		if strings.Contains(p.Input, "never-store") || strings.Contains(p.Input, "secret-value") || strings.Contains(p.Input, "upstream cyber_policy") {
			t.Fatal("secret or upstream error stored as input")
		}
		if p.RedactionVersion == "" || p.Truncated {
			t.Fatal("invalid metadata")
		}
	}
}

func TestSnapshotRedactionAndBounds(t *testing.T) {
	body := `{"input":{"Authorization":"Bearer synthetic-secret","api_key":"synthetic-secret","password":"synthetic-secret","cookie":"synthetic-secret","ticket":"synthetic-secret","content":"email person@example.test ip 192.0.2.10 ip:port 192.0.2.11:8080 ipv6 [2001:db8::1]:443 token=synthetic-secret sk-syntheticKey012345678901234567 https://example.test/?ticket=synthetic-secret"}}`
	p := Snapshot([]byte(body), `Authorization: Bearer synthetic-secret`)
	for _, s := range []string{"synthetic-secret", "person@example.test", "192.0.2.10", "192.0.2.11:8080", "2001:db8::1", "sk-syntheticKey"} {
		if strings.Contains(p.Input+p.Reason, s) {
			t.Fatalf("redaction missed %s", s)
		}
	}
	p = Snapshot([]byte(`{"input":"`+strings.Repeat("长", 100000)+`"}`), "reason")
	if !p.Truncated || len(p.Input) > MaxStoredBytes || !json.Valid([]byte(p.Input)) {
		t.Fatal("unbounded or invalid truncated input")
	}
	p = Snapshot([]byte(`{"input":"`+strings.Repeat("a", MaxParseBytes)+`"}`), "reason")
	if !p.Truncated || p.Complete || p.Omission == "" {
		t.Fatal("oversized input must be explicit")
	}
}

func TestSnapshotKeepsOnlyExplicitRedactedSessionReference(t *testing.T) {
	p := SnapshotWithSession([]byte(`{"input":"synthetic"}`), "reason", "sess_synthetic-1")
	if p.SessionID != "sess_synthetic-1" {
		t.Fatalf("explicit session reference lost: %q", p.SessionID)
	}
	p = SnapshotWithSession([]byte(`{"input":"synthetic"}`), "reason", "person@example.test")
	if strings.Contains(p.SessionID, "person@example.test") {
		t.Fatal("session reference was not redacted")
	}
	if Snapshot([]byte(`{"input":"synthetic"}`), "reason").SessionID != "" {
		t.Fatal("header-derived session must be opt-in")
	}
}

func TestSnapshotNeverTreatsErrorAsInput(t *testing.T) {
	for _, b := range []string{"", `{"error":{"code":"cyber_policy","message":"rejected by content policy"}}`, `{"input":`} {
		p := Snapshot([]byte(b), "upstream reason")
		if p.Input != "" || p.Complete || p.Omission == "" {
			t.Fatal("missing input falsely reconstructed")
		}
	}
}
