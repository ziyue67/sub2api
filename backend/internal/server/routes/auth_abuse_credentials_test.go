package routes

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// Real handlers/services with offline repositories and caches. The OAuth fixture
// uses a real browser-bound pending session; no handler or guard is replaced.
func newAuthCredentialFixture(t *testing.T) *handler.Handlers {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// SQLite pragmas are connection-scoped. Keep the fixture on the same
	// connection so both Ent schema setup and handler queries enforce FKs.
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	_, err = service.NewAuthPendingIdentityService(client).CreatePendingSession(context.Background(), service.CreatePendingAuthSessionInput{
		SessionToken: "test-pending-session", Intent: "login",
		Identity:          service.PendingAuthIdentityKey{ProviderType: "oidc", ProviderKey: "oidc", ProviderSubject: "test-subject"},
		BrowserSessionKey: "test-browser-key",
	})
	require.NoError(t, err)
	cfg := &config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled: true, RPID: "example.com", RPDisplayName: "Test", RPOrigins: []string{"https://example.com"},
	}}
	users := &authCredentialUserRepo{}
	auth := service.NewAuthService(client, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	totp := service.NewTotpService(users, &authCredentialEncryptor{}, &authCredentialTotpCache{}, nil, nil, nil)
	passkeys, err := service.NewPasskeyService(cfg, nil, &authCredentialPasskeySessions{}, users)
	require.NoError(t, err)
	settings := service.NewSettingService(&authCredentialSettingRepo{}, cfg)
	return &handler.Handlers{
		Auth:    handler.NewAuthHandler(cfg, auth, nil, nil, nil, nil, totp, nil),
		Passkey: handler.NewPasskeyHandler(passkeys, auth, settings),
		Setting: &handler.SettingHandler{},
	}
}

type authCredentialUserRepo struct{ service.UserRepository }

func (*authCredentialUserRepo) GetByEmail(context.Context, string) (*service.User, error) {
	return nil, service.ErrUserNotFound
}

func (*authCredentialUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	secret := "test-encrypted-secret"
	return &service.User{ID: 1, TotpEnabled: true, TotpSecretEncrypted: &secret}, nil
}

type authCredentialTotpCache struct{ service.TotpCache }

func (*authCredentialTotpCache) GetLoginSession(context.Context, string) (*service.TotpLoginSession, error) {
	return &service.TotpLoginSession{UserID: 1, Email: "test@example.com", TokenExpiry: time.Now().Add(time.Minute)}, nil
}

func (*authCredentialTotpCache) GetVerifyAttempts(context.Context, int64) (int, error) {
	return 0, nil
}

func (*authCredentialTotpCache) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	return 1, nil
}

type authCredentialEncryptor struct{ service.SecretEncryptor }

func (*authCredentialEncryptor) Decrypt(string) (string, error) { return "JBSWY3DPEHPK3PXP", nil }

type authCredentialPasskeySessions struct{ service.PasskeySessionStore }

func (*authCredentialPasskeySessions) Consume(context.Context, string) (*service.PasskeySession, error) {
	return &service.PasskeySession{Kind: "login"}, nil
}

type authCredentialSettingRepo struct{ service.SettingRepository }

func (*authCredentialSettingRepo) GetValue(context.Context, string) (string, error) {
	return "true", nil
}

func TestAuthRoutesCountCredentialFailures(t *testing.T) {
	fixture := newAuthCredentialFixture(t)
	for _, tc := range []struct {
		path, body, reason string
		status             int
	}{
		{"/api/v1/auth/login/2fa", `{"temp_token":"test-session","totp_code":"xxxxxx"}`, "TOTP_INVALID_CODE", 400},
		{"/api/v1/auth/oauth/pending/bind-login", `{"email":"unknown@example.com","password":"incorrect"}`, "INVALID_CREDENTIALS", 401},
		{"/api/v1/auth/passkey/login/finish", `{"session_token":"test-session","credential":{}}`, "PASSKEY_VERIFICATION_FAILED", 401},
	} {
		t.Run(tc.path, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			router := newAuthRoutesTestRouter(rdb, fixture)
			require.NoError(t, router.SetTrustedProxies(nil))
			request := func(path, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = "192.0.2.1:1234"
				req.AddCookie(&http.Cookie{Name: "oauth_pending_session", Value: base64.RawURLEncoding.EncodeToString([]byte("test-pending-session"))})
				req.AddCookie(&http.Cookie{Name: "oauth_pending_browser_session", Value: base64.RawURLEncoding.EncodeToString([]byte("test-browser-key"))})
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w
			}
			// Malformed requests through the real route must not consume failure
			// budget; advance the existing minute limiter before real failures.
			for i := 0; i < 10; i++ {
				w := request(tc.path, `{}`)
				require.Equal(t, http.StatusBadRequest, w.Code)
			}
			mr.FastForward(time.Minute + time.Second)
			for i := 0; i < 10; i++ {
				w := request(tc.path, tc.body)
				require.Equal(t, tc.status, w.Code)
				require.Contains(t, w.Body.String(), tc.reason)
			}
			w := request("/api/v1/auth/register", `{}`)
			require.Equal(t, http.StatusTooManyRequests, w.Code)
			require.Equal(t, "3600", w.Header().Get("Retry-After"))
		})
	}
}
