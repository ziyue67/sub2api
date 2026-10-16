package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	authFailureLimit  = 10
	authFailureWindow = 15 * time.Minute
	authBanDuration   = time.Hour
)

// Check and record use the same atomic script. A ban has a fixed expiry;
// blocked requests neither extend it nor perform password hashing.
// A malformed ban without TTL is repaired to a full hour as a fail-safe.
var authAbuseScript = redis.NewScript(`
local banned = redis.call('PTTL', KEYS[2])
if banned == -1 then
  redis.call('PEXPIRE', KEYS[2], ARGV[3])
  return tonumber(ARGV[3])
end
if banned > 0 then return banned end
if ARGV[1] == 'check' then return 0 end
local failures = redis.call('INCR', KEYS[1])
if failures == 1 or redis.call('PTTL', KEYS[1]) == -1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
if failures >= tonumber(ARGV[4]) then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
  redis.call('DEL', KEYS[1])
  return tonumber(ARGV[3])
end
return 0
`)

// AuthAbuseGuard protects credential/signup endpoints and enables database signup
// accounting for every creation path, including OAuth callbacks/completion.
// It deliberately uses the trusted proxy chain, never legacy raw-header trust.
func AuthAbuseGuard(client *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isAuthAbuseProtectedEndpoint(c) {
			c.Next()
			return
		}
		address := ippkg.GetTrustedClientIP(c)
		key := authguard.IPKey(address)
		c.Request = c.Request.WithContext(authguard.WithRegistrationIP(c.Request.Context(), address))
		if key == "" || client == nil {
			abortRateLimit(c, time.Minute)
			return
		}

		ttl, err := runAuthAbuse(c.Request.Context(), client, key, "check")
		if err != nil {
			slog.Error("auth abuse guard unavailable")
			abortRateLimit(c, time.Minute)
			return
		}
		if ttl > 0 {
			abortRateLimit(c, time.Duration(ttl)*time.Millisecond)
			return
		}

		c.Next()
		if !isCredentialAttempt(c) || !isCredentialFailure(c) {
			return
		}
		// Preserve failure history across successful logins so a known valid
		// credential cannot be used to reset an attacker's password spray.
		// Client cancellation must not discard an already evaluated failure.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
		defer cancel()
		if _, err := runAuthAbuse(ctx, client, key, "failure"); err != nil {
			slog.Error("auth abuse guard could not record failure")
		}
	}
}

func isAuthAbuseProtectedEndpoint(c *gin.Context) bool {
	path := c.FullPath()
	// Session maintenance, recovery and unrelated validation endpoints do not
	// depend on the new guard. OAuth entry/completion stays protected so auto-
	// creation cannot bypass signup accounting via another provider.
	return isCredentialAttempt(c) || strings.HasSuffix(path, "/register") ||
		strings.HasSuffix(path, "/send-verify-code") ||
		strings.HasSuffix(path, "/passkey/login/begin") ||
		(strings.Contains(path, "/oauth/") && !strings.Contains(path, "/payment/"))
}

func isCredentialFailure(c *gin.Context) bool {
	status := c.Writer.Status()
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError || status == http.StatusTooManyRequests {
		return false
	}
	// TOTP_INVALID_CODE is HTTP 400; account-disabled/backend-mode 403 and
	// malformed requests or missing captcha/session state are not credential
	// failures. Classify by the server's reason instead of broad HTTP status.
	switch response.ErrorReason(c) {
	case "INVALID_CREDENTIALS", "TOTP_INVALID_CODE", "PASSKEY_VERIFICATION_FAILED":
		return true
	default:
		return false
	}
}

func runAuthAbuse(ctx context.Context, client *redis.Client, key, action string) (int64, error) {
	// Hash tags keep both keys in the same slot if Redis Cluster is adopted.
	prefix := "auth_abuse:{" + key + "}:"
	return authAbuseScript.Run(ctx, client, []string{prefix + "failures", prefix + "banned"},
		action, authFailureWindow.Milliseconds(), authBanDuration.Milliseconds(), authFailureLimit).Int64()
}

func isCredentialAttempt(c *gin.Context) bool {
	if c.Request.Method != http.MethodPost {
		return false
	}
	path := c.FullPath()
	return strings.HasSuffix(path, "/login") ||
		strings.HasSuffix(path, "/login/2fa") ||
		strings.HasSuffix(path, "/bind-login") ||
		strings.HasSuffix(path, "/passkey/login/finish")
}
