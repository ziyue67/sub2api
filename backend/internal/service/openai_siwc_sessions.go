package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// SIWC sessions contain PKCE and a credential snapshot. Shared sessions must be
// encrypted, and consumption must never fall back to an independent local copy.
const siwcSessionPrefix = "oauth:session:openai:siwc:"

func siwcSessionKey(id string) (string, error) {
	if len(id) != 32 {
		return "", errors.New("invalid SIWC session ID")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", errors.New("invalid SIWC session ID")
	}
	return siwcSessionPrefix + id, nil
}

func (s *OpenAIOAuthReauthService) setSIWCSession(ctx context.Context, id string, session *openai.OAuthSession) error {
	if s.siwcSessionStore == nil {
		s.oauth.sessionStore.Set(id, session)
		return nil
	}
	_, err := siwcSessionKey(id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		ID      string
		Session *openai.OAuthSession
	}{id, session})
	if err != nil {
		return errors.New("failed to encode SIWC session")
	}
	ciphertext, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return errors.New("failed to encrypt SIWC session")
	}
	sharedCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = s.siwcSessionStore.Set(sharedCtx, id, ciphertext); err != nil {
		return errors.New("SIWC session storage unavailable; retry authorization later")
	}
	return nil
}

func (s *OpenAIOAuthReauthService) readSIWCSession(ctx context.Context, id string, take bool) (*openai.OAuthSession, bool) {
	if s.siwcSessionStore == nil {
		if take {
			return s.oauth.sessionStore.Take(id)
		}
		return s.oauth.sessionStore.Get(id)
	}
	_, err := siwcSessionKey(id)
	if err != nil {
		return nil, false
	}
	sharedCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var ciphertext string
	var ok bool
	if take {
		ok, err = s.siwcSessionStore.Take(sharedCtx, id, &ciphertext)
	} else {
		ok, err = s.siwcSessionStore.Get(sharedCtx, id, &ciphertext)
	}
	if err != nil || !ok {
		return nil, false
	}
	plain, err := s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, false
	}
	var envelope struct {
		ID      string
		Session *openai.OAuthSession
	}
	if json.Unmarshal([]byte(plain), &envelope) != nil || envelope.ID != id || envelope.Session == nil {
		return nil, false
	}
	session := envelope.Session
	if session.SIWCAccountID <= 0 || session.CreatedAt.IsZero() || time.Since(session.CreatedAt) > openai.SessionTTL {
		return nil, false
	}
	return session, true
}

func (s *OpenAIOAuthReauthService) deleteSIWCSession(ctx context.Context, id string) {
	if s.siwcSessionStore == nil {
		s.oauth.sessionStore.Delete(id)
		return
	}
	_, err := siwcSessionKey(id)
	if err != nil {
		return
	}
	sharedCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// A failed delete cannot authorize a request: a valid code path still has to
	// atomically take the shared entry before exchanging an authorization code.
	_ = s.siwcSessionStore.Delete(sharedCtx, id)
}
