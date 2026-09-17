package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"golang.org/x/sync/errgroup"
)

const (
	// UpstreamUsageProbeExtraKey is deliberately separate from the billing-rate
	// snapshot. A balance observation must never affect rate synchronisation.
	UpstreamUsageProbeExtraKey     = "upstream_usage_probe"
	upstreamUsageProbeMaxBodyBytes = 64 * 1024
)

const (
	UpstreamUsageProbeStatusOK          = "ok"
	UpstreamUsageProbeStatusUnsupported = "unsupported"
	UpstreamUsageProbeStatusFailed      = "failed"
)

var ErrUpstreamUsageProbeUnavailable = errors.New("upstream usage probe is unavailable")

// UpstreamUsageProbeSnapshot contains only the small, sanitized amount fields
// needed by the admin UI. Raw responses, credentials and usage-detail arrays
// are intentionally never persisted.
type UpstreamUsageProbeSnapshot struct {
	Status        string         `json:"status"`
	Data          map[string]any `json:"data,omitempty"`
	FetchedAt     *time.Time     `json:"fetched_at,omitempty"`
	FreshUntil    *time.Time     `json:"fresh_until,omitempty"`
	LastAttemptAt time.Time      `json:"last_attempt_at"`
	NextProbeAt   time.Time      `json:"next_probe_at"`
	FailureCount  int            `json:"failure_count,omitempty"`
	HTTPStatus    int            `json:"http_status,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
}

type UpstreamUsageProbeResult struct {
	AccountID int64                       `json:"account_id"`
	Snapshot  *UpstreamUsageProbeSnapshot `json:"snapshot,omitempty"`
	Error     string                      `json:"error,omitempty"`
}

type UpstreamUsageSnapshotItem struct {
	AccountID int64                       `json:"account_id"`
	Snapshot  *UpstreamUsageProbeSnapshot `json:"snapshot"`
}

func BuildUpstreamUsageSnapshotItems(accounts []Account) []UpstreamUsageSnapshotItem {
	items := make([]UpstreamUsageSnapshotItem, 0, len(accounts))
	for _, account := range accounts {
		var snapshot *UpstreamUsageProbeSnapshot
		if account.Type == AccountTypeAPIKey {
			snapshot = decodeUpstreamUsageProbeSnapshot(account.Extra)
		}
		items = append(items, UpstreamUsageSnapshotItem{AccountID: account.ID, Snapshot: snapshot})
	}
	return items
}

type upstreamUsageSnapshotWriter interface {
	UpdateUpstreamUsageProbeSnapshot(context.Context, *Account, *UpstreamUsageProbeSnapshot) error
}

// ProbeUpstreamUsage performs exactly one GET /v1/usage request. It is manual
// only: unlike the billing-rate probe it has no periodic runner or auto switch.
func (s *UpstreamBillingProbeService) ProbeUpstreamUsage(ctx context.Context, accountID int64) (*UpstreamUsageProbeSnapshot, error) {
	if s == nil || s.accountRepo == nil || s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return nil, ErrUpstreamUsageProbeUnavailable
	}
	settings, err := s.getSettings(ctx)
	if err != nil {
		return nil, err
	}
	key := "usage:" + strconv.FormatInt(accountID, 10)
	value, err, _ := s.probeGroup.Do(key, func() (any, error) {
		select {
		case s.probeSlots <- struct{}{}:
			defer func() { <-s.probeSlots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		account, loadErr := s.accountRepo.GetByID(ctx, accountID)
		if loadErr != nil {
			return nil, loadErr
		}
		if !isUpstreamBillingProbeAccount(account) {
			return nil, ErrUpstreamBillingProbeAccountInvalid
		}
		return s.probeLoadedUsageAccount(ctx, account, settings.IntervalMinutes)
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	snapshot, ok := value.(*UpstreamUsageProbeSnapshot)
	if !ok {
		return nil, fmt.Errorf("invalid upstream usage probe result")
	}
	return snapshot, nil
}

func (s *UpstreamBillingProbeService) ProbeUpstreamUsageBatch(ctx context.Context, accountIDs []int64) []UpstreamUsageProbeResult {
	if len(accountIDs) > UpstreamBillingProbeMaxBatchSize {
		accountIDs = accountIDs[:UpstreamBillingProbeMaxBatchSize]
	}
	results := make([]UpstreamUsageProbeResult, len(accountIDs))
	var group errgroup.Group
	for i, accountID := range accountIDs {
		i, accountID := i, accountID
		results[i].AccountID = accountID
		group.Go(func() error {
			snapshot, err := s.ProbeUpstreamUsage(ctx, accountID)
			if err != nil {
				results[i].Error = safeUsageProbeError(err)
				return nil
			}
			results[i].Snapshot = snapshot
			return nil
		})
	}
	_ = group.Wait()
	return results
}

func (s *UpstreamBillingProbeService) probeLoadedUsageAccount(ctx context.Context, account *Account, intervalMinutes int) (*UpstreamUsageProbeSnapshot, error) {
	now := s.currentTime().UTC()
	apiKey := account.GetCredential("api_key")
	if apiKey == "" {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, 0, "missing_api_key")
	}
	baseURL := account.GetCredential("base_url")
	if account.Platform == PlatformOpenAI && strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com"
	}
	normalizedBaseURL, err := s.accountTestService.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, 0, "invalid_base_url")
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, 0, "proxy_unavailable")
		}
		if account.Proxy.ID != *account.ProxyID {
			return nil, ErrUpstreamBillingProbeIdentityChanged
		}
		proxyURL = account.Proxy.URL()
	}
	probeURL := buildOpenAIEndpointURL(normalizedBaseURL, "/v1/usage")
	probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, bytes.NewReader(nil))
	if err != nil {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, 0, "request_build_failed")
	}
	profile := HTTPUpstreamProfileDefault
	if account.Platform == PlatformOpenAI {
		profile = HTTPUpstreamProfileOpenAI
	}
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(req.Context(), profile)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	account.ApplyHeaderOverrides(req.Header)
	var tlsProfile *tlsfingerprint.Profile
	if s.accountTestService.tlsFPProfileService != nil {
		tlsProfile = s.accountTestService.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, 0, "request_failed")
	}
	if resp == nil || resp.Body == nil {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, 0, "empty_response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamUsageProbeMaxBodyBytes+1))
	if readErr != nil {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "response_read_failed")
	}
	if len(body) > upstreamUsageProbeMaxBodyBytes {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "response_too_large")
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "unsupported")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "http_error")
	}
	data, err := parseUpstreamUsageProbeResponse(body)
	if err != nil {
		return s.persistUsageProbeFailure(ctx, account, intervalMinutes, now, resp.StatusCode, "invalid_response")
	}
	snapshot := &UpstreamUsageProbeSnapshot{
		Status: UpstreamUsageProbeStatusOK, Data: data, FetchedAt: probeTimePtr(now),
		FreshUntil:    probeTimePtr(now.Add(2 * time.Duration(intervalMinutes) * time.Minute)),
		LastAttemptAt: now, NextProbeAt: now.Add(nextProbeDelay(intervalMinutes, 0)), HTTPStatus: resp.StatusCode,
	}
	if err := s.updateUsageSnapshot(ctx, account, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *UpstreamBillingProbeService) persistUsageProbeFailure(ctx context.Context, account *Account, intervalMinutes int, now time.Time, statusCode int, reason string) (*UpstreamUsageProbeSnapshot, error) {
	previous := decodeUpstreamUsageProbeSnapshot(account.Extra)
	failureCount := 1
	if previous != nil {
		failureCount = previous.FailureCount + 1
	}
	status := UpstreamUsageProbeStatusFailed
	if reason == "unsupported" {
		status = UpstreamUsageProbeStatusUnsupported
	}
	snapshot := &UpstreamUsageProbeSnapshot{Status: status, LastAttemptAt: now, NextProbeAt: now.Add(nextProbeDelay(intervalMinutes, 0)), FailureCount: failureCount, HTTPStatus: statusCode, LastError: reason}
	if err := s.updateUsageSnapshot(ctx, account, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *UpstreamBillingProbeService) updateUsageSnapshot(ctx context.Context, account *Account, snapshot *UpstreamUsageProbeSnapshot) error {
	if writer, ok := s.accountRepo.(upstreamUsageSnapshotWriter); ok {
		return writer.UpdateUpstreamUsageProbeSnapshot(ctx, account, snapshot)
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{UpstreamUsageProbeExtraKey: snapshot})
}

func parseUpstreamUsageProbeResponse(body []byte) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	data := make(map[string]any)
	for key, limit := range map[string]int{"mode": 64, "planName": 128, "unit": 32} {
		if value, ok := boundedUsageString(raw[key], limit); ok {
			data[key] = value
		}
	}
	if value, ok := raw["isValid"].(bool); ok {
		data["isValid"] = value
	}
	if remaining, ok := usageNumber(raw["remaining"]); ok {
		data["remaining"] = remaining
		data["source_field"] = "remaining"
		data["amount_kind"] = "wallet"
	}
	if balance, ok := usageNumber(raw["balance"]); ok {
		data["balance"] = balance
		if _, exists := data["source_field"]; !exists {
			data["source_field"] = "balance"
			data["amount_kind"] = "wallet"
		}
	}
	if quota, ok := raw["quota"].(map[string]any); ok {
		cleanQuota := make(map[string]any)
		for _, key := range []string{"limit", "used", "remaining"} {
			if value, valid := usageNumber(quota[key]); valid {
				cleanQuota[key] = value
			}
		}
		if unit, valid := boundedUsageString(quota["unit"], 32); valid {
			cleanQuota["unit"] = unit
		}
		if len(cleanQuota) > 0 {
			data["quota"] = cleanQuota
			if value, valid := usageNumber(quota["remaining"]); valid {
				if _, alreadyFound := data["remaining"]; !alreadyFound {
					data["remaining"] = value
					data["source_field"] = "quota.remaining"
					data["amount_kind"] = "quota"
				}
			}
		}
	}
	if _, ok := data["remaining"]; !ok {
		if _, ok := data["balance"]; !ok {
			return nil, fmt.Errorf("usage response has no finite remaining amount")
		}
	}
	return data, nil
}

func boundedUsageString(value any, maxRunes int) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	runes := []rune(trimmed)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes), true
}

func usageNumber(value any) (float64, bool) {
	var number float64
	switch value := value.(type) {
	case float64:
		number = value
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0) && math.Abs(number) <= 1e15
}

func decodeUpstreamUsageProbeSnapshot(extra map[string]any) *UpstreamUsageProbeSnapshot {
	if extra == nil {
		return nil
	}
	raw, ok := extra[UpstreamUsageProbeExtraKey]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot UpstreamUsageProbeSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil
	}
	if snapshot.Status != UpstreamUsageProbeStatusOK && snapshot.Status != UpstreamUsageProbeStatusUnsupported && snapshot.Status != UpstreamUsageProbeStatusFailed {
		return nil
	}
	return &snapshot
}

func safeUsageProbeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrUpstreamUsageProbeUnavailable) {
		return ErrUpstreamUsageProbeUnavailable.Error()
	}
	if errors.Is(err, ErrUpstreamBillingProbeAccountInvalid) {
		return ErrUpstreamBillingProbeAccountInvalid.Error()
	}
	if errors.Is(err, ErrUpstreamBillingProbeIdentityChanged) {
		return ErrUpstreamBillingProbeIdentityChanged.Error()
	}
	return "probe_failed"
}
