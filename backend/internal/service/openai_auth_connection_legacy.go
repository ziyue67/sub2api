package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
)

// Legacy registrations are stable views over their original storage, never
// copies of rotating tokens. Old clients keep exactly one lifecycle owner.
func legacyConnectionID(accountID int64, slot string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("sub2api:openai:"+strconv.FormatInt(accountID, 10)+":"+slot)).String()
}

func (s *OpenAIOAuthReauthService) legacyAuthConnections(ctx context.Context, a *Account) ([]*OpenAIAuthConnection, error) {
	result := []*OpenAIAuthConnection{}
	selected := "primary"
	if a.IsSIWCEnabled() {
		selected = "siwc"
	} else if a.IsExcelBPSEnabled() && !a.IsExcelOAuth() {
		selected = "bps"
	}
	if a.GetCredential("access_token") != "" || a.GetCredential("api_key") != "" {
		kind := "codex"
		caps := []string{"responses", "chat_completions", "responses_compact"}
		if a.Type == AccountTypeAPIKey {
			kind = "api_key"
			caps = []string{"responses", "chat_completions", "embeddings"}
		} else if a.IsExcelOAuth() {
			kind = "bps"
			caps = []string{"responses", "chat_completions"}
		}
		result = append(result, &OpenAIAuthConnection{ID: legacyConnectionID(a.ID, "primary"), AccountID: a.ID, Kind: kind, Label: "Legacy primary", Legacy: true, Status: "active", ClientID: a.GetCredential("client_id"), Workspace: a.GetCredential("chatgpt_account_id"), Scopes: []string{}, Capabilities: caps, Preferred: !a.HasManagedOpenAIConnections() && selected == "primary"})
	}
	for _, kind := range []string{"bps", "siwc"} {
		var ciphertext string
		var err error
		if kind == "bps" {
			r, ok := s.repo.(OpenAIExcelOAuthRepository)
			if !ok {
				continue
			}
			ciphertext, err = r.GetExcelCredentials(ctx, a.ID)
		} else {
			r, ok := s.repo.(OpenAISIWCOAuthRepository)
			if !ok {
				continue
			}
			ciphertext, err = r.GetSIWCCredentials(ctx, a.ID)
		}
		if err != nil {
			return nil, err
		}
		if ciphertext == "" {
			continue
		}
		plain, err := s.encryptor.Decrypt(ciphertext)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("OPENAI_CONNECTION_LEGACY_UNAVAILABLE", "Legacy credential metadata cannot be read")
		}
		var creds map[string]any
		if json.Unmarshal([]byte(plain), &creds) != nil {
			return nil, infraerrors.ServiceUnavailable("OPENAI_CONNECTION_LEGACY_UNAVAILABLE", "Legacy credential metadata is invalid")
		}
		scope := stringCredential(creds, "scope")
		c := &OpenAIAuthConnection{ID: legacyConnectionID(a.ID, kind), AccountID: a.ID, Kind: kind, Label: "Legacy " + kind, Legacy: true, Status: "active", ClientID: stringCredential(creds, "client_id"), Scopes: []string{}, Capabilities: connectionCapabilities(kind, scope), Preferred: !a.HasManagedOpenAIConnections() && selected == kind}
		if kind == "siwc" {
			c.Subject = stringCredential(creds, "subject")
			c.IdentityVerified = c.Subject != ""
			c.HostID = stringCredential(creds, "host_id")
			c.Issuer = "https://auth.openai.com"
			c.Resource = openai.SIWCResource
		}
		result = append(result, c)
	}
	return result, nil
}

// Return to the existing route only by an explicit administrative operation.
func (s *OpenAIOAuthReauthService) SelectLegacyAuthConnection(ctx context.Context, accountID int64) error {
	if _, err := s.managementConnectionAccount(ctx, accountID); err != nil {
		return err
	}
	r, err := s.connectionRepository()
	if err != nil {
		return err
	}
	return r.UseLegacyAuthConnections(ctx, accountID)
}

func (s *OpenAIOAuthReauthService) RotateConnectionAPIKey(ctx context.Context, accountID int64, id string, revision int64, key string) (*OpenAIAuthConnection, error) {
	if _, err := s.managementConnectionAccount(ctx, accountID); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	lease, err := s.lockAuthConnection(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	defer lease.release()
	c, err := s.authConnection(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	if err = lease.bind(c); err != nil {
		return nil, err
	}
	if c.Legacy || c.Kind != "api_key" || len(key) == 0 || len(key) > 16384 {
		return nil, infraerrors.BadRequest("OPENAI_CONNECTION_API_KEY_INVALID", "Select a managed API key connection and provide a key")
	}
	if c.Revision != revision {
		return nil, connectionChanged()
	}
	if err = s.ensureDurableEncryption(); err != nil {
		return nil, err
	}
	if err = s.encryptConnectionCredentials(c, map[string]any{"api_key": key}); err != nil {
		return nil, err
	}
	c.Status = "active"
	c.CooldownUntil = nil
	c.Capabilities = []string{"responses", "chat_completions", "embeddings"}
	r, err := s.connectionRepository()
	if err != nil {
		return nil, err
	}
	changed, err := r.CASAuthConnection(ctx, c, revision)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, connectionChanged()
	}
	return s.authConnection(ctx, accountID, id)
}
