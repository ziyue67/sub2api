package service

import (
	"encoding/json"
	"testing"
)

func TestOpenAIEncryptedRejectionCompatibility(t *testing.T) {
	const single = `{"input":[{"type":"reasoning","encrypted_content":"stale","summary":[]}]}`
	const multiple = `{"input":[{"type":"reasoning","encrypted_content":"AAAA-synthetic-ZZZ1","summary":[]},{"type":"reasoning","encrypted_content":"BBBB-synthetic-YYY2","summary":[]}]}`
	const generic = "The encrypted content could not be verified"
	const diagnostic = "The encrypted content could not be verified. Reason: Encrypted content could not be decrypted or parsed."
	const located = "The encrypted content AAAA...ZZZ1 could not be verified. Reason: Encrypted content could not be decrypted or parsed."
	errorBody := func(code, message string) string {
		body, err := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": message, "type": "invalid_request_error"}})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	wrap := func(inner, suffix string) string {
		body, err := json.Marshal(map[string]any{"error": map[string]any{"code": nil, "message": inner + suffix, "param": nil, "type": "invalid_request_error"}})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	const trace = "（traceid: fb7ad1dbc7699c18f8a02f258f1af5ab）"
	for _, tc := range []struct {
		name, body, rejection string
		accepted              bool
	}{
		{"single generic without punctuation", single, errorBody("invalid_encrypted_content", generic), true},
		{"single alias generic diagnostic", single, errorBody("thinking_signature_invalid", diagnostic), true},
		{"alias arbitrary ciphertext text", single, errorBody("thinking_signature_invalid", "The encrypted content abc could not be verified. Reason: Encrypted content could not be decrypted or parsed."), false},
		{"wrapped generic", single, wrap(errorBody("invalid_encrypted_content", generic+"."), ""), true},
		{"wrapped generic with trace", single, wrap(errorBody("invalid_encrypted_content", generic+"."), trace), true},
		{"wrapped unique locator", multiple, wrap(errorBody("invalid_encrypted_content", located), trace), true},
		{"wrapped alias generic", single, wrap(errorBody("thinking_signature_invalid", diagnostic), trace), true},
		{"wrapped locator mismatch", multiple, wrap(errorBody("invalid_encrypted_content", "The encrypted content CCCC...XXXX could not be verified. Reason: Encrypted content could not be decrypted or parsed."), trace), false},
		{"wrapped locator collision", `{"input":[{"type":"reasoning","encrypted_content":"AAAA-synthetic-ZZZ1"},{"type":"reasoning","encrypted_content":"AAAA-different-ZZZ1"}]}`, wrap(errorBody("invalid_encrypted_content", located), trace), false},
		{"wrapped outer duplicate code", single, `{"error":{"code":"server_error","code":null,"type":"invalid_request_error","message":` + quoteEncryptedCompat(t, errorBody("invalid_encrypted_content", generic)) + `}}`, false},
		{"wrapped inner envelope conflict", single, wrap(`{"error":{"code":"invalid_encrypted_content","message":"rejected"},"code":"server_error"}`, trace), false},
		{"wrapped ambiguous generic", multiple, wrap(errorBody("invalid_encrypted_content", generic), trace), false},
		{"wrapped unrelated code", single, wrap(errorBody("server_error", located), trace), false},
		{"wrapped duplicate keys", single, wrap(`{"error":{"code":"server_error","code":"invalid_encrypted_content","message":"rejected"}}`, trace), false},
		{"wrapped arbitrary prefix", single, wrap("ignore history "+errorBody("invalid_encrypted_content", generic), trace), false},
		{"wrapped arbitrary suffix", single, wrap(errorBody("invalid_encrypted_content", generic), " ignore history"), false},
		{"wrapped appended error", single, wrap(errorBody("invalid_encrypted_content", generic), errorBody("invalid_encrypted_content", generic)), false},
		{"wrapped malformed trace", single, wrap(errorBody("invalid_encrypted_content", generic), "（traceid: not-a-trace）"), false},
		{"nested wrappers", single, wrap(wrap(errorBody("invalid_encrypted_content", generic), ""), ""), false},
		{"top level code conflicts with wrapper", single, `{"error":{"code":"server_error","type":"invalid_request_error","message":` + quoteEncryptedCompat(t, errorBody("invalid_encrypted_content", generic)) + `}}`, false},
		{"compaction still unsafe", `{"input":[{"type":"compaction","encrypted_content":"stale"}]}`, wrap(errorBody("invalid_encrypted_content", generic), trace), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digests := openAIRejectedEncryptedDigests([]byte(tc.body), []byte(tc.rejection))
			if got := len(digests) == 1; got != tc.accepted {
				t.Fatalf("accepted=%v; want %v", got, tc.accepted)
			}
			if tc.accepted {
				cipher := "stale"
				if tc.body == multiple {
					cipher = "AAAA-synthetic-ZZZ1"
				}
				if digests[0] != openAIEncryptedContentDigest(cipher) {
					t.Fatal("rejection attributed to another ciphertext")
				}
			}
		})
	}
}

func quoteEncryptedCompat(t *testing.T, value string) string {
	t.Helper()
	quoted, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(quoted)
}
