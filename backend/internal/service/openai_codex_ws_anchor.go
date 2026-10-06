package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type codexWSAnchorKey struct {
	account, apiKey, group int64
	scope                  string
	identity               [32]byte
}
type codexWSAnchorEntry struct {
	responseID, connID string
	proxyURL           string
	expires            time.Time
}
type codexWSAnchorStore struct {
	mu      sync.Mutex
	entries map[codexWSAnchorKey]codexWSAnchorEntry
	busy    map[codexWSAnchorKey]bool
}
type codexWSAnchorTurn struct {
	previousID, connID, cookie string
	proxyURL                   string
	scope                      string
	expires                    time.Time
	qualified                  bool
}
type codexWSAnchorContextKey struct{}

func codexWSAnchorFromContext(ctx context.Context) *codexWSAnchorTurn {
	v, _ := ctx.Value(codexWSAnchorContextKey{}).(*codexWSAnchorTurn)
	return v
}

func codexAstraCompleted(response gjson.Result) bool {
	if response.Get("model").String() != "gpt-6-astra" || response.Get("status").String() != "completed" || response.Get("error").IsObject() {
		return false
	}
	for _, item := range response.Get("output").Array() {
		for _, c := range item.Get("content").Array() {
			if c.Get("type").String() == "output_text" && strings.TrimSpace(c.Get("text").String()) != "" {
				return true
			}
		}
	}
	return false
}

// Anchors are scoped to an authenticated API key, group, explicit client
// thread/session, upstream account and credential revision. Callers explicitly
// supply previous_response_id and delta input; we never attach unrelated history.
func (s *OpenAIGatewayService) prepareCodexWSAnchor(ctx context.Context, c *gin.Context, account *Account, body []byte) (context.Context, func(*OpenAIForwardResult, error), error) {
	if s == nil || s.cfg == nil {
		return ctx, nil, nil
	}
	settings := s.cfg.AstraRouting(ctx)
	if !settings.WSSession.Enabled || account == nil || !slices.Contains(settings.WSSession.AccountIDs, account.ID) || !account.IsOpenAIOAuthLike() || account.IsOpenAIPassthroughEnabled() || c == nil || c.Request == nil || isOpenAIResponsesCompactPath(c) || gjson.GetBytes(body, "model").String() != "gpt-6-astra" || account.GetMappedModel("gpt-6-astra") != "gpt-6-astra" {
		return ctx, nil, nil
	}
	if err := settings.WSSession.Validate(); err != nil {
		return ctx, nil, err
	}
	apiKeyID := getAPIKeyIDFromContext(c)
	scope, _ := resolveOpenAIWSExecutionScope(c, body, apiKeyID)
	if apiKeyID <= 0 || scope == "" {
		return ctx, nil, errors.New("codex WS anchor requires an authenticated API key and explicit session/thread identity")
	}
	previous := gjson.GetBytes(body, "previous_response_id").String()
	if s.getOpenAIWSProtocolResolver().Resolve(account).Transport != OpenAIUpstreamTransportResponsesWebsocketV2 {
		return ctx, nil, errors.New("codex WS anchor requires account and global WSv2 to be enabled")
	}
	key := codexWSAnchorKey{account: account.ID, apiKey: apiKeyID, group: getOpenAIGroupIDFromContext(c), scope: scope, identity: sha256.Sum256([]byte(account.GetCredential("chatgpt_account_id") + "\x00" + account.GetCredential("access_token")))}
	scope += ":" + settings.Revision
	key.scope = scope
	now := time.Now()
	store := &s.codexWSAnchors
	store.mu.Lock()
	if store.entries == nil {
		store.entries = make(map[codexWSAnchorKey]codexWSAnchorEntry)
		store.busy = make(map[codexWSAnchorKey]bool)
	}
	for k, e := range store.entries {
		if !now.Before(e.expires) && !store.busy[k] {
			delete(store.entries, k)
		}
	}
	if store.busy[key] {
		store.mu.Unlock()
		return ctx, nil, errors.New("codex WS anchor session already has an active request")
	}
	entry, found := store.entries[key]
	if previous != "" && (!found || previous != entry.responseID || !now.Before(entry.expires)) {
		store.mu.Unlock()
		return ctx, nil, errors.New("codex WS anchor is missing, expired or belongs to another session")
	}
	if !found && len(store.entries)+len(store.busy) >= 1024 {
		store.mu.Unlock()
		return ctx, nil, errors.New("codex WS anchor capacity reached")
	}
	turn := &codexWSAnchorTurn{previousID: previous, scope: fmt.Sprintf("%d:%d:%s:%x", key.apiKey, key.group, key.scope, key.identity), expires: entry.expires}
	if previous != "" {
		turn.connID = entry.connID
		turn.proxyURL = entry.proxyURL

	}
	if previous == "" {
		ttl := settings.WSSession.TTLSeconds
		if ttl == 0 {
			ttl = 3600
		}
		turn.expires = now.Add(time.Duration(ttl) * time.Second)
	}
	store.busy[key] = true
	store.mu.Unlock()
	finish := func(result *OpenAIForwardResult, resultErr error) {
		store.mu.Lock()
		defer store.mu.Unlock()
		delete(store.busy, key)
		qualified := turn.qualified

		if resultErr != nil || result == nil || result.ClientDisconnect || (previous != "" && !time.Now().Before(entry.expires)) || !qualified || result.ResponseID == "" || result.UpstreamResponseModel != "gpt-6-astra" || result.UpstreamResponseModelConflict {
			delete(store.entries, key)
			if turn.connID != "" {
				s.getOpenAIWSConnPool().evictConn(account.ID, turn.connID)
			}
			return
		}
		if previous == "" {
			if entry.connID != "" {
				s.getOpenAIWSConnPool().evictConn(account.ID, entry.connID)
			}
			entry = codexWSAnchorEntry{expires: turn.expires}
		}
		// The session deadline never slides forward on continuation.
		entry.responseID = result.ResponseID
		entry.connID = turn.connID
		entry.proxyURL = turn.proxyURL
		store.entries[key] = entry
	}
	return context.WithValue(ctx, codexWSAnchorContextKey{}, turn), finish, nil
}

func replaceCodexWSAnchorCookie(h http.Header, value string) {
	var keep []string
	for _, raw := range h.Values("Cookie") {
		for _, part := range strings.Split(raw, ";") {
			part = strings.TrimSpace(part)
			name, _, _ := strings.Cut(part, "=")
			if part != "" && strings.TrimSpace(name) != "__oailb" {
				keep = append(keep, part)
			}
		}
	}
	keep = append(keep, (&http.Cookie{Name: "__oailb", Value: value}).String())
	h.Set("Cookie", strings.Join(keep, "; "))
}
