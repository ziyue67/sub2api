package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
)

func requestCodex780WS(req *http.Request, proxy, edge string, payload []byte, seed []string, target, model string) (out codexHarvestProbeResult) {
	out = codexHarvestProbeResult{Sent: true, EdgeIP: edge, Transport: "websocket", Gateway: target}
	client, req, err := codexMintHTTPClient(req, proxy, edge)
	if err != nil {
		out.Err = errors.New("mint websocket proxy unavailable")
		return
	}
	defer client.CloseIdleConnections()
	headers := req.Header.Clone()

	headers.Del("Accept")
	headers.Del("Content-Type")
	headers.Set("OpenAI-Beta", openAIWSBetaV2Value)
	endpoint := *req.URL
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	conn, resp, err := coderws.Dial(req.Context(), endpoint.String(), &coderws.DialOptions{HTTPClient: client, Host: req.Host, HTTPHeader: headers, CompressionMode: coderws.CompressionDisabled})
	if resp != nil {
		out.Status = resp.StatusCode
		out.RetryAfter = codexHarvestRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	}
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		out.Err = mintTransportError(err)
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(openAICodexStateProbeMaxBody)
	out.State = extractOpenAICodexTurnState(resp.Header)
	var routeErr error
	out.Cookies, routeErr = codex780ResponseRoute(resp, seed, target, time.Now())
	defer func() {
		if routeErr == nil {
			return
		}
		var eventErr *codexMintError
		if errors.As(out.Err, &eventErr) && (eventErr.terminal || eventErr.kind == "rate_limited") {
			return
		}
		out.Err = routeErr
	}()
	out.Gateway = codex780CookieGateway(out.Cookies)
	if out.Gateway == "" {
		out.Gateway = "any"
	}
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil {
		out.Err = errors.New("mint payload invalid")
		return
	}
	delete(body, "stream")
	body["type"] = "response.create"
	if state := req.Header.Get(openAICodexTurnStateHeader); state != "" {
		body["client_metadata"] = map[string]string{openAICodexTurnStateHeader: state}
	}
	raw, _ := json.Marshal(body)
	if err := conn.Write(req.Context(), coderws.MessageText, raw); err != nil {
		out.Err = mintTransportError(err)
		return
	}
	var transcript strings.Builder
	for total := 0; total < openAICodexStateProbeMaxBody; {
		kind, message, err := conn.Read(req.Context())
		total += len(message)
		if err != nil {
			out.Err = mintTransportError(err)
			return
		}
		if kind != coderws.MessageText || total > openAICodexStateProbeMaxBody {
			out.Err = errors.New("mint websocket response incomplete")
			return
		}
		if err := codex780EventError(message, ""); err != nil {
			out.Err = err
			return
		}
		_, _ = transcript.WriteString("data: ")
		_, _ = transcript.Write(message)
		_, _ = transcript.WriteString("\n\n")
		var event struct {
			Type     string            `json:"type"`
			Headers  map[string]string `json:"headers"`
			Response struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"response"`
		}
		if json.Unmarshal(message, &event) != nil {
			out.Err = errors.New("mint websocket invalid event")
			return
		}
		switch event.Type {
		case "codex.response.metadata":
			if ticket := event.Headers["x-codex-turn-state"]; ticket != "" {
				out.State = ticket
			}
		case "response.completed":
			out.Err = readCodexHarvestComplete(strings.NewReader(transcript.String()))
			return
		case "error", "response.failed":
			out.Err = errors.New("mint websocket error event")
			return
		}
	}
	out.Err = errors.New("mint websocket scan limit")
	return
}
