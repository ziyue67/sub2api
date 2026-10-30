package service

import (
	"context"
	"database/sql"
	"os"
	"time"
)

// ImageHistoryBilling is a customer receipt, never an upstream cost estimate.
type ImageHistoryBilling struct {
	Status string   `json:"status"`
	Amount *float64 `json:"amount,omitempty"`
}

type imageHistoryBillingReader interface {
	ImageHistoryBilling(context.Context, int64, int64, string) (ImageHistoryBilling, error)
}

func (s *ImageArchiveService) ListHistory(ctx context.Context, userID, keyID int64, filter ImageArchiveFilter) ([]ImageArchiveRecord, int64, error) {
	if userID <= 0 || keyID < 0 {
		return nil, 0, sql.ErrNoRows
	}
	// Never accept a caller-provided tenant, account, or group predicate.
	filter.UserID, filter.APIKeyID = userID, keyID
	filter.AccountID, filter.GroupID = 0, 0
	return s.repo.List(ctx, filter)
}

func (s *ImageArchiveService) GetHistory(ctx context.Context, userID, keyID int64, id string) (*ImageArchiveRecord, error) {
	if userID <= 0 || keyID < 0 {
		return nil, sql.ErrNoRows
	}
	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if v == nil || v.UserID != userID || (keyID > 0 && v.APIKeyID != keyID) || !v.ExpiresAt.After(time.Now()) {
		return nil, sql.ErrNoRows
	}
	return v, nil
}

func (s *ImageArchiveService) OpenHistoryFile(ctx context.Context, userID, keyID int64, id string, fileID int, thumb bool) (*os.File, string, error) {
	v, err := s.GetHistory(ctx, userID, keyID, id)
	if err != nil {
		return nil, "", err
	}
	for _, f := range v.Files {
		if f.ID == fileID && f.Role == "result" {
			return s.OpenFile(ctx, id, fileID, thumb)
		}
	}
	return nil, "", os.ErrNotExist
}

func (s *ImageArchiveService) HistoryBilling(ctx context.Context, record *ImageArchiveRecord) ImageHistoryBilling {
	unknown := ImageHistoryBilling{Status: "unknown"}
	reader, ok := s.repo.(imageHistoryBillingReader)
	if !ok || record == nil {
		return unknown
	}
	// Only new captures have the canonical billing identity. An upstream request
	// ID or an old usage row alone cannot establish that money was deducted.
	id, _ := record.Parameters["_billing_request_id"].(string)
	if id == "" {
		return unknown
	}
	billing, err := reader.ImageHistoryBilling(ctx, record.UserID, record.APIKeyID, id)
	if err != nil {
		return unknown
	}
	return billing
}
