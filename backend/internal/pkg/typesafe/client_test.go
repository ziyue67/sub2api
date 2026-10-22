package typesafe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewSystemOneRequestEditableURL(t *testing.T) {
	for _, tc := range []struct{ name, base, want string }{
		{"empty default", "", DefaultBaseURL + SystemOnePath},
		{"whitespace default", "  ", DefaultBaseURL + SystemOnePath},
		{"default", DefaultBaseURL, DefaultBaseURL + SystemOnePath},
		{"trailing slashes", " https://relay.example/// ", "https://relay.example/v1/systemone"},
		{"api base", "https://relay.example/v1/", "https://relay.example/v1/systemone"},
		{"legacy uppercase version", "https://relay.example/V1/", "https://relay.example/v1/systemone"},
		{"full endpoint", "https://relay.example/v1/systemone/", "https://relay.example/v1/systemone"},
		{"proxy prefix", "https://relay.example/typesafe/", "https://relay.example/typesafe/v1/systemone"},
		{"prefixed api base", "https://relay.example/typesafe/v1/", "https://relay.example/typesafe/v1/systemone"},
		{"prefixed endpoint", "https://relay.example/typesafe/v1/systemone/", "https://relay.example/typesafe/v1/systemone"},
		{"version-like prefix", "https://relay.example/apiv1", "https://relay.example/apiv1/v1/systemone"},
		{"escaped prefix", "https://relay.example/my%20relay/v1", "https://relay.example/my%20relay/v1/systemone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := NewSystemOneRequest(context.Background(), tc.base, "fixture-key", []byte(`{}`))
			require.NoError(t, err)
			require.Equal(t, tc.want, req.URL.String())
			require.Equal(t, http.MethodPost, req.Method)
		})
	}
}

func TestNewSystemOneRequestRejectsUnsafeURLSyntax(t *testing.T) {
	for _, raw := range []string{
		"/v1", "//relay.example", "ftp://relay.example", "file:///etc/hosts",
		"https://user:fixture-password@relay.example", "https://relay.example?token=fixture-secret",
		"https://relay.example?", "https://relay.example#fragment", "https://relay.example:99999",
	} {
		t.Run(raw, func(t *testing.T) {
			req, err := NewSystemOneRequest(context.Background(), raw, "fixture-key", []byte(`{}`))
			require.Error(t, err)
			require.Nil(t, req)
			require.NotContains(t, err.Error(), "fixture-")
		})
	}
}

func TestEvaluateDoesNotDisableTLSVerificationForCustomURL(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("untrusted TLS endpoint must not receive a request")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	result, status, err := Evaluate(context.Background(), &http.Client{Timeout: time.Second}, server.URL+"/v1/", "fixture-key", Request{})
	require.Error(t, err)
	require.Nil(t, result)
	require.Zero(t, status)
}

func TestEvaluateContract(t *testing.T) {
	questions := map[string]Question{"sexual": {Type: "noul", Instructions: "Evaluate sexual content"}, "illicit": {Type: "noul", Instructions: "Evaluate illegal assistance"}}
	for _, tc := range []struct {
		name, response string
		valid          bool
	}{
		{"valid", `{"model":"jev-1.13.0","answers":{"sexual":{"type":"noul","noul":0},"illicit":{"type":"noul","noul":1}}}`, true},
		{"missing_answer", `{"model":"jev-1.13.0","answers":{"sexual":{"type":"noul","noul":0}}}`, false},
		{"missing_probability", `{"model":"jev-1.13.0","answers":{"sexual":{"type":"noul"},"illicit":{"type":"noul","noul":0}}}`, false},
		{"negative", `{"model":"jev-1.13.0","answers":{"sexual":{"type":"noul","noul":-0.1},"illicit":{"type":"noul","noul":0}}}`, false},
		{"above_one", `{"model":"jev-1.13.0","answers":{"sexual":{"type":"noul","noul":1.1},"illicit":{"type":"noul","noul":0}}}`, false},
		{"wrong_type", `{"model":"jev-1.13.0","answers":{"sexual":{"type":"score","noul":0},"illicit":{"type":"noul","noul":0}}}`, false},
		{"missing_model", `{"answers":{"sexual":{"type":"noul","noul":0},"illicit":{"type":"noul","noul":0}}}`, false},
		{"invalid_json", `{bad`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/systemone", r.URL.Path)
				require.Equal(t, "Bearer test-secret", r.Header.Get("Authorization"))
				var input Request
				require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
				require.Equal(t, questions, input.Questions)
				require.Equal(t, "sample", input.State)
				_, err := w.Write([]byte(tc.response))
				require.NoError(t, err)
			}))
			defer server.Close()
			result, status, err := Evaluate(context.Background(), server.Client(), server.URL, "test-secret", Request{Model: "jev-latest", State: "sample", Questions: questions})
			require.Equal(t, 200, status)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, "jev-1.13.0", result.Model)
				require.Equal(t, 1.0, result.Scores["illicit"])
			} else {
				require.Error(t, err)
				require.Nil(t, result)
			}
		})
	}
}

func TestEvaluateHTTPAndTimeoutDoNotExposePayload(t *testing.T) {
	for _, status := range []int{401, 429, 500, 529} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, err := w.Write([]byte("private request text and test-secret"))
			require.NoError(t, err)
		}))
		_, code, err := Evaluate(context.Background(), server.Client(), server.URL, "test-secret", Request{})
		require.Equal(t, status, code)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private")
		require.NotContains(t, err.Error(), "test-secret")
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := Evaluate(ctx, server.Client(), server.URL, "test-secret", Request{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, strings.Contains(err.Error(), "test-secret"))
}
