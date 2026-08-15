package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/sjson"
)

type DeepSeekUserIdentityProtocol string

const (
	DeepSeekUserIdentityChatCompletions DeepSeekUserIdentityProtocol = "chat_completions"
	DeepSeekUserIdentityResponses       DeepSeekUserIdentityProtocol = "responses"
	DeepSeekUserIdentityMessages        DeepSeekUserIdentityProtocol = "messages"

	deepSeekUserIDPrefix             = "dsu_v1_"
	deepSeekUserIDEncodedDigestBytes = 43
	deepSeekUserIDDomain             = "sub2api:deepseek-user:v1:"
	deepSeekUserIDFallbackKeyDomain  = "sub2api:deepseek-user:key:v1"
	deepSeekUserIdentityMissingError = "authenticated user context is required for DeepSeek user isolation"
)

var (
	ErrDeepSeekUserIdentityDuplicateJSONKey    = errors.New("DeepSeek request contains a duplicate user isolation JSON key")
	ErrDeepSeekUserIdentityNonCanonicalJSONKey = errors.New("DeepSeek request contains a non-canonical user isolation JSON key")
)

func normalizeDeepSeekUserIsolationMode(mode any) (string, error) {
	value, ok := mode.(string)
	if !ok {
		return "", infraerrors.BadRequest("DEEPSEEK_USER_ISOLATION_MODE_INVALID", "DeepSeek user isolation mode must be a string")
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DeepSeekUserIsolationModeAuthenticatedUser:
		return DeepSeekUserIsolationModeAuthenticatedUser, nil
	case DeepSeekUserIsolationModeOff:
		return DeepSeekUserIsolationModeOff, nil
	default:
		return "", infraerrors.BadRequest("DEEPSEEK_USER_ISOLATION_MODE_INVALID", "DeepSeek user isolation mode must be authenticated_user or off")
	}
}

func normalizeDeepSeekAccountExtra(platform string, extra map[string]any, defaultMode string) (map[string]any, error) {
	if platform != PlatformDeepSeek {
		if _, exists := extra[DeepSeekUserIsolationModeKey]; exists {
			return nil, infraerrors.BadRequest("DEEPSEEK_USER_ISOLATION_PLATFORM_INVALID", "DeepSeek user isolation mode is only valid for DeepSeek accounts")
		}
		return extra, nil
	}
	mode := defaultMode
	if rawMode, exists := extra[DeepSeekUserIsolationModeKey]; exists {
		var err error
		mode, err = normalizeDeepSeekUserIsolationMode(rawMode)
		if err != nil {
			return nil, err
		}
	}
	normalized := make(map[string]any, len(extra)+1)
	for key, value := range extra {
		normalized[key] = value
	}
	normalized[DeepSeekUserIsolationModeKey] = mode
	return normalized, nil
}

func deepSeekUserIdentitySecret(cfg *config.Config) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("DeepSeek user isolation configuration is unavailable")
	}
	if configured := strings.TrimSpace(cfg.DeepSeek.UserIDSecret); configured != "" {
		return []byte(configured), nil
	}
	jwtSecret := strings.TrimSpace(cfg.JWT.Secret)
	if jwtSecret == "" {
		return nil, errors.New("DeepSeek user isolation requires deepseek.user_id_secret or jwt.secret")
	}
	mac := hmac.New(sha256.New, []byte(jwtSecret))
	_, _ = mac.Write([]byte(deepSeekUserIDFallbackKeyDomain))
	return mac.Sum(nil), nil
}

// ValidateDeepSeekAuthenticatedUserContext ensures a trusted business user is
// available before any DeepSeek account selection or upstream work begins.
func ValidateDeepSeekAuthenticatedUserContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New(deepSeekUserIdentityMissingError)
	}
	userID, ok := ctx.Value(ctxkey.UserID).(int64)
	if !ok || userID <= 0 {
		return errors.New(deepSeekUserIdentityMissingError)
	}
	return nil
}

// DeriveDeepSeekAuthenticatedUserID derives a stable, non-identifying upstream
// user value exclusively from the authenticated Sub2API user in context.
func DeriveDeepSeekAuthenticatedUserID(ctx context.Context, cfg *config.Config) (string, error) {
	if err := ValidateDeepSeekAuthenticatedUserContext(ctx); err != nil {
		return "", err
	}
	userID, _ := ctx.Value(ctxkey.UserID).(int64)
	secret, err := deepSeekUserIdentitySecret(cfg)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(deepSeekUserIDDomain))
	_, _ = mac.Write([]byte(strconv.FormatInt(userID, 10)))
	return deepSeekUserIDPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func consumeDeepSeekIdentityJSONValue(decoder *json.Decoder, depth int) error {
	if depth >= deepSeekResponsesMaxJSONDepth {
		return errors.New("DeepSeek request exceeds the supported JSON nesting depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := consumeDeepSeekIdentityJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("DeepSeek request has an invalid JSON object boundary")
		}
	case '[':
		for decoder.More() {
			if err := consumeDeepSeekIdentityJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("DeepSeek request has an invalid JSON array boundary")
		}
	default:
		return errors.New("DeepSeek request has an invalid JSON boundary")
	}
	return nil
}

func validateDeepSeekMetadataValue(decoder *json.Decoder, depth int) error {
	if depth >= deepSeekResponsesMaxJSONDepth {
		return errors.New("DeepSeek request exceeds the supported JSON nesting depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return nil
	}
	if token != json.Delim('{') {
		return errors.New("DeepSeek Anthropic metadata must be an object")
	}
	seenUserID := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("DeepSeek request contains a non-string JSON key")
		}
		if strings.EqualFold(key, "user_id") {
			if key != "user_id" {
				return ErrDeepSeekUserIdentityNonCanonicalJSONKey
			}
			if seenUserID {
				return ErrDeepSeekUserIdentityDuplicateJSONKey
			}
			seenUserID = true
		}
		if err := consumeDeepSeekIdentityJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return errors.New("DeepSeek request has an invalid metadata object boundary")
	}
	return nil
}

func validateDeepSeekIdentityObjectKeys(body []byte, canonicalKey string, validateMetadata bool) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return errors.New("DeepSeek request body must be a JSON object")
	}
	seen := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("DeepSeek request contains a non-string JSON key")
		}
		if strings.EqualFold(key, canonicalKey) {
			if key != canonicalKey {
				return ErrDeepSeekUserIdentityNonCanonicalJSONKey
			}
			if seen {
				return ErrDeepSeekUserIdentityDuplicateJSONKey
			}
			seen = true
		}
		if validateMetadata && key == canonicalKey {
			if err := validateDeepSeekMetadataValue(decoder, 1); err != nil {
				return err
			}
		} else if err := consumeDeepSeekIdentityJSONValue(decoder, 1); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return errors.New("DeepSeek request has an invalid JSON object boundary")
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("DeepSeek request contains trailing JSON data")
		}
		return err
	}
	return nil
}

// ValidateDeepSeekUserIdentityRequest rejects ambiguous official isolation
// fields before scheduling. Unknown provider-native fields remain untouched.
func ValidateDeepSeekUserIdentityRequest(body []byte, protocol DeepSeekUserIdentityProtocol) error {
	switch protocol {
	case DeepSeekUserIdentityChatCompletions:
		return validateDeepSeekIdentityObjectKeys(body, "user_id", false)
	case DeepSeekUserIdentityResponses:
		_, err := scanDeepSeekResponsesJSON(body)
		return err
	case DeepSeekUserIdentityMessages:
		return validateDeepSeekIdentityObjectKeys(body, "metadata", true)
	default:
		return fmt.Errorf("unsupported DeepSeek user identity protocol %q", protocol)
	}
}

func applyDeepSeekAuthenticatedUserID(
	ctx context.Context,
	cfg *config.Config,
	account *Account,
	protocol DeepSeekUserIdentityProtocol,
	body []byte,
) ([]byte, error) {
	if account == nil || !account.IsDeepSeekAPIKey() {
		return body, nil
	}
	if err := ValidateDeepSeekUserIdentityRequest(body, protocol); err != nil {
		return nil, err
	}
	if account.ResolveDeepSeekUserIsolationMode() == DeepSeekUserIsolationModeOff {
		return body, nil
	}
	upstreamUserID, err := DeriveDeepSeekAuthenticatedUserID(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var path string
	switch protocol {
	case DeepSeekUserIdentityChatCompletions:
		path = "user_id"
	case DeepSeekUserIdentityResponses:
		path = "user"
	case DeepSeekUserIdentityMessages:
		path = "metadata.user_id"
	default:
		return nil, fmt.Errorf("unsupported DeepSeek user identity protocol %q", protocol)
	}
	return sjson.SetBytes(body, path, upstreamUserID)
}
