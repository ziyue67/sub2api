package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func issue600NestedMetadata() map[string]any {
	return map[string]any{
		"thread_id": "fixture-client-thread", "session_id": "fixture-nested-session",
		"installation_id": "fixture-nested-installation", "window_id": "fixture-nested-window", "turn_id": "fixture-nested-turn",
		"x-codex-turn-metadata": `{"thread_id":"fixture-client-thread","session_id":"fixture-nested-session","installation_id":"fixture-nested-installation","window_id":"fixture-nested-window","turn_id":"fixture-nested-turn"}`,
		"fixture_sequence":      json.Number("9007199254740993"),
		"fixture_label":         "preserved",
	}
}
func issue600NestedPayload(t *testing.T, rootMetadata, nestedMetadata any) string {
	t.Helper()
	response := map[string]any{"input": []any{map[string]any{"role": "user", "content": "fixture nested input"}}, "instructions": "preserve nested instructions"}
	body := map[string]any{"type": "response.create", "model": "gpt-5.1", "input": "fixture root input", "response": response}
	if rootMetadata != nil {
		body["client_metadata"] = rootMetadata
	}
	if nestedMetadata != nil {
		response["client_metadata"] = nestedMetadata
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	return string(payload)
}
func issue600AssertNestedProjection(t *testing.T, namespace string, original, projected []byte) {
	t.Helper()
	require.JSONEq(t, gjson.GetBytes(original, "response.input").Raw, gjson.GetBytes(projected, "response.input").Raw, "nested input shape must survive identity projection")
	require.Equal(t, gjson.GetBytes(original, "response.instructions").String(), gjson.GetBytes(projected, "response.instructions").String())
	for _, prefix := range []string{"client_metadata", "response.client_metadata"} {
		before := gjson.GetBytes(original, prefix)
		after := gjson.GetBytes(projected, prefix)
		require.Equal(t, before.Exists(), after.Exists(), "metadata locations must not be created or flattened")
		if !before.Exists() {
			continue
		}
		require.Equal(t, before.Get("fixture_label").String(), after.Get("fixture_label").String())
		require.Equal(t, before.Get("fixture_sequence").Raw, after.Get("fixture_sequence").Raw, "non-identity metadata precision must survive")
		for _, field := range []string{"thread_id", "session_id", "installation_id", "window_id", "turn_id"} {
			raw := before.Get(field).String()
			actual := after.Get(field).String()
			if raw == "" {
				continue
			}
			require.NotEmpty(t, actual)
			if namespace == "none" {
				require.Equal(t, raw, actual)
			} else {
				require.NotEqual(t, raw, actual, "nested outbound identity must be account isolated: %s.%s", prefix, field)
			}
			other := gjson.GetBytes(projected, "client_metadata."+field).String()
			if prefix == "response.client_metadata" && other != "" {
				require.Equal(t, other, actual, "equal original root/nested values must project equally")
			}
			embedded := gjson.Get(after.Get("x-codex-turn-metadata").String(), field).String()
			if embedded != "" {
				require.Equal(t, actual, embedded, "embedded metadata must use the same projection")
			}
		}
	}
	if namespace != "none" {
		for _, raw := range []string{"fixture-client-thread", "fixture-nested-session", "fixture-nested-installation", "fixture-nested-window", "fixture-nested-turn"} {
			require.NotContains(t, string(projected), raw, "original nested identity must not leak upstream")
		}
	}
}

// Every case executes the real handler and forwarder over network sockets. Two
// stable turns complete before the third turn tests omission or rejection.
func TestIssue600NestedIdentityNetwork(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, "legacy"} {
		t.Run(mode, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				t.Run("strict="+strconv.FormatBool(strict), func(t *testing.T) {
					for _, namespace := range []string{"none", "oauth"} {
						t.Run(namespace, func(t *testing.T) {
							for _, scenario := range []string{"nested_header_body", "nested_body_only", "root_nested_consistent", "omitted_nested_identity", "root_hidden_by_empty_envelope", "conflicting_root_nested_thread", "conflicting_root_nested_session", "invalid_nested_identity", "invalid_root_identity", "invalid_nested_metadata", "invalid_root_metadata", "nested_header_conflict", "true_nested_switch", "session_only_consistent", "session_only_switch"} {
								t.Run(scenario, func(t *testing.T) {
									rootFields, nestedFields := issue600NestedMetadata(), issue600NestedMetadata()
									var rootMeta any
									var nestedMeta any = nestedFields
									headers := http.Header{}
									switch scenario {
									case "nested_header_body", "nested_header_conflict":
										headers.Set("thread-id", "fixture-client-thread")
									case "root_nested_consistent", "conflicting_root_nested_thread", "conflicting_root_nested_session", "invalid_nested_identity", "invalid_root_identity", "invalid_nested_metadata", "invalid_root_metadata":
										rootMeta = rootFields
									case "root_hidden_by_empty_envelope":
										rootMeta = rootFields
										nestedMeta = nil
									case "session_only_consistent", "session_only_switch":
										delete(nestedFields, "thread_id")
										delete(nestedFields, "x-codex-turn-metadata")
										rootFields = map[string]any{"session_id": "fixture-nested-session", "fixture_label": "preserved"}
										rootMeta = rootFields
									}
									stable := issue600NestedPayload(t, rootMeta, nestedMeta)
									third := stable
									reject := false
									switch scenario {
									case "omitted_nested_identity":
										third = issue600NestedPayload(t, nil, nil)
									case "conflicting_root_nested_thread":
										rootFields["thread_id"] = "fixture-conflicting-thread"
										reject = true
									case "conflicting_root_nested_session":
										delete(rootFields, "thread_id")
										delete(nestedFields, "thread_id")
										// Bind a session for this case so thread precedence stays intentional.
										stable = issue600NestedPayload(t, rootMeta, nestedMeta)
										rootFields["session_id"] = "fixture-conflicting-session"
										reject = true
									case "invalid_nested_identity":
										nestedFields["thread_id"] = 123
										reject = true
									case "invalid_root_identity":
										rootFields["thread_id"] = "fixture\ninvalid"
										reject = true
									case "invalid_nested_metadata":
										nestedMeta = []string{"invalid"}
										reject = true
									case "invalid_root_metadata":
										rootMeta = "invalid"
										reject = true
									case "nested_header_conflict", "true_nested_switch":
										nestedFields["thread_id"] = "fixture-switched-thread"
										reject = true
									case "session_only_switch":
										rootFields["session_id"] = "fixture-switched-session"
										nestedFields["session_id"] = "fixture-switched-session"
										reject = true
									}
									if reject {
										third = issue600NestedPayload(t, rootMeta, nestedMeta)
									}
									h, up := newIssue600NetworkHarness(t, mode, namespace, strict, headers)
									issue600Send(t, h, stable, 1, false)
									issue600Send(t, h, stable, 2, false)
									issue600Send(t, h, third, 3, reject)
									issue600Finish(t, h, headers)
									frames, outHeaders := up.snapshot()
									want := 3
									if reject {
										want = 2
									}
									require.Len(t, frames, want, "rejected nested turn must cause zero additional upstream generation")
									for i, frame := range frames {
										source := stable
										if i == 2 {
											source = third
										}
										issue600AssertNestedProjection(t, namespace, []byte(source), frame)
										if headers.Get("thread-id") != "" {
											issue600ProjectedHeader(t, mode, outHeaders[i], issue600Thread(frames[0]))
										}
										if namespace != "none" {
											require.NotContains(t, fmt.Sprint(outHeaders[i]), "fixture-client-thread")
										}
									}
								})
							}
						})
					}
				})
			}
		})
	}
}

func TestIssue600NestedInitialPolicy(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, "legacy"} {
		t.Run(mode, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				t.Run("strict="+strconv.FormatBool(strict), func(t *testing.T) {
					for _, scenario := range []string{"root_nested_conflict", "invalid_root", "invalid_nested"} {
						t.Run(scenario, func(t *testing.T) {
							rootMeta, nestedMeta := issue600NestedMetadata(), issue600NestedMetadata()
							switch scenario {
							case "root_nested_conflict":
								rootMeta["thread_id"] = "fixture-conflicting-thread"
							case "invalid_root":
								rootMeta["thread_id"] = false
							case "invalid_nested":
								nestedMeta["thread_id"] = "fixture\ninvalid"
							}
							payload := issue600NestedPayload(t, rootMeta, nestedMeta)
							h, up := newIssue600NetworkHarness(t, mode, "oauth", strict, nil)
							issue600Send(t, h, payload, 1, strict)
							if !strict {
								issue600Send(t, h, payload, 2, false)
							}
							issue600Finish(t, h, nil)
							frames, _ := up.snapshot()
							want := 2
							if strict {
								want = 0
							}
							require.Len(t, frames, want, "initial strict/non-strict policy must not change")
						})
					}
				})
			}
		})
	}
}

func TestIssue600NestedAccountSwitch(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, "legacy"} {
		t.Run(mode, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				t.Run("strict="+strconv.FormatBool(strict), func(t *testing.T) {
					payload := issue600NestedPayload(t, issue600NestedMetadata(), issue600NestedMetadata())
					headers := http.Header{"Thread-Id": []string{"fixture-client-thread"}}
					h, up := newIssue600NetworkHarness(t, mode, "oauth", strict, headers, issue600FixtureOptions{switchAccount: true})
					issue600Send(t, h, payload, 1, false)
					issue600Send(t, h, payload, 2, false)
					issue600Finish(t, h, headers)
					frames, outHeaders := up.snapshot()
					require.Len(t, frames, 2, "replacement account must generate exactly two turns")
					rejectedHeaders, rejectedBodies := up.rejectedSnapshot()
					require.Len(t, rejectedHeaders, 1, "refusing credential must never generate or be retried")
					projected := issue600Thread(frames[0])
					for i, frame := range frames {
						issue600AssertNestedProjection(t, "oauth", []byte(payload), frame)
						require.Equal(t, projected, issue600Thread(frame))
						issue600ProjectedHeader(t, mode, outHeaders[i], projected)
						require.Equal(t, "Bearer fixture-account-b", outHeaders[i].Get("Authorization"))
					}
					if mode == service.OpenAIWSIngressModeHTTPBridge {
						issue600AssertNestedProjection(t, "oauth", []byte(payload), rejectedBodies[0])
						require.NotEqual(t, issue600Thread(rejectedBodies[0]), projected, "credential switch must reproject original root AND nested identity")
					} else {
						require.NotEqual(t, rejectedHeaders[0].Get("thread-id"), projected)
					}
				})
			}
		})
	}
}

// A connection bound by nested thread metadata still observes the real block
// key when later metadata is omitted, even with a lower-priority static header.
func TestIssue600NestedInheritedBlock(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, "legacy"} {
		t.Run(mode, func(t *testing.T) {
			first := issue600NestedPayload(t, nil, issue600NestedMetadata())
			headers := http.Header{"Session_id": []string{"fixture-static-session"}}
			h, up := newIssue600NetworkHarness(t, mode, "oauth", true, headers)
			issue600Send(t, h, first, 1, false)
			issue600Send(t, h, first, 2, false)
			// The trusted nested identity has exactly the same block key as its root form.
			c := newTestGinContext()
			c.Request = httptestIssue600Request(first, headers)
			key := service.CyberSessionExplicitBlockKey(h.apiKey.ID, c, []byte(first))
			require.NotEmpty(t, key)
			store, ok := h.gatewayCache.(service.CyberSessionBlockStore)
			require.True(t, ok)
			require.NoError(t, store.SetCyberSessionBlocked(context.Background(), "", []string{key}, time.Minute))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, h.clientConn.Write(ctx, coderws.MessageText, []byte(issue600NestedPayload(t, nil, nil))))
			_, event, err := h.clientConn.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, "session_blocked_by_cyber_policy", gjson.GetBytes(event, "error.code").String())
			_, _, err = h.clientConn.Read(ctx)
			require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
			issue600Finish(t, h, headers)
			frames, _ := up.snapshot()
			require.Len(t, frames, 2, "blocked inherited nested session must not forward")
		})
	}
}

func httptestIssue600Request(body string, headers http.Header) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://fixture.invalid/v1/responses", strings.NewReader(body))
	req.Header = headers.Clone()
	return req
}

// This exercises the bridge's accountIdentitySourceRaw path after completed
// turns. The third turn is refused before generation, then the replacement must
// derive B(original) rather than B(A(original)). No completed turn is replayed.
func TestIssue600NestedBridgeLaterAccountSwitch(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run("strict="+strconv.FormatBool(strict), func(t *testing.T) {
			payload := issue600NestedPayload(t, issue600NestedMetadata(), issue600NestedMetadata())
			headers := http.Header{"Thread-Id": []string{"fixture-client-thread"}}
			var expectedThread, expectedSession string
			t.Run("replacement_reference", func(t *testing.T) {
				h, up := newIssue600NetworkHarness(t, service.OpenAIWSIngressModeHTTPBridge, "oauth", strict, headers)
				issue600Send(t, h, payload, 1, false)
				issue600Send(t, h, payload, 2, false)
				issue600Finish(t, h, headers)
				frames, _ := up.snapshot()
				require.Len(t, frames, 2)
				expectedThread = issue600Thread(frames[0])
				expectedSession = gjson.GetBytes(frames[0], "response.client_metadata.session_id").String()
			})
			require.NotEmpty(t, expectedThread)
			require.NotEmpty(t, expectedSession)
			t.Run("later_turn_replacement", func(t *testing.T) {
				h, up := newIssue600NetworkHarness(t, service.OpenAIWSIngressModeHTTPBridge, "oauth", strict, headers, issue600FixtureOptions{switchAfterTurns: 2})
				for turn := 1; turn <= 4; turn++ {
					issue600Send(t, h, payload, turn, false)
				}
				issue600Finish(t, h, headers)
				frames, outHeaders := up.snapshot()
				require.Len(t, frames, 4, "two original and two replacement turns must generate exactly once each")
				refusals, refusedBodies := up.rejectedSnapshot()
				require.Len(t, refusals, 1, "only the current third turn is refused, with no generation")
				require.Len(t, refusedBodies, 1)
				originalAccountThread := issue600Thread(frames[0])
				require.Equal(t, originalAccountThread, issue600Thread(frames[1]))
				require.Equal(t, originalAccountThread, issue600Thread(refusedBodies[0]))
				require.NotEqual(t, originalAccountThread, expectedThread)
				for i, frame := range frames {
					issue600AssertNestedProjection(t, "oauth", []byte(payload), frame)
					issue600ProjectedHeader(t, service.OpenAIWSIngressModeHTTPBridge, outHeaders[i], issue600Thread(frame))
					if i < 2 {
						require.Equal(t, "Bearer fixture-account-a", outHeaders[i].Get("Authorization"))
					} else {
						require.Equal(t, "Bearer fixture-account-b", outHeaders[i].Get("Authorization"))
						require.Equal(t, expectedThread, issue600Thread(frame), "replacement thread must be B(original), never B(A(original))")
						require.Equal(t, expectedSession, gjson.GetBytes(frame, "response.client_metadata.session_id").String(), "replacement nested session must derive from original accountIdentitySourceRaw")
					}
				}
			})
		})
	}
}
