package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIssue600NestedIdentityRawAndMapProjection(t *testing.T) {
	body := []byte(`{"type":"response.create","prompt_cache_key":"fixture-session","client_metadata":{"session_id":"fixture-session","thread_id":"fixture-thread","x-codex-turn-metadata":"{\"thread_id\":\"fixture-thread\"}","label":"preserved","sequence":9007199254740993},"response":{"prompt_cache_key":"fixture-session","client_metadata":{"session_id":"fixture-session","thread_id":"fixture-thread","x-codex-turn-metadata":"{\"thread_id\":\"fixture-thread\"}","label":"preserved","sequence":9007199254740993},"input":[{"role":"user","content":"nested input"}],"instructions":"nested instructions"}}`)
	for _, namespace := range []string{"apikey", "oauth", "connection"} {
		t.Run(namespace, func(t *testing.T) {
			a := &Account{Type: AccountTypeAPIKey, Platform: PlatformOpenAI}
			switch namespace {
			case "oauth":
				a.Type = AccountTypeOAuth
				a.Credentials = map[string]any{"chatgpt_account_id": "fixture-account"}
			case "connection":
				a.AuthConnectionID = "11111111-1111-4111-8111-111111111111"
			}
			raw, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, a, 77)
			require.NoError(t, err)
			require.Equal(t, namespace != "apikey", changed)
			var mapped map[string]any
			require.NoError(t, decodeOpenAIJSONUseNumber(body, &mapped))
			require.Equal(t, changed, applyCodexAccountIdentityClientMetadataMap(mapped, a, 77))
			encoded, err := json.Marshal(mapped)
			require.NoError(t, err)
			require.JSONEq(t, string(raw), string(encoded), "map/raw projectors must preserve equivalent schema and scope")
			for _, field := range []string{"thread_id", "session_id"} {
				root := gjson.GetBytes(raw, "client_metadata."+field).String()
				nested := gjson.GetBytes(raw, "response.client_metadata."+field).String()
				require.Equal(t, root, nested)
				if namespace != "apikey" {
					require.NotEqual(t, gjson.GetBytes(body, "client_metadata."+field).String(), nested)
				}
			}
			require.Equal(t, gjson.GetBytes(raw, "client_metadata.session_id").String(), gjson.GetBytes(raw, "response.prompt_cache_key").String())
			require.Equal(t, gjson.GetBytes(body, "response.input").Raw, gjson.GetBytes(raw, "response.input").Raw)
			require.Equal(t, "preserved", gjson.GetBytes(raw, "response.client_metadata.label").String())
			require.Equal(t, "9007199254740993", gjson.GetBytes(raw, "response.client_metadata.sequence").Raw, "non-identity metadata must not lose integer precision")
			// A different account/authorized connection must derive from the same source.
			if namespace != "apikey" {
				b := *a
				b.AuthConnectionID = "22222222-2222-4222-8222-222222222222"
				other, _, err := applyCodexAccountIdentityClientMetadataRaw(body, &b, 77)
				require.NoError(t, err)
				require.NotEqual(t, gjson.GetBytes(raw, "response.client_metadata.thread_id").String(), gjson.GetBytes(other, "response.client_metadata.thread_id").String())
			}
		})
	}
}

func TestIssue600NestedIdentityResolutionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     OpenAIClientSessionIdentityStatus
		kind       string
	}{
		{"equal_thread", `{"type":"response.create","client_metadata":{"thread_id":"thread"},"response":{"client_metadata":{"thread_id":"thread"}}}`, OpenAIClientSessionIdentityResolved, "thread"},
		{"conflicting_thread", `{"type":"response.create","client_metadata":{"thread_id":"root"},"response":{"client_metadata":{"thread_id":"nested"}}}`, OpenAIClientSessionIdentityConflict, "thread"},
		{"invalid_hidden_root", `{"type":"response.create","client_metadata":{"thread_id":123},"response":{"client_metadata":{"thread_id":"nested"}}}`, OpenAIClientSessionIdentityInvalid, "thread"},
		{"invalid_hidden_nested", `{"type":"response.create","client_metadata":{"thread_id":"root"},"response":{"client_metadata":{"thread_id":false}}}`, OpenAIClientSessionIdentityInvalid, "thread"},
		{"equal_session", `{"type":"response.create","client_metadata":{"session_id":"same"},"response":{"client_metadata":{"session_id":"same"}}}`, OpenAIClientSessionIdentityResolved, "session"},
		{"conflicting_session", `{"type":"response.create","client_metadata":{"session_id":"root"},"response":{"client_metadata":{"session_id":"nested"}}}`, OpenAIClientSessionIdentityConflict, "session"},
		{"root_thread_precedes_nested_session", `{"type":"response.create","client_metadata":{"thread_id":"root"},"response":{"client_metadata":{"session_id":"nested"}}}`, OpenAIClientSessionIdentityResolved, "thread"},
		{"nested_thread_precedes_invalid_root_session", `{"type":"response.create","client_metadata":{"session_id":false},"response":{"client_metadata":{"thread_id":"nested"}}}`, OpenAIClientSessionIdentityResolved, "thread"},
		{"empty_envelope_does_not_hide_root", `{"type":"response.create","client_metadata":{"thread_id":"root"},"response":{"input":[]}}`, OpenAIClientSessionIdentityResolved, "thread"},
		{"non_response_keeps_existing_view", `{"client_metadata":{"session_id":"root"},"response":{"client_metadata":{"thread_id":"not-an-envelope"}}}`, OpenAIClientSessionIdentityResolved, "session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			got := InspectOpenAIClientSessionIdentity(c, []byte(tc.body))
			require.Equal(t, tc.status, got.Status)
			require.Equal(t, tc.kind, got.Kind)
			require.Equal(t, OpenAIClientSessionIdentitySourceBody, got.Source)
		})
	}
}
