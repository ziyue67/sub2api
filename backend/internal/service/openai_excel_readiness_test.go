//go:build unit

package service

import (
	"context"
	"maps"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExcelAuthorizationStateSurvivesModelAndTestPaths(t *testing.T) {
	for _, tc := range []struct{ status, workerError, reason string }{
		{"", "", "REQUIRED"}, {"queued", "", "PENDING"}, {"running", "", "PENDING"},
		{"callback_processing", "", "PENDING"}, {"failed", "upstream secret=do-not-expose", "FAILED"},
		{"failed", "stage=web_login, reason=security_check, secret=do-not-expose", "VERIFICATION_REQUIRED"},
		{"failed", "reason=additional_verification", "VERIFICATION_REQUIRED"}, {"succeeded", "", "REQUIRED"},
	} {
		t.Run(tc.status+tc.reason, func(t *testing.T) {
			reauth, reader, repo, _, _ := newExcelReauthTestService(t)
			account := reader.account
			account.Status = StatusActive
			account.Extra = map[string]any{"openai_excel_bps": true}
			if tc.status != "" {
				repo.task = &OpenAIOAuthReauthTaskRecord{OAuthProfile: "excel", Status: tc.status, Error: tc.workerError}
			}
			upstream := &httpUpstreamRecorder{}
			gateway := openAIClientToolsTestService(upstream)
			gateway.excelOAuthReauth = reauth
			_, err := gateway.FetchOpenAIModelsList(context.Background(), account)
			require.Equal(t, "OPENAI_EXCEL_AUTH_"+tc.reason, infraerrors.Reason(err))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/test", nil)
			svc := &AccountTestService{openaiGatewayService: gateway}
			err = svc.testExcelBPSAccountConnection(c, account, "gpt-6-astra", "hi")
			require.ErrorContains(t, err, "basispoints_auth_"+strings.ToLower(tc.reason))
			require.ErrorContains(t, err, "Credential Operations")
			require.NotContains(t, rec.Body.String(), "do-not-expose")
			require.Nil(t, upstream.lastReq)
		})
	}
}

func TestExcelMissingConfigurationAndValidGrant(t *testing.T) {
	reauth, reader, repo, _, _ := newExcelReauthTestService(t)
	repo.config = nil
	_, err := reauth.ExcelAccessToken(context.Background(), reader.account, nil)
	require.Equal(t, "OPENAI_EXCEL_AUTH_CONFIG_REQUIRED", infraerrors.Reason(err))
	// An old failure cannot override a subsequently saved valid grant.
	repo.task = &OpenAIOAuthReauthTaskRecord{OAuthProfile: "excel", Status: "failed"}
	creds := excelTestCredentials("ready", time.Now().Add(time.Hour))
	storeExcelTestCredentials(t, reauth, repo, creds)
	token, err := reauth.ExcelAccessToken(context.Background(), reader.account, nil)
	require.NoError(t, err)
	require.Equal(t, creds["access_token"], token)
}

type readinessAccountRepo struct {
	adminExcelBPSGroupRepo
	preparation map[int64]any
}

func (r *readinessAccountRepo) BulkUpdate(ctx context.Context, ids []int64, update AccountBulkUpdate) (int64, error) {
	if r.preparation == nil {
		r.preparation = map[int64]any{}
	}
	for _, id := range ids {
		r.preparation[id] = update.ExcelBPSAuthorizationPending[id]
	}
	return r.accountRepoStubForBulkUpdate.BulkUpdate(ctx, ids, update)
}

func (r *readinessAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	a := *r.getByIDAccounts[id]
	a.Extra = maps.Clone(a.Extra)
	a.Credentials = maps.Clone(a.Credentials)
	return &a, nil
}

func TestAdminExcelActivationPreservesRouteAcrossEntryPoints(t *testing.T) {
	for _, method := range []string{"create", "update", "extra", "bulk"} {
		t.Run(method, func(t *testing.T) {
			reauth, reader, loginRepo, _, _ := newExcelReauthTestService(t)
			account := reader.account
			account.Status = StatusActive
			account.Extra = map[string]any{}
			loginRepo.task = &OpenAIOAuthReauthTaskRecord{OAuthProfile: "excel", Status: "failed", Error: "reason=security_check"}
			repo := &readinessAccountRepo{adminExcelBPSGroupRepo: adminExcelBPSGroupRepo{accountRepoStubForBulkUpdate: accountRepoStubForBulkUpdate{
				getByIDAccounts: map[int64]*Account{account.ID: account}, getByIDsAccounts: []*Account{account},
			}}}
			svc := &adminServiceImpl{accountRepo: repo, excelOAuthReauth: reauth}
			extra := map[string]any{"openai_excel_bps": true}
			ctx := context.Background()
			var err error
			switch method {
			case "create":
				_, err = svc.CreateAccount(ctx, &CreateAccountInput{Name: "test", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: account.Credentials, Extra: extra, SkipDefaultGroupBind: true})
			case "update":
				_, err = svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{Extra: extra})
			case "extra":
				err = svc.UpdateAccountExtra(ctx, account.ID, extra)
			case "bulk":
				_, err = svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: extra})
			}
			require.NoError(t, err)
			switch method {
			case "create":
				require.Equal(t, true, repo.createAccount.Extra[ExcelBPSAuthorizationPendingKey])
			case "update":
				require.Equal(t, true, repo.updatedAccounts[0].Extra[ExcelBPSAuthorizationPendingKey])
			case "extra":
				require.Equal(t, true, repo.extraUpdates[0][ExcelBPSAuthorizationPendingKey])
			case "bulk":
				require.Equal(t, true, repo.preparation[account.ID])
			}
			require.False(t, account.IsExcelBPSEnabled())
		})
	}
}

func TestAdminExcelActivationQueuesOnceAndAcceptsReadyGrant(t *testing.T) {
	reauth, reader, loginRepo, _, _ := newExcelReauthTestService(t)
	account := reader.account
	account.Status = StatusActive
	account.Extra = map[string]any{}
	repo := &readinessAccountRepo{adminExcelBPSGroupRepo: adminExcelBPSGroupRepo{accountRepoStubForBulkUpdate: accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{account.ID: account}}}}
	svc := &adminServiceImpl{accountRepo: repo, excelOAuthReauth: reauth}
	extra := map[string]any{"openai_excel_bps": true}
	for range 2 {
		err := svc.UpdateAccountExtra(context.Background(), account.ID, extra)
		require.NoError(t, err)
	}
	require.Len(t, repo.extraUpdates, 2)
	require.Nil(t, loginRepo.task, "worker queues only after the routing request is persisted")
	loginRepo.ids = []int64{account.ID}
	require.NoError(t, reauth.QueueMissingExcelAuthorizations(context.Background()))
	require.Equal(t, "excel", loginRepo.task.OAuthProfile)
	storeExcelTestCredentials(t, reauth, loginRepo, excelTestCredentials("ready", time.Now().Add(time.Hour)))
	require.NoError(t, svc.UpdateAccountExtra(context.Background(), account.ID, extra))
	require.Len(t, repo.extraUpdates, 3)
	require.NotEqual(t, true, repo.extraUpdates[2][ExcelBPSAuthorizationPendingKey])
}

func TestExcelScopedModelDiscoveryKeepsNativeModels(t *testing.T) {
	reauth, _, _, _, _ := newExcelReauthTestService(t)
	account := excelAccount()
	account.Extra["openai_excel_bps_models"] = []string{"gpt-6-astra"}
	_, calls := newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"gpt-6-astra","display_name":"Astra"},{"slug":"gpt-6-sol","display_name":"Sol"}]}`)
	nativeAccount := newCodexModelsTestAccount()
	account.Credentials = nativeAccount.Credentials
	gateway := openAIClientToolsTestService(&httpUpstreamRecorder{})
	gateway.excelOAuthReauth = reauth
	result, err := gateway.FetchOpenAIModelsList(context.Background(), account)
	require.NoError(t, err)
	require.Contains(t, string(result.Body), "gpt-6-sol")
	require.NotContains(t, string(result.Body), "gpt-6-astra")
	require.EqualValues(t, 1, calls.Load())
}

type mixedExcelGrantRepo struct {
	*excelReauthTestRepo
	readyID int64
}

func (r *mixedExcelGrantRepo) GetExcelCredentials(ctx context.Context, id int64) (string, error) {
	if id != r.readyID {
		return "", nil
	}
	return r.excelReauthTestRepo.GetExcelCredentials(ctx, id)
}
func TestAdminExcelBulkSeparatesReadyAndPreparingAccounts(t *testing.T) {
	reauth, reader, loginRepo, _, _ := newExcelReauthTestService(t)
	a := *reader.account
	a.Extra = map[string]any{}
	b := a
	b.ID++
	storeExcelTestCredentials(t, reauth, loginRepo, excelTestCredentials("ready", time.Now().Add(time.Hour)))
	reauth.repo = &mixedExcelGrantRepo{excelReauthTestRepo: loginRepo, readyID: a.ID}
	repo := &readinessAccountRepo{adminExcelBPSGroupRepo: adminExcelBPSGroupRepo{accountRepoStubForBulkUpdate: accountRepoStubForBulkUpdate{
		getByIDsAccounts: []*Account{&a, &b}, getByIDAccounts: map[int64]*Account{a.ID: &a, b.ID: &b},
	}}}
	svc := &adminServiceImpl{accountRepo: repo, excelOAuthReauth: reauth}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{a.ID, b.ID}, Extra: map[string]any{"openai_excel_bps": true}})
	require.NoError(t, err)
	require.Equal(t, false, repo.preparation[a.ID])
	require.Equal(t, true, repo.preparation[b.ID])
	require.Equal(t, 1, repo.bulkUpdateCalls)
}
