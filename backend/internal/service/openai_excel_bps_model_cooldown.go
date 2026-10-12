package service

import (
	"strings"
	"sync"
	"time"
)

const excelBPSModelPermissionCooldown = 30 * time.Minute
const excelBPSModelPermissionFilterReason = "excel_bps_model_not_allowed"
const excelBPSModelPermissionMaxEntries = 4096

type excelBPSModelKey struct {
	accountID int64
	model     string
}
type excelBPSModelCooldowns struct {
	mu    sync.Mutex
	until map[excelBPSModelKey]time.Time
}

func isExcelBPSModelPermissionError(status int, code string) bool {
	return status == 403 && (code == "basispoints_model_access_changed" || code == "model_not_allowed")
}

// Store the actual upstream model, so aliases share a cooldown while native
// Codex requests and the account's other BPS models remain eligible.
func (s *OpenAIGatewayService) coolDownExcelBPSModel(account *Account, upstreamModel string) {
	if s == nil || account == nil || strings.TrimSpace(upstreamModel) == "" {
		return
	}
	cache := &s.excelBPSModelCooldowns
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	if cache.until == nil {
		cache.until = make(map[excelBPSModelKey]time.Time)
	}
	for key, expiry := range cache.until {
		if !now.Before(expiry) {
			delete(cache.until, key)
		}
	}
	key := excelBPSModelKey{account.ID, upstreamModel}
	if _, exists := cache.until[key]; !exists && len(cache.until) >= excelBPSModelPermissionMaxEntries {
		var oldest excelBPSModelKey
		var expiry time.Time
		for candidate, until := range cache.until {
			if expiry.IsZero() || until.Before(expiry) {
				oldest, expiry = candidate, until
			}
		}
		delete(cache.until, oldest)
	}
	cache.until[key] = now.Add(excelBPSModelPermissionCooldown)
}

func (s *OpenAIGatewayService) isExcelBPSModelCoolingDown(account *Account, requestedModel string) bool {
	if s == nil || account == nil || !account.IsExcelBPSEnabledForModel(requestedModel) {
		return false
	}
	key := excelBPSModelKey{account.ID, account.GetMappedModel(requestedModel)}
	cache := &s.excelBPSModelCooldowns
	cache.mu.Lock()
	defer cache.mu.Unlock()
	until, exists := cache.until[key]
	if exists && time.Now().Before(until) {
		return true
	}
	if exists {
		delete(cache.until, key)
	}
	return false
}
