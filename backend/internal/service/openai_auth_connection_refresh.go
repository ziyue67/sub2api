package service

import (
	"context"
	"encoding/json"
	"errors"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// No raw transport/body/token is exposed. Only definitely pre-send errors may
// reset durable intent; explicit rejection and uncertain outcomes require login.
type OpenAIConnectionTokenError struct{ Outcome string }

func (e *OpenAIConnectionTokenError) Error() string {
	return "connection token operation " + e.Outcome + "; review authorization before retrying"
}
func connectionRefreshUnavailable() error {
	return infraerrors.Conflict("OPENAI_CONNECTION_REFRESH_UNCERTAIN", "Connection refresh needs recovery or reauthorization; the old refresh token will not be replayed")
}

type connectionRefreshCandidate struct {
	ConnectionID string   `json:"connection_id"`
	ClientID     string   `json:"client_id"`
	Subject      string   `json:"subject"`
	Attempt      string   `json:"attempt"`
	Revision     int64    `json:"revision"`
	Ciphertext   string   `json:"credentials_ciphertext"`
	Scopes       []string `json:"scopes"`
	Capabilities []string `json:"capabilities"`
}

func (s *OpenAIOAuthReauthService) encryptRefreshCandidate(c *OpenAIAuthConnection) (string, error) {
	raw, err := json.Marshal(connectionRefreshCandidate{c.ID, c.ClientID, c.Subject, c.RefreshAttemptID, c.Revision, c.CredentialsCiphertext, c.Scopes, c.Capabilities})
	if err != nil {
		return "", err
	}
	return s.encryptor.Encrypt(string(raw))
}
func (s *OpenAIOAuthReauthService) finishConnectionRefresh(ctx context.Context, r OpenAIAuthConnectionRepository, c *OpenAIAuthConnection) (string, error) {
	plain, err := s.encryptor.Decrypt(c.RefreshCandidateCiphertext)
	if err != nil {
		return "", connectionRefreshUnavailable()
	}
	var candidate connectionRefreshCandidate
	if json.Unmarshal([]byte(plain), &candidate) != nil || candidate.ConnectionID != c.ID || candidate.ClientID != c.ClientID || candidate.Subject != c.Subject || candidate.Attempt != c.RefreshAttemptID || candidate.Revision != c.Revision {
		return "", connectionRefreshUnavailable()
	}
	next := *c
	next.CredentialsCiphertext = candidate.Ciphertext
	creds, err := s.readConnectionCredentials(&next)
	if err != nil {
		return "", connectionRefreshUnavailable()
	}
	scopes, _ := json.Marshal(candidate.Scopes)
	caps, _ := json.Marshal(candidate.Capabilities)
	changed, err := r.FinishAuthConnectionRefresh(ctx, c, candidate.Ciphertext, string(scopes), string(caps))
	if err != nil || !changed {
		return "", connectionRefreshUnavailable()
	}
	if len(candidate.Capabilities) == 0 {
		return "", errors.New("connection no longer permits inference")
	}
	return stringCredential(creds, "access_token"), nil
}
