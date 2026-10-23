package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// WSNodeOwnerRef contains internal IDs only. ResponseID is hashed by the cache;
// neither this record nor the node/boot token grants downstream authorization.
type WSNodeOwnerRef struct {
	GroupID, APIKeyID, AccountID int64
	ResponseID                   string
}

type OpenAIWSNodeOwnerCache interface {
	ClaimWSNodeOwner(context.Context, WSNodeOwnerRef, string, time.Duration) (bool, error)
	MatchesWSNodeOwner(context.Context, WSNodeOwnerRef, string) (bool, error)
}

var ErrOpenAIWSNodeOwner = errors.New("websocket continuation owner unavailable")

func wsNodeOwnerRejection() error {
	return NewOpenAIWSClientCloseError(websocket.StatusPolicyViolation, "continuation requires its original live node and connection; automatic replay is disabled", ErrOpenAIWSNodeOwner)
}

func (s *OpenAIGatewayService) wsNodeOwnerEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Runtime.WSNodeOwner.Enabled
}

// ProtectsWSNodeContinuation also prevents the handler from stripping a first
// frame's previous_response_id before the native ingress can check ownership.
func (s *OpenAIGatewayService) ProtectsWSNodeContinuation(account *Account, raw []byte) bool {
	return s.wsNodeOwnerEnabled() && account != nil && s.isOpenAIWSStoreDisabledInRequestRaw(raw, account)
}

func (s *OpenAIGatewayService) wsNodeOwnerToken() string {
	s.openaiWSNodeOwnerOnce.Do(func() {
		s.openaiWSNodeOwnerToken = "v1:" + s.cfg.Runtime.WSNodeOwner.NodeID + ":" + uuid.NewString()
	})
	return s.openaiWSNodeOwnerToken
}

func (s *OpenAIGatewayService) wsConnectionOwnerToken(connID string) string {
	digest := sha256.Sum256([]byte(connID))
	return s.wsNodeOwnerToken() + ":" + hex.EncodeToString(digest[:])
}

func (s *OpenAIGatewayService) checkWSNodeOwner(ctx context.Context, ref WSNodeOwnerRef) error {
	if !s.wsNodeOwnerEnabled() {
		return nil
	}
	cache, ok := s.cache.(OpenAIWSNodeOwnerCache)
	if !ok || ref.GroupID < 0 || ref.APIKeyID <= 0 || ref.AccountID <= 0 || s.cfg.Runtime.WSNodeOwner.Validate() != nil {
		return wsNodeOwnerRejection()
	}
	if strings.TrimSpace(ref.ResponseID) == "" {
		return nil
	}
	connID, live := s.getOpenAIWSStateStore().GetResponseConn(ref.ResponseID)
	if !live {
		return wsNodeOwnerRejection()
	}
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(ctx)
	defer cancel()
	matches, err := cache.MatchesWSNodeOwner(cacheCtx, ref, s.wsConnectionOwnerToken(connID))
	if err != nil || !matches {
		return wsNodeOwnerRejection()
	}
	return nil
}

func (s *OpenAIGatewayService) bindWSNodeOwner(ctx context.Context, ref WSNodeOwnerRef) error {
	if !s.wsNodeOwnerEnabled() || strings.TrimSpace(ref.ResponseID) == "" {
		return nil
	}
	cache, ok := s.cache.(OpenAIWSNodeOwnerCache)
	if !ok || ref.GroupID < 0 || ref.APIKeyID <= 0 || ref.AccountID <= 0 || s.cfg.Runtime.WSNodeOwner.Validate() != nil {
		return wsNodeOwnerRejection()
	}
	connID, live := s.getOpenAIWSStateStore().GetResponseConn(ref.ResponseID)
	if !live {
		return wsNodeOwnerRejection()
	}
	cacheCtx, cancel := withOpenAIWSStateStoreRedisTimeout(ctx)
	defer cancel()
	owned, err := cache.ClaimWSNodeOwner(cacheCtx, ref, s.wsConnectionOwnerToken(connID), s.openAIWSResponseStickyTTL())
	if err != nil || !owned {
		return wsNodeOwnerRejection()
	}
	return nil
}
