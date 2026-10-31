package service

import (
	"context"
	"errors"
	"net/http"
)

// Call immediately before EVERY managed network attempt, after proxy/slot waits.
// Cookie/ticket restoration and plugin transport belong to the legacy account
// lifecycle and cannot run for an independently authorized registration.
func (s *OpenAIGatewayService) fenceAuthConnectionEgress(ctx context.Context, req *http.Request, a *Account) error {
	if a == nil || a.AuthConnectionID == "" {
		return nil
	}
	if s.excelOAuthReauth == nil {
		return errors.New("authorization connection fence unavailable")
	}
	if err := s.excelOAuthReauth.validateAuthConnectionPolicy(ctx, a); err != nil {
		return err
	}
	if err := s.excelOAuthReauth.ValidateAuthConnectionSend(ctx, a); err != nil {
		return err
	}
	if req != nil {
		req.Header.Del("Cookie")
		req.Header.Del(openAICodexTurnStateHeader)
	}
	return nil
}
