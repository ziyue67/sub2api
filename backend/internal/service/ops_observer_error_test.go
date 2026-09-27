package service

import (
	"context"
	"encoding/json"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestObserverErrorsScopeAndWhitelist(t *testing.T) {
	owner, other := int64(42), int64(99)
	repo := &stubOpsRepoForUserErr{detailToReturn: &OpsErrorLogDetail{
		OpsErrorLog:  OpsErrorLog{ID: 1, UserID: &owner, AccountName: "own-account", UpstreamModel: "mapped", ResolvedByUserName: "secret-operator"},
		APIKeyPrefix: "secret-key-prefix", UpstreamErrors: "secret-events", UpstreamErrorDetail: "secret-detail", ErrorBody: "own-error",
	}}
	svc := &OpsService{opsRepo: repo}
	_, err := svc.ListObserverErrorRequests(context.Background(), owner, &OpsErrorLogFilter{UserID: &other, UserQuery: "other", Phase: "upstream", IncludeRecoveredUpstream: true, AccountID: &other, GroupID: &other})
	require.NoError(t, err)
	require.Equal(t, owner, *repo.gotFilter.UserID)
	require.Equal(t, "upstream", repo.gotFilter.Phase)
	require.Equal(t, other, *repo.gotFilter.AccountID)
	require.Equal(t, other, *repo.gotFilter.GroupID)
	require.False(t, repo.gotFilter.IncludeRecoveredUpstream)
	require.True(t, repo.gotFilter.ExcludeCountTokens)
	require.Empty(t, repo.gotFilter.UserQuery)
	require.False(t, repo.gotFilter.ModelFuzzy)
	detail, err := svc.GetObserverErrorRequestDetail(context.Background(), owner, 1)
	require.NoError(t, err)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "own-account")
	require.Contains(t, string(encoded), "own-error")
	require.NotContains(t, string(encoded), "secret-")
	require.NotContains(t, string(encoded), "api_key_prefix")
	require.NotContains(t, string(encoded), "upstream_errors")
	_, err = svc.GetObserverErrorRequestDetail(context.Background(), other, 1)
	require.True(t, infraerrors.IsNotFound(err))
	repo.detailToReturn.UserID = nil
	_, err = svc.GetObserverErrorRequestDetail(context.Background(), owner, 1)
	require.True(t, infraerrors.IsNotFound(err))
}
