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
	filter         ImageArchiveFilter
	billing        ImageHistoryBilling
	billingErr     error
	billingCalls   int
	legacyBilling  ImageHistoryBilling
	legacyErr      error
	legacyCalls    int
	legacyIdentity []any
}

func (r *historyTestRepository) List(_ context.Context, f ImageArchiveFilter) ([]ImageArchiveRecord, int64, error) {
	r.filter = f
	return nil, 0, nil
}
func (r *historyTestRepository) ImageHistoryBilling(_ context.Context, user, key int64, request string) (ImageHistoryBilling, error) {
	r.billingCalls++
	return r.billing, r.billingErr
}

func (r *historyTestRepository) ImageHistoryLegacyBilling(_ context.Context, user, key, account int64, upstream string) (ImageHistoryBilling, error) {
	r.legacyCalls++
	r.legacyIdentity = []any{user, key, account, upstream}
	if r.legacyBilling.Status == "" {
		return ImageHistoryBilling{Status: "unknown"}, r.legacyErr
	}
	return r.legacyBilling, r.legacyErr
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
				require.Equal(t, "not_submitted_v1", got.Parameters["_billing_outcome"])
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

func TestImageHistoryFailedAttemptBillingProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ImageArchiveRecord)
		want   string
	}{
		{"legacy_failed_attempt", nil, "no_charge"},
		{"new_failed_attempt", func(r *ImageArchiveRecord) { r.Parameters["_billing_outcome"] = "not_submitted_v1" }, "no_charge"},
		{"in_memory_count", func(r *ImageArchiveRecord) { r.Parameters["_image_count"] = 0 }, "no_charge"},
		{"reference_only", func(r *ImageArchiveRecord) { r.Files = []ImageArchiveFile{{Role: "reference"}} }, "no_charge"},
		{"missing_failure_evidence", func(r *ImageArchiveRecord) { r.ErrorCodes = "missing_results" }, "unknown"},
		{"substring_is_not_failure", func(r *ImageArchiveRecord) { r.ErrorCodes = "not_forward_failed" }, "unknown"},
		{"missing_generation_evidence", func(r *ImageArchiveRecord) { delete(r.Parameters, "_generation_status") }, "unknown"},
		{"missing_count", func(r *ImageArchiveRecord) { delete(r.Parameters, "_image_count") }, "unknown"},
		{"nonzero_count", func(r *ImageArchiveRecord) { r.Parameters["_image_count"] = float64(1) }, "unknown"},
		{"invalid_count", func(r *ImageArchiveRecord) { r.Parameters["_image_count"] = "0" }, "unknown"},
		{"result_exists", func(r *ImageArchiveRecord) { r.Files = []ImageArchiveFile{{Role: "result"}} }, "unknown"},
		{"future_contract", func(r *ImageArchiveRecord) { r.Parameters["_billing_outcome"] = "future_version" }, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := archiveTestService(t)
			record := &ImageArchiveRecord{UserID: 12, APIKeyID: 34, ErrorCodes: "forward_failed,missing_results", Parameters: map[string]any{"_generation_status": "failed", "_image_count": float64(0)}}
			if tc.mutate != nil {
				tc.mutate(record)
			}
			got := s.HistoryBilling(context.Background(), record)
			require.Equal(t, tc.want, got.Status)
			if tc.want == "no_charge" {
				require.NotNil(t, got.Amount)
				require.Zero(t, *got.Amount)
			} else {
				require.Nil(t, got.Amount)
			}
		})
	}
}

func TestImageHistoryCanonicalReceiptWinsOverFailureMetadata(t *testing.T) {
	s, base := archiveTestService(t)
	amount := 0.125
	repo := &historyTestRepository{archiveTestRepository: base, billing: ImageHistoryBilling{Status: "charged", Amount: &amount}}
	s.repo = repo
	record := &ImageArchiveRecord{UserID: 12, APIKeyID: 34, ErrorCodes: "forward_failed", Parameters: map[string]any{"_generation_status": "failed", "_image_count": float64(0), "_billing_outcome": "not_submitted_v1", "_billing_request_id": "client:paid"}}
	require.Equal(t, "charged", s.HistoryBilling(context.Background(), record).Status)
	repo.billingErr = sql.ErrNoRows
	require.Equal(t, "unknown", s.HistoryBilling(context.Background(), record).Status)
}

func TestImageHistoryLegacyBillingUsesVerifiedIdentity(t *testing.T) {
	s, base := archiveTestService(t)
	amount := 0.25
	repo := &historyTestRepository{archiveTestRepository: base, legacyBilling: ImageHistoryBilling{Status: "charged", Amount: &amount}}
	s.repo = repo
	record := &ImageArchiveRecord{UserID: 12, APIKeyID: 34, AccountID: 56, RequestID: "upstream-request", Parameters: map[string]any{}}
	got := s.HistoryBilling(context.Background(), record)
	require.Equal(t, "charged", got.Status)
	require.Equal(t, []any{int64(12), int64(34), int64(56), "upstream-request"}, repo.legacyIdentity)
	require.Zero(t, repo.billingCalls)
	repo.legacyErr = errors.New("database unavailable")
	require.Equal(t, "unknown", s.HistoryBilling(context.Background(), record).Status)
	repo.legacyErr = nil
	record.Parameters["_billing_request_id"] = "client:canonical"
	repo.billingErr = sql.ErrNoRows
	calls := repo.legacyCalls
	require.Equal(t, "unknown", s.HistoryBilling(context.Background(), record).Status)
	require.Equal(t, calls, repo.legacyCalls, "a missing canonical receipt must not fall back to an unrelated legacy charge")
}
