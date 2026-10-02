package prismbridge

import "testing"

func TestCredentialFromMapBuildsPrismCookieHeader(t *testing.T) {
	c := CredentialFromMap(map[string]any{
		"prism_oai_access_token":  "access",
		"prism_oai_refresh_token": "refresh",
		"prism_session_token":     "session",
		"oai-did":                 "did",
	})
	cookie, err := c.CookieHeader()
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "prism_oai_access_token=access; prism_oai_refresh_token=refresh; prism_session_token=session; oai-did=did" {
		t.Fatalf("cookie=%q", cookie)
	}
}

func TestCredentialRequiresPrismAccessToken(t *testing.T) {
	if _, err := (Credential{}).CookieHeader(); err == nil {
		t.Fatal("missing access token must fail closed")
	}
}
