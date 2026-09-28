package service

import (
	"context"
	"errors"
	"testing"
)

func TestAccountRPMProviderStrategies(t *testing.T) {
	for _, tt := range []struct {
		name     string
		platform string
		strategy string
		current  int
		want     WindowCostSchedulability
	}{
		{"anthropic below base", PlatformAnthropic, "tiered", 9, WindowCostSchedulable},
		{"anthropic sticky buffer", PlatformAnthropic, "tiered", 10, WindowCostStickyOnly},
		{"anthropic buffer exhausted", PlatformAnthropic, "tiered", 13, WindowCostNotSchedulable},
		{"anthropic sticky exemption", PlatformAnthropic, "sticky_exempt", 100, WindowCostStickyOnly},
		{"openai below limit", PlatformOpenAI, "tiered", 9, WindowCostSchedulable},
		{"openai strict at limit", PlatformOpenAI, "tiered", 10, WindowCostNotSchedulable},
		{"openai ignores sticky exemption", PlatformOpenAI, "sticky_exempt", 10, WindowCostNotSchedulable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Account{Platform: tt.platform, Type: AccountTypeOAuth, Extra: map[string]any{
				"base_rpm": 10, "rpm_strategy": tt.strategy, "rpm_sticky_buffer": 3,
			}}
			if got := a.CheckRPMSchedulability(tt.current); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccountRPMPrefetchScopesAndCacheFailures(t *testing.T) {
	accounts := []Account{
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 10}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 10}},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Extra: map[string]any{"base_rpm": 10}},
	}
	cache := &openAIRPMTestCache{counts: map[int64]int{1: 10, 2: 10}}
	anthropic := &GatewayService{rpmCache: cache}
	ctx := anthropic.withRPMPrefetch(context.Background(), accounts)
	if len(cache.ids) != 1 || cache.ids[0] != 1 {
		t.Fatalf("Anthropic prefetch scope changed: %v", cache.ids)
	}
	// Successful prefetch is shared by the same reader even if Redis later fails.
	cache.err = errors.New("redis unavailable")
	if anthropic.isAccountSchedulableForRPM(ctx, &accounts[0], false) {
		t.Fatal("Anthropic fresh sessions must still honor the prefetched base limit")
	}
	if !anthropic.isAccountSchedulableForRPM(ctx, &accounts[0], true) {
		t.Fatal("Anthropic sticky buffer must stay available")
	}
	if !anthropic.isAccountSchedulableForRPM(context.Background(), &accounts[0], false) {
		t.Fatal("Anthropic must retain its existing fail-open policy")
	}
	openai := &OpenAIGatewayService{rpmCache: cache}
	if allowed, _, err := openai.OpenAIRPMSchedulable(context.Background(), &accounts[1], false); allowed || !errors.Is(err, ErrOpenAIRPMUnavailable) {
		t.Fatalf("OpenAI strict policy must fail closed: %v", err)
	}
	cache.err = nil
	_, err := openai.withOpenAIRPMPrefetch(context.Background(), accounts)
	if err != nil || len(cache.ids) != 1 || cache.ids[0] != 2 {
		t.Fatalf("OpenAI prefetch must exclude setup tokens and Anthropic: ids=%v err=%v", cache.ids, err)
	}
}
