package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
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

type imageHistoryLegacyBillingReader interface {
	ImageHistoryLegacyBilling(context.Context, int64, int64, int64, string) (ImageHistoryBilling, error)
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
	if record == nil || record.UserID <= 0 || record.APIKeyID <= 0 {
		return unknown
	}
	if id, _ := record.Parameters["_billing_request_id"].(string); id != "" {
		reader, ok := s.repo.(imageHistoryBillingReader)
		if !ok {
			return unknown
		}
		billing, err := reader.ImageHistoryBilling(ctx, record.UserID, record.APIKeyID, id)
		if err != nil {
			return unknown
		}
		return billing
	}
	// Forwarding failed before this attempt could submit usage. A later successful
	// retry has its own archive and must not donate its receipt to this attempt.
	if imageHistoryAttemptNotBilled(record) {
		zero := float64(0)
		return ImageHistoryBilling{Status: "no_charge", Amount: &zero}
	}
	// Old captures may have an upstream ID but no canonical billing ID. Only a
	// unique, same-owner/key/account usage link AND its durable receipt suffice.
	if record.RequestID != "" && record.AccountID > 0 {
		if reader, ok := s.repo.(imageHistoryLegacyBillingReader); ok {
			billing, err := reader.ImageHistoryLegacyBilling(ctx, record.UserID, record.APIKeyID, record.AccountID, record.RequestID)
			if err == nil {
				return billing
			}
		}
	}
	return unknown
}

func imageHistoryAttemptNotBilled(record *ImageArchiveRecord) bool {
	// PR #138 persisted all these fields before adding the explicit outcome.
	// Preserve that evidence-based compatibility path; older bare storage/error
	// records and absence of a billing row are never sufficient.
	if outcome, exists := record.Parameters["_billing_outcome"]; exists && outcome != "not_submitted_v1" {
		return false
	}
	if record.Parameters["_generation_status"] != "failed" {
		return false
	}
	failed := false
	for _, code := range strings.Split(record.ErrorCodes, ",") {
		if code == "forward_failed" {
			failed = true
		}
	}
	if !failed {
		return false
	}
	for _, file := range record.Files {
		if file.Role == "result" {
			return false
		}
	}
	switch count := record.Parameters["_image_count"].(type) {
	case int:
		return count == 0
	case int64:
		return count == 0
	case float64:
		return count == 0
	case json.Number:
		n, err := count.Float64()
		return err == nil && n == 0
	default:
		return false
	}
}
