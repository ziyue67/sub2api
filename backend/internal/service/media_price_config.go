package service

func imagePriceConfigFromAPIKey(apiKey *APIKey) *ImagePriceConfig {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return &ImagePriceConfig{
		Price1K: apiKey.Group.ImagePrice1K,
		Price2K: apiKey.Group.ImagePrice2K,
		Price4K: apiKey.Group.ImagePrice4K,
	}
}

func apiKeyHasConfiguredImagePrice(apiKey *APIKey, imageSize string) bool {
	return apiKey != nil && apiKey.Group != nil && apiKey.Group.GetImagePrice(imageSize) != nil
}

func videoPriceConfigFromAPIKey(apiKey *APIKey) *VideoPriceConfig {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return &VideoPriceConfig{
		Price480P:   apiKey.Group.VideoPrice480P,
		Price720P:   apiKey.Group.VideoPrice720P,
		Price1080P:  apiKey.Group.VideoPrice1080P,
		ModelPrices: apiKey.Group.VideoModelPrices,
	}
}

func apiKeyHasConfiguredVideoPrice(apiKey *APIKey, model, resolution string) bool {
	return apiKey != nil && apiKey.Group != nil && apiKey.Group.GetVideoPriceForModel(model, resolution) != nil
}

func webSearchPricePerCallFromAPIKey(apiKey *APIKey) *float64 {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return apiKey.Group.WebSearchPricePerCall
}

// buildWebSearchToolSurcharge keeps the persisted detail and the charged
// amount on the same pricing path for OpenAI Responses and Anthropic Messages.
func buildWebSearchToolSurcharge(billingService *BillingService, callCount int, apiKey *APIKey, multiplier float64) (ToolSurcharge, bool) {
	if billingService == nil || callCount <= 0 {
		return ToolSurcharge{}, false
	}
	pricePerCall := defaultWebSearchPricePerCall
	configured := webSearchPricePerCallFromAPIKey(apiKey)
	if configured != nil && *configured >= 0 {
		pricePerCall = *configured
	}
	cost := billingService.CalculateWebSearchCost(callCount, configured, multiplier)
	if cost == nil || (cost.TotalCost == 0 && cost.ActualCost == 0) {
		return ToolSurcharge{}, false
	}
	return ToolSurcharge{
		Name:           "web_search",
		Count:          callCount,
		Price:          pricePerCall * 1000,
		RateMultiplier: multiplier,
		Cost:           cost.ActualCost,
	}, true
}

func groupSearchPricePer1kFromAPIKey(apiKey *APIKey) *float64 {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return apiKey.Group.GetSearchPricePer1k()
}

func groupAudioPriceConfigFromAPIKey(apiKey *APIKey) *audioPriceConfig {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	g := apiKey.Group
	return &audioPriceConfig{
		RealtimePerMin: g.AudioRealtimePricePerMin,
		TTSPerMChars:   g.AudioTTSPricePerMillionChars,
		STTPerHour:     g.AudioSTTPricePerHour,
	}
}
