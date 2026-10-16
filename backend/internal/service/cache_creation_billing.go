package service

import (
	"context"
	"math"
	"time"
)

const CacheCreationBilling1hKey = "cache_creation_billing_1h_enabled"

// IsCacheCreationBilling1hEnabled is independent of request TTL injection and
// the legacy OAuth response-usage override. Missing or malformed flags are off.
func (a *Account) IsCacheCreationBilling1hEnabled() bool {
	if a == nil || a.Platform != PlatformAnthropic || a.Type != AccountTypeAPIKey {
		return false
	}
	enabled, _ := a.Extra[CacheCreationBilling1hKey].(bool)
	return enabled
}

// CacheCreationBillingAudit is persisted with the usage row and exposed only
// through the admin DTO. It describes billing, never physical cache lifetime.
type CacheCreationBillingAudit struct {
	Original5mPresent      bool    `json:"original_5m_present"`
	Original1hPresent      bool    `json:"original_1h_present"`
	Version                int     `json:"version"`
	Enabled                bool    `json:"enabled"`
	Status                 string  `json:"status"`
	OriginalCreationTokens int     `json:"original_creation_tokens"`
	Original5mTokens       int     `json:"original_5m_tokens"`
	Original1hTokens       int     `json:"original_1h_tokens"`
	OriginalReadTokens     int     `json:"original_read_tokens"`
	ConvertedTokens        int     `json:"converted_tokens"`
	OriginalCreationCost   float64 `json:"original_creation_cost"`
	BilledCreationCost     float64 `json:"billed_creation_cost"`
	ActualCostDelta        float64 `json:"actual_cost_delta"`
}

// prepareCacheCreationBilling operates on a copy: retries, quotes, SSE payloads
// and callers retain the original usage. Positive split counters are supplied
// only by upstream parsers on these API-key paths; an aggregate alone is unknown.
func prepareCacheCreationBilling(result *ForwardResult) (*ForwardResult, *CacheCreationBillingAudit) {
	copyResult := *result
	if !result.CacheCreationBilling1h {
		return &copyResult, nil
	}
	u := result.Usage
	audit := &CacheCreationBillingAudit{
		Version: 1, Enabled: true, Status: "unchanged",
		Original5mPresent:      u.CacheCreation5mPresent || u.CacheCreation5mTokens != 0,
		Original1hPresent:      u.CacheCreation1hPresent || u.CacheCreation1hTokens != 0,
		OriginalCreationTokens: u.CacheCreationInputTokens,
		Original5mTokens:       u.CacheCreation5mTokens, Original1hTokens: u.CacheCreation1hTokens,
		OriginalReadTokens: u.CacheReadInputTokens,
	}
	if u.CacheCreationInputTokens < 0 || u.CacheCreation5mTokens < 0 || u.CacheCreation1hTokens < 0 ||
		u.CacheCreation5mTokens > math.MaxInt-u.CacheCreation1hTokens {
		audit.Status = "invalid_usage"
		return &copyResult, audit
	}
	sum := u.CacheCreation5mTokens + u.CacheCreation1hTokens
	if sum != u.CacheCreationInputTokens {
		audit.Status = "inconsistent_usage"
		if sum == 0 && u.CacheCreationInputTokens > 0 {
			audit.Status = "missing_breakdown"
		}
		return &copyResult, audit
	}
	if u.CacheCreation5mTokens > 0 {
		audit.Status = "converted"
		audit.ConvertedTokens = u.CacheCreation5mTokens
		copyResult.Usage.CacheCreation5mTokens = 0
		copyResult.Usage.CacheCreation1hTokens = sum
	}
	return &copyResult, audit
}

// priceCacheCreationBilling uses the same resolved model, channel, long-context
// tier and request time for the before/after amounts. Unsupported/failed pricing
// must not result in a row claiming a successful 1h conversion.
func (s *GatewayService) priceCacheCreationBilling(ctx context.Context, result *ForwardResult, audit *CacheCreationBillingAudit, apiKey *APIKey, model string, multiplier, imageMultiplier float64, pricingAt time.Time, billed *CostBreakdown) *CostBreakdown {
	if audit == nil {
		return nil
	}
	original := *result
	original.Usage.CacheCreation5mTokens = audit.Original5mTokens
	original.Usage.CacheCreation1hTokens = audit.Original1hTokens
	originalCost := s.calculateRecordUsageCost(ctx, &original, apiKey, model, multiplier, imageMultiplier, pricingAt)
	resolver := s.resolver
	if resolver == nil {
		resolver = NewModelPricingResolver(nil, s.billingService)
	}
	resolved := resolver.Resolve(ctx, PricingInput{Model: model, GroupID: apiKey.GroupID, Group: apiKey.Group})
	if audit.Status == "converted" && (!resolved.SupportsCacheBreakdown || billed == nil || originalCost == nil || (billed.BillingMode != "" && billed.BillingMode != string(BillingModeToken)) || billed.CacheCreationCost <= 0 || math.IsNaN(billed.CacheCreationCost) || math.IsInf(billed.CacheCreationCost, 0)) {
		result.Usage = original.Usage
		audit.Status = "unavailable_pricing"
		audit.ConvertedTokens = 0
	}
	if originalCost != nil {
		audit.OriginalCreationCost = originalCost.CacheCreationCost
	}
	if billed != nil && audit.Status == "converted" {
		audit.BilledCreationCost = billed.CacheCreationCost
		audit.ActualCostDelta = billed.ActualCost - originalCost.ActualCost
	} else {
		audit.BilledCreationCost = audit.OriginalCreationCost
	}
	return originalCost
}
