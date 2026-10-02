package prismbridge

import (
	"fmt"
	"strings"
	"time"
)

// Credential is the account-side Prism OAuth material. Prism's web OAuth
// callback produces prism_* cookies, which are different from ChatGPT's
// auth.openai.com OAuth access token.
type Credential struct {
	AccessToken  string
	RefreshToken string
	SessionToken string
	DID          string
	SC           string
	ExpiresAt    time.Time
}

func CredentialFromMap(values map[string]any) Credential {
	return Credential{
		AccessToken:  first(values, "prism_oai_access_token", "access_token", "oai_access_token"),
		RefreshToken: first(values, "prism_oai_refresh_token", "refresh_token"),
		SessionToken: first(values, "prism_session_token", "session_token"),
		DID:          first(values, "oai-did", "oai_did"),
		SC:           first(values, "oai-sc", "oai_sc"),
		ExpiresAt:    parseTime(first(values, "expires_at", "prism_oai_expires_at")),
	}
}

func (c Credential) Valid() bool {
	return strings.TrimSpace(c.AccessToken) != ""
}

func (c Credential) CookieHeader() (string, error) {
	if !c.Valid() {
		return "", fmt.Errorf("prism OAuth access token is missing")
	}
	parts := []string{"prism_oai_access_token=" + c.AccessToken}
	if c.RefreshToken != "" {
		parts = append(parts, "prism_oai_refresh_token="+c.RefreshToken)
	}
	if c.SessionToken != "" {
		parts = append(parts, "prism_session_token="+c.SessionToken)
	}
	if c.DID != "" {
		parts = append(parts, "oai-did="+c.DID)
	}
	if c.SC != "" {
		parts = append(parts, "oai-sc="+c.SC)
	}
	return strings.Join(parts, "; "), nil
}

func first(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}
