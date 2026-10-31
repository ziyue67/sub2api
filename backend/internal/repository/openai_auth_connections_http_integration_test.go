//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type connectionDBReader struct{ accounts service.AccountRepository }

func (r connectionDBReader) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	return r.accounts.GetByID(ctx, id)
}
func (r connectionDBReader) GetProxy(context.Context, int64) (*service.Proxy, error) {
	return nil, fmt.Errorf("test does not configure proxies")
}

type connectionHTTPFixture struct {
	db        *sql.DB
	accountID int64
	repo      service.OpenAIAuthConnectionRepository
	svc       *service.OpenAIOAuthReauthService
	request   func(string, string, string) *httptest.ResponseRecorder
	encryptor service.SecretEncryptor
	accounts  service.AccountRepository
	restart   func() *service.OpenAIOAuthReauthService
}

func newConnectionHTTPFixture(t *testing.T) *connectionHTTPFixture {
	t.Helper()
	ctx := context.Background()
	db := repository.ChannelOperationsTestDatabase()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	accounts := repository.NewAccountRepository(client, db, nil)
	a := &service.Account{Name: "issue594-http", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "synthetic-legacy-token", "refresh_token": "synthetic-legacy-refresh", "client_id": "legacy-client"}}
	require.NoError(t, accounts.Create(ctx, a))
	t.Cleanup(func() { _, err := db.Exec(`DELETE FROM accounts WHERE id=$1`, a.ID); require.NoError(t, err) })
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)}}
	encryptor, err := repository.NewAESEncryptor(cfg)
	require.NoError(t, err)
	repo := repository.NewOpenAIOAuthReauthRepository(db)
	updater, ok := accounts.(service.OpenAIOAuthReauthCredentialUpdater)
	require.True(t, ok)
	svc := service.NewOpenAIOAuthReauthService(repo, connectionDBReader{accounts}, updater, service.NewOpenAIOAuthService(nil, repository.NewOpenAIOAuthClient()), encryptor, true, nil, nil)
	h := admin.NewOpenAIOAuthReauthHandler(svc)
	router := gin.New()
	router.GET("/accounts/:id/auth-connections", h.ListAuthConnections)
	router.POST("/accounts/:id/auth-connections", h.CreateAuthConnection)
	router.PATCH("/accounts/:id/auth-connections/:connection_id", h.UpdateAuthConnection)
	router.PUT("/accounts/:id/auth-connections/:connection_id/api-key", h.RotateConnectionAPIKey)
	router.POST("/accounts/:id/auth-connections/use-legacy", h.SelectLegacyAuthConnection)
	router.POST("/accounts/:id/auth-connections/:connection_id/authorize", h.BeginConnectionAuthorization)
	router.POST("/accounts/:id/auth-connections/:connection_id/callback", h.CompleteConnectionAuthorization)
	router.POST("/accounts/:id/auth-connections/:connection_id/revoke", h.RevokeAuthConnection)
	f := &connectionHTTPFixture{db: db, accountID: a.ID, repo: repo.(service.OpenAIAuthConnectionRepository), svc: svc}
	f.encryptor = encryptor
	f.accounts = accounts
	f.restart = func() *service.OpenAIOAuthReauthService {
		return service.NewOpenAIOAuthReauthService(repository.NewOpenAIOAuthReauthRepository(db), connectionDBReader{accounts}, updater, service.NewOpenAIOAuthService(nil, repository.NewOpenAIOAuthClient()), encryptor, true, nil, nil)
	}
	f.request = func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, fmt.Sprintf("/accounts/%d/auth-connections%s", a.ID, path), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}
	return f
}
func connectionResponse(t *testing.T, w *httptest.ResponseRecorder) *service.OpenAIAuthConnection {
	t.Helper()
	require.Equal(t, 200, w.Code, w.Body.String())
	var body struct {
		Data service.OpenAIAuthConnection `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return &body.Data
}

func TestIssue594HTTPDatabaseCoexistenceRotationUnlinkAndLegacy(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	ctx := context.Background()

	first := f.seedHistoricalAPIKey(t, "First key", "synthetic-first-key")
	second := f.seedHistoricalAPIKey(t, "Second key", "synthetic-second-key")
	require.NotEqual(t, first.ID, second.ID)
	var ciphertext string
	require.NoError(t, f.db.QueryRow(`SELECT credentials_ciphertext FROM openai_auth_connections WHERE id=$1`, first.ID).Scan(&ciphertext))
	require.NotContains(t, ciphertext, "synthetic-first-key")
	var peerCipher string
	require.NoError(t, f.db.QueryRow(`SELECT credentials_ciphertext FROM openai_auth_connections WHERE id=$1`, second.ID).Scan(&peerCipher))
	_, errSwap := f.db.Exec(`UPDATE openai_auth_connections SET credentials_ciphertext=$2 WHERE id=$1`, first.ID, peerCipher)
	require.NoError(t, errSwap)
	_, errBound := f.svc.ConnectionAccessToken(ctx, f.accountID, first.ID)
	require.Error(t, errBound, "ciphertexts cannot be transplanted between registrations")
	_, errSwap = f.db.Exec(`UPDATE openai_auth_connections SET credentials_ciphertext=$2 WHERE id=$1`, first.ID, ciphertext)
	require.NoError(t, errSwap)
	selected := connectionResponse(t, f.request("PATCH", "/"+first.ID, `{"action":"select","revision":1}`))
	require.True(t, selected.Preferred)
	var extra map[string]any
	var raw []byte
	require.NoError(t, f.db.QueryRow(`SELECT extra FROM accounts WHERE id=$1`, f.accountID).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.Equal(t, true, extra["openai_connections_managed"])
	// Old clients cannot remove connection ownership by overwriting extra.
	_, err := f.db.Exec(`UPDATE accounts SET extra='{}' WHERE id=$1`, f.accountID)
	require.NoError(t, err)
	require.NoError(t, f.db.QueryRow(`SELECT extra FROM accounts WHERE id=$1`, f.accountID).Scan(&raw))
	require.Contains(t, string(raw), "openai_connections_managed")
	rotated := connectionResponse(t, f.request("PUT", "/"+first.ID+"/api-key", fmt.Sprintf(`{"revision":%d,"api_key":"synthetic-rotated-key"}`, selected.Revision)))
	token, err := f.svc.ConnectionAccessToken(ctx, f.accountID, first.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-rotated-key", token)
	peerToken, err := f.svc.ConnectionAccessToken(ctx, f.accountID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-second-key", peerToken)
	stale := f.request("PATCH", "/"+first.ID, `{"action":"unlink","revision":1}`)
	require.Equal(t, 409, stale.Code)
	unlinked := connectionResponse(t, f.request("PATCH", "/"+first.ID, fmt.Sprintf(`{"action":"unlink","revision":%d}`, rotated.Revision)))
	require.Equal(t, "unlinked", unlinked.Status)
	_, err = f.svc.ConnectionAccessToken(ctx, f.accountID, first.ID)
	require.Error(t, err)
	peerToken, err = f.svc.ConnectionAccessToken(ctx, f.accountID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-second-key", peerToken)
	list := f.request("GET", "", "")
	require.Equal(t, 200, list.Code)
	for _, secret := range []string{"synthetic-first-key", "synthetic-second-key", "synthetic-rotated-key", "credentials_ciphertext", "synthetic-legacy-token"} {
		require.NotContains(t, list.Body.String(), secret)
	}
	require.Contains(t, list.Body.String(), `"legacy":true`)
	legacy := f.request("POST", "/use-legacy", `{}`)
	require.Equal(t, 200, legacy.Code, legacy.Body.String())
	var managed bool
	managed, err = f.repo.AuthConnectionsManaged(ctx, f.accountID)
	require.NoError(t, err)
	require.False(t, managed)
	var primary string
	require.NoError(t, f.db.QueryRow(`SELECT credentials->>'refresh_token' FROM accounts WHERE id=$1`, f.accountID).Scan(&primary))
	require.Equal(t, "synthetic-legacy-refresh", primary)
	// Repeat mapping without copying or rotating the old grant.
	a, err := f.svc.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	b, err := f.svc.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	require.Equal(t, a[len(a)-1].ID, b[len(b)-1].ID)
}

func TestIssue594DatabaseCASAndCrossInstanceLease(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	ctx := context.Background()
	c := connectionResponse(t, f.request("POST", "", `{"kind":"siwc","label":"SIWC registration"}`))
	other := repository.NewOpenAIOAuthReauthRepository(f.db).(service.OpenAIAuthConnectionRepository)
	acquired, err := f.repo.AcquireAuthConnectionLease(ctx, f.accountID, c.ID, "worker-one")
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = other.AcquireAuthConnectionLease(ctx, f.accountID, c.ID, "worker-two")
	require.NoError(t, err)
	require.False(t, acquired)
	_, disableErr := f.svc.UpdateAuthConnection(ctx, f.accountID, c.ID, service.UpdateOpenAIAuthConnectionInput{Revision: c.Revision, Action: "disable"})
	require.Error(t, disableErr, "disable must not discard a concurrently rotating grant")
	require.NoError(t, other.ReleaseAuthConnectionLease(ctx, f.accountID, c.ID, "worker-two"))
	acquired, err = other.AcquireAuthConnectionLease(ctx, f.accountID, c.ID, "worker-two")
	require.NoError(t, err)
	require.False(t, acquired)
	require.NoError(t, f.repo.ReleaseAuthConnectionLease(ctx, f.accountID, c.ID, "worker-one"))
	acquired, err = other.AcquireAuthConnectionLease(ctx, f.accountID, c.ID, "worker-two")
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, other.ReleaseAuthConnectionLease(ctx, f.accountID, c.ID, "worker-two"))
	// Re-read the current lease epoch; pre-lease snapshots are now fenced.
	currentRows, readErr := f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, readErr)
	c = currentRows[0]
	// Parallel replacements at one revision have exactly one winner.
	var wg sync.WaitGroup
	outcomes := make(chan bool, 2)
	failures := make(chan error, 2)
	for _, r := range []service.OpenAIAuthConnectionRepository{f.repo, other} {
		wg.Add(1)
		go func(r service.OpenAIAuthConnectionRepository) {
			defer wg.Done()
			copy := *c
			copy.Label = "same registration"
			ok, e := r.CASAuthConnection(ctx, &copy, c.Revision)
			outcomes <- ok
			failures <- e
		}(r)
	}
	wg.Wait()
	close(outcomes)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	winners := 0
	for ok := range outcomes {
		if ok {
			winners++
		}
	}
	require.Equal(t, 1, winners)
	rows, err := f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(2), rows[0].Revision)
	// Schema/legacy mapping remains idempotent under the actual migration runner.
	require.NoError(t, repository.ApplyMigrations(ctx, f.db))
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err = f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// Explicit upgrade fixture, not a management API shortcut: production no longer
// permits creating these grants, but pre-upgrade encrypted rows remain valid.
func (f *connectionHTTPFixture) seedHistoricalAPIKey(t *testing.T, label, key string) *service.OpenAIAuthConnection {
	t.Helper()
	c := &service.OpenAIAuthConnection{ID: uuid.NewString(), AccountID: f.accountID, Kind: "api_key", Label: label, Status: "active", RefreshState: "idle", Revision: 1, Scopes: []string{}, Capabilities: []string{"responses", "chat_completions", "embeddings"}, Resource: "https://api.openai.com/v1"}
	raw, err := json.Marshal(map[string]any{"_connection_id": c.ID, "_account_id": strconv.FormatInt(c.AccountID, 10), "_connection_kind": c.Kind, "_connection_subject": c.Subject, "api_key": key})
	require.NoError(t, err)
	c.CredentialsCiphertext, err = f.encryptor.Encrypt(string(raw))
	require.NoError(t, err)
	require.NoError(t, f.repo.CreateAuthConnection(context.Background(), c))
	return c
}

func TestOpenAIConnectionDatabaseRegistrationBoundary(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	rejected := f.request("POST", "", `{"kind":"api_key","label":"Mixed","api_key":"synthetic-rejected"}`)
	require.Equal(t, 400, rejected.Code, rejected.Body.String())
	for _, kind := range []string{"codex", "bps", "siwc"} {
		created := connectionResponse(t, f.request("POST", "", fmt.Sprintf(`{"kind":%q,"label":"Synthetic OAuth"}`, kind)))
		require.Equal(t, kind, created.Kind)
	}
	// Type, not credential shape, controls the account management boundary.
	_, err := f.db.Exec(`UPDATE accounts SET type='apikey' WHERE id=$1`, f.accountID)
	require.NoError(t, err)
	for _, request := range []struct{ method, body string }{
		{"GET", ""}, {"POST", `{"kind":"codex","label":"Denied"}`},
	} {
		response := f.request(request.method, "", request.body)
		require.Equal(t, 400, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "OPENAI_CONNECTION_ACCOUNT_INVALID")
	}
	rows, err := f.repo.ListAuthConnections(context.Background(), f.accountID)
	require.NoError(t, err)
	require.Len(t, rows, 3, "stored grants must remain untouched")
}
