// Package model provides a deliberately narrow metered text adapter. It is not
// registered by the application router until global budget admission is ready.
package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Request struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int64     `json:"max_tokens"`
	Stream    bool      `json:"stream"`
}

// Admission MUST authenticate the request and reserve all shared budgets. A
// public header must never directly supply lease/user/account identity.
type Admission func(context.Context, Request) (ledger.Reservation, ledger.Price, error)

// Forward receives a validated, bounded request. Implementations must use the
// admitted account, disable retries and honor MaxTokens. It must not read client
// Authorization as an upstream credential.
type Forward func(context.Context, ledger.Reservation, Request) (*http.Response, error)
type Handler struct {
	Ledger  ledger.RequestLedger
	Admit   Admission
	Forward Forward
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Ledger == nil || h.Admit == nil || h.Forward == nil {
		http.Error(w, "regional admission unavailable", http.StatusServiceUnavailable)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		http.Error(w, "request too large", http.StatusBadRequest)
		return
	}
	var req Request
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&req); err != nil || dec.Decode(new(any)) != io.EOF || req.Model == "" || req.MaxTokens <= 0 || len(req.Messages) == 0 {
		http.Error(w, "only bounded text chat is supported", http.StatusBadRequest)
		return
	}
	for _, m := range req.Messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			http.Error(w, "unsupported message role", http.StatusBadRequest)
			return
		}
	}
	reservation, price, err := h.Admit(r.Context(), req)
	if err != nil || price.Model != req.Model {
		http.Error(w, "regional budget unavailable", http.StatusServiceUnavailable)
		return
	}
	wrote := false
	err = ledger.ExecuteBounded(r.Context(), h.Ledger, price, reservation, raw, req.MaxTokens, func(ctx context.Context) (ledger.MeasuredUsage, error) {
		response, err := h.Forward(ctx, reservation, req)
		if err != nil {
			return ledger.MeasuredUsage{}, err
		}
		if response == nil || response.Body == nil {
			return ledger.MeasuredUsage{}, errors.New("missing upstream response")
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != 200 {
			return ledger.MeasuredUsage{}, errors.New("upstream rejected; reservation retained")
		}
		if req.Stream {
			if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				return ledger.MeasuredUsage{}, errors.New("unexpected stream content type")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			wrote = true
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			var usage ledger.MeasuredUsage
			done := false
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")
					if data == "[DONE]" {
						done = true
					} else {
						measured, present, parseErr := parseUsage([]byte(data))
						if parseErr != nil {
							return ledger.MeasuredUsage{}, parseErr
						}
						if present {
							if usage.Final {
								return ledger.MeasuredUsage{}, errors.New("duplicate final usage")
							}
							usage = measured
						}
					}
				}
				if _, err = io.WriteString(w, line+"\n"); err != nil {
					return ledger.MeasuredUsage{}, err
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			if err = scanner.Err(); err != nil {
				return ledger.MeasuredUsage{}, err
			}
			if !done {
				return ledger.MeasuredUsage{}, errors.New("stream ended without terminal marker")
			}
			return usage, nil
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
		if err != nil || len(body) > 8<<20 {
			return ledger.MeasuredUsage{}, errors.New("invalid bounded response")
		}
		usage, _, err := parseUsage(body)
		if err != nil {
			return usage, err
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		wrote = true
		if _, err = w.Write(body); err != nil {
			return usage, err
		}
		return usage, nil
	})
	if err != nil && !wrote {
		http.Error(w, "metered request unavailable", http.StatusServiceUnavailable)
	}
}

func parseUsage(raw []byte) (ledger.MeasuredUsage, bool, error) {
	var envelope struct {
		Usage *struct {
			Input   *int64 `json:"prompt_tokens"`
			Output  *int64 `json:"completion_tokens"`
			Details struct {
				Cached int64 `json:"cached_tokens"`
				Audio  int64 `json:"audio_tokens"`
			} `json:"prompt_tokens_details"`
			OutputDetails struct {
				Audio int64 `json:"audio_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ledger.MeasuredUsage{}, false, err
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return ledger.MeasuredUsage{}, false, errors.New("upstream error event")
	}
	u := envelope.Usage
	if u == nil {
		return ledger.MeasuredUsage{}, false, nil
	}
	if u.Input == nil || u.Output == nil || u.Details.Cached < 0 || *u.Input < u.Details.Cached || u.Details.Audio != 0 || u.OutputDetails.Audio != 0 {
		return ledger.MeasuredUsage{}, false, errors.New("unsupported or incomplete usage")
	}
	return ledger.MeasuredUsage{Input: *u.Input - u.Details.Cached, Output: *u.Output, CacheRead: u.Details.Cached, Final: true}, true, nil
}
