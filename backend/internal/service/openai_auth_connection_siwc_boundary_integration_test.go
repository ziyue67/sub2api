//go:build integration

package service_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type siwcBoundaryAccounts struct{ repo service.AccountRepository }

func (r siwcBoundaryAccounts) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	return r.repo.GetByID(ctx, id)
}
func (siwcBoundaryAccounts) GetProxy(context.Context, int64) (*service.Proxy, error) { return nil, nil }

// Exercise the existing last priority/admission phase after the transport's
// initial validation, representing disable/rotation while final preparation is
// in progress. The hook only changes real PostgreSQL state, not the fence.
type siwcFinalPreparationContext struct {
	context.Context
	once   sync.Once
	change func()
}

func (c *siwcFinalPreparationContext) Value(key any) any {
	typ := reflect.TypeOf(key)
	if typ != nil && typ.PkgPath() == "github.com/Wei-Shaw/sub2api/internal/service" && typ.Name() == "priorityLatencyTurnKey" {
		c.once.Do(c.change)
	}
	return c.Context.Value(key)
}

type siwcBoundaryHTTP struct {
	target string
	calls  atomic.Int32
}

func (u *siwcBoundaryHTTP) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	copy := r.Clone(r.Context())
	target := *r.URL
	target.Scheme = "http"
	target.Host = u.target
	copy.URL = &target
	copy.Host = ""
	return http.DefaultClient.Do(copy)
}
func (u *siwcBoundaryHTTP) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

func TestIssue594SIWCFinalEgressFencesRevocationAndRevision(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("siwc_boundary_test"), tcpostgres.WithUsername("fixture"), tcpostgres.WithPassword("synthetic"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	accounts := repository.NewAccountRepository(client, db, nil)
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)}}
	enc, err := repository.NewAESEncryptor(cfg)
	require.NoError(t, err)
	repo := repository.NewOpenAIOAuthReauthRepository(db)
	svc := service.NewOpenAIOAuthReauthService(repo, siwcBoundaryAccounts{accounts}, accounts.(service.OpenAIOAuthReauthCredentialUpdater), service.NewOpenAIOAuthService(nil, repository.NewOpenAIOAuthClient()), enc, true, nil, nil)
	for _, action := range []string{"unchanged", "disable", "revision"} {
		t.Run(action, func(t *testing.T) {
			a := &service.Account{Name: "siwc boundary", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{}, Extra: map[string]any{}}
			require.NoError(t, accounts.Create(ctx, a))
			c, e := svc.CreateAuthConnection(ctx, a.ID, service.CreateOpenAIAuthConnectionInput{Kind: "siwc", Label: "final send"})
			require.NoError(t, e)
			c.Status = "active"
			c.Capabilities = []string{"responses"}
			r := repo.(service.OpenAIAuthConnectionRepository)
			changed, e := r.CASAuthConnection(ctx, c, c.Revision)
			require.NoError(t, e)
			require.True(t, changed)
			rows, e := r.ListAuthConnections(ctx, a.ID)
			require.NoError(t, e)
			c = rows[0]
			selected, e := svc.UpdateAuthConnection(ctx, a.ID, c.ID, service.UpdateOpenAIAuthConnectionInput{Action: "select", Revision: c.Revision})
			require.NoError(t, e)
			a.AuthConnectionID = c.ID
			a.AuthConnectionRevision = selected.Revision
			a.Extra[service.OpenAISIWCEnabledKey] = true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer synthetic-siwc-selected", r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("Cookie"))
				require.Empty(t, r.Header.Get("x-codex-turn-state"))
				w.WriteHeader(200)
				_, _ = io.WriteString(w, "synthetic OK")
			}))
			defer server.Close()
			upstream := &siwcBoundaryHTTP{target: strings.TrimPrefix(server.URL, "http://")}
			gateway := service.NewOpenAIGatewayService(accounts, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, service.WithOpenAIAuthConnections(svc))
			defer gateway.StopOpenAICodexTicketHarvester()
			defer gateway.CloseOpenAIWSPool()
			var prepared atomic.Bool
			boundary := &siwcFinalPreparationContext{Context: ctx, change: func() {
				prepared.Store(true)
				switch action {
				case "disable":
					_, e := svc.UpdateAuthConnection(ctx, a.ID, c.ID, service.UpdateOpenAIAuthConnectionInput{Action: "disable", Revision: selected.Revision})
					require.NoError(t, e)
				case "revision":
					rows, e := r.ListAuthConnections(ctx, a.ID)
					require.NoError(t, e)
					changed, e := r.CASAuthConnection(ctx, rows[0], rows[0].Revision)
					require.NoError(t, e)
					require.True(t, changed)
				}
			}}
			request, e := http.NewRequestWithContext(boundary, http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"model":"synthetic","stream":true,"store":false,"input":"synthetic user content"}`))
			require.NoError(t, e)
			request.Header.Set("Authorization", "Bearer synthetic-siwc-selected")
			request.Header.Set("Cookie", "synthetic-parent-cookie")
			request.Header.Set("x-codex-turn-state", "synthetic-parent-ticket")
			response, e := service.Issue594SendProductionUpstream(gateway, request, a)
			require.True(t, prepared.Load(), "test must mutate after initial transport validation")
			if action == "unchanged" {
				require.NoError(t, e)
				require.Equal(t, int32(1), upstream.calls.Load())
				require.NotNil(t, response)
				_ = response.Body.Close()
			} else {
				require.Error(t, e)
				require.Nil(t, response)
				require.Zero(t, upstream.calls.Load(), "no content may be sent after final preparation changed authorization")
			}
		})
	}
}
