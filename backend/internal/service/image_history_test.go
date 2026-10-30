package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type historyTestRepository struct {
	*archiveTestRepository
	filter       ImageArchiveFilter
	billing      ImageHistoryBilling
	billingErr   error
	billingCalls int
}

func (r *historyTestRepository) List(_ context.Context, f ImageArchiveFilter) ([]ImageArchiveRecord, int64, error) {
	r.filter = f
	return nil, 0, nil
}
func (r *historyTestRepository) ImageHistoryBilling(_ context.Context, user, key int64, request string) (ImageHistoryBilling, error) {
	r.billingCalls++
	return r.billing, r.billingErr
}

func TestImageHistoryOwnershipExpiryAndFiles(t *testing.T) {
	s, repo := archiveTestService(t)
	id := uuid.NewString()
	record := &ImageArchiveRecord{ID: id, UserID: 12, APIKeyID: 34, ExpiresAt: time.Now().Add(time.Hour), Files: []ImageArchiveFile{
		{ID: 1, Role: "reference", MIME: "image/png"}, {ID: 2, Role: "result", MIME: "image/png"}, {ID: 3, Role: "mask", MIME: "image/png"},
	}}
	repo.records[id] = record
	require.NoError(t, os.MkdirAll(filepath.Join(s.root, id), 0700))
	for _, file := range []string{"1", "2", "3"} {
		require.NoError(t, os.WriteFile(filepath.Join(s.root, id, file), []byte("image"), 0600))
	}
	for _, pair := range [][2]int64{{99, 0}, {99, 34}, {12, 35}, {0, 34}, {12, -1}} {
		_, err := s.GetHistory(context.Background(), pair[0], pair[1], id)
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, _, err = s.OpenHistoryFile(context.Background(), pair[0], pair[1], id, 2, false)
		require.ErrorIs(t, err, sql.ErrNoRows)
	}
	for _, key := range []int64{0, 34} {
		got, err := s.GetHistory(context.Background(), 12, key, id)
		require.NoError(t, err)
		require.Equal(t, id, got.ID)
		f, _, err := s.OpenHistoryFile(context.Background(), 12, key, id, 2, false)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		for _, file := range []int{1, 3, 99} {
			_, _, err = s.OpenHistoryFile(context.Background(), 12, key, id, file, false)
			require.ErrorIs(t, err, os.ErrNotExist)
		}
	}
	_, err := s.GetHistory(context.Background(), 12, 0, "../../secret")
	require.ErrorIs(t, err, sql.ErrNoRows)
	record.ExpiresAt = time.Now().Add(-time.Second)
	_, err = s.GetHistory(context.Background(), 12, 0, id)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestImageHistoryListForcesTenantAndKey(t *testing.T) {
	s, base := archiveTestService(t)
	repo := &historyTestRepository{archiveTestRepository: base}
	s.repo = repo
	for _, key := range []int64{0, 34} {
		_, _, err := s.ListHistory(context.Background(), 12, key, ImageArchiveFilter{UserID: 99, APIKeyID: 66, AccountID: 7, GroupID: 8, Page: 2, PageSize: 20})
		require.NoError(t, err)
		require.Equal(t, int64(12), repo.filter.UserID)
		require.Equal(t, key, repo.filter.APIKeyID)
		require.Zero(t, repo.filter.AccountID)
		require.Zero(t, repo.filter.GroupID)
		require.Equal(t, 2, repo.filter.Page)
	}
	_, _, err := s.ListHistory(context.Background(), 0, 0, ImageArchiveFilter{})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestImageHistoryFailureAndInterruptedCapture(t *testing.T) {
	for _, withImage := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "interrupted_result"}[withImage], func(t *testing.T) {
			s, repo := archiveTestService(t)
			c, _ := archiveTestContext()
			c.Header("X-Client-Request-ID", "client-visible")
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "client-visible"))
			capture := archiveTestCapture(s, c)
			var result *OpenAIForwardResult
			if withImage {
				archiveObserveImage(t, c)
				result = &OpenAIForwardResult{ImageCount: 1, RequestID: "private-upstream-id"}
			} else {
				// Even when the image directory cannot be used, failure metadata survives.
				s.root = filepath.Join(t.TempDir(), "file")
				require.NoError(t, os.WriteFile(s.root, []byte("not a directory"), 0600))
			}
			capture.FinishWithError(c, result, errors.New("secret upstream credential error"))
			s.process(<-s.queue)
			got := repo.records[capture.record.ID]
			require.NotNil(t, got)
			require.Equal(t, "client-visible", got.Parameters["_client_request_id"])
			require.NotContains(t, got.ErrorCodes, "secret")
			if withImage {
				require.Equal(t, "interrupted", got.Parameters["_delivery_status"])
				require.Equal(t, "succeeded", got.Parameters["_generation_status"])
				require.Equal(t, "client:client-visible", got.Parameters["_billing_request_id"])
				require.NotEmpty(t, got.Files)
			} else {
				require.Equal(t, "unknown", got.Parameters["_delivery_status"])
				require.Equal(t, "failed", got.Parameters["_generation_status"])
				require.NotContains(t, got.Parameters, "_billing_request_id")
			}
		})
	}
}

func TestImageHistoryBillingUnknownWithoutDurableReceipt(t *testing.T) {
	s, base := archiveTestService(t)
	record := &ImageArchiveRecord{UserID: 12, APIKeyID: 34, RequestID: "upstream-is-not-canonical", Parameters: map[string]any{}}
	require.Equal(t, "unknown", s.HistoryBilling(context.Background(), record).Status)
	amount := 0.1
	repo := &historyTestRepository{archiveTestRepository: base, billing: ImageHistoryBilling{Status: "charged", Amount: &amount}}
	s.repo = repo
	require.Equal(t, "unknown", s.HistoryBilling(context.Background(), record).Status)
	require.Zero(t, repo.billingCalls)
	record.Parameters["_billing_request_id"] = "client:canonical"
	require.Equal(t, "charged", s.HistoryBilling(context.Background(), record).Status)
	repo.billingErr = sql.ErrNoRows
	require.Equal(t, "unknown", s.HistoryBilling(context.Background(), record).Status)
}
