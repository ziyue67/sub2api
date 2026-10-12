package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestResolveCodexAccountAvailability(t *testing.T) {
	now := time.Date(2026, 9, 20, 22, 50, 0, 0, time.Local)
	at := func(d time.Duration) *time.Time {
		v := now.Add(d)
		return &v
	}
	expired := now.Add(-time.Hour)

	cases := []struct {
		name      string
		account   Account
		wantKind  string
		wantRecov time.Duration
	}{
		{name: "available", account: Account{Status: StatusActive, Schedulable: true}, wantKind: CodexAccountAvailabilityAvailable},
		{name: "rate limited while schedulable", account: Account{Status: StatusActive, Schedulable: true, RateLimitResetAt: at(2 * time.Hour)}, wantKind: CodexAccountAvailabilityRateLimited, wantRecov: 2 * time.Hour},
		{name: "rate limit expired", account: Account{Status: StatusActive, Schedulable: true, RateLimitResetAt: at(-time.Minute)}, wantKind: CodexAccountAvailabilityAvailable},
		{name: "credential error", account: Account{Status: "error", Schedulable: false}, wantKind: CodexAccountAvailabilityError},
		{name: "error beats schedulable", account: Account{Status: "error", Schedulable: true}, wantKind: CodexAccountAvailabilityError},
		{name: "manually disabled", account: Account{Status: StatusActive, Schedulable: false}, wantKind: CodexAccountAvailabilityDisabled},
		{name: "expired auto pause", account: Account{Status: StatusActive, Schedulable: true, ExpiresAt: &expired, AutoPauseOnExpired: true}, wantKind: CodexAccountAvailabilityExpired},
		{name: "expired without auto pause", account: Account{Status: StatusActive, Schedulable: true, ExpiresAt: &expired, AutoPauseOnExpired: false}, wantKind: CodexAccountAvailabilityAvailable},
		{name: "overload", account: Account{Status: StatusActive, Schedulable: true, OverloadUntil: at(15 * time.Minute)}, wantKind: CodexAccountAvailabilityOverload, wantRecov: 15 * time.Minute},
		{name: "temp unschedulable", account: Account{Status: StatusActive, Schedulable: true, TempUnschedulableUntil: at(50 * time.Minute)}, wantKind: CodexAccountAvailabilityTempUnschedulable, wantRecov: 50 * time.Minute},
		{name: "latest window wins rate limit", account: Account{Status: StatusActive, Schedulable: true, TempUnschedulableUntil: at(50 * time.Minute), RateLimitResetAt: at(2 * time.Hour)}, wantKind: CodexAccountAvailabilityRateLimited, wantRecov: 2 * time.Hour},
		{name: "latest window wins overload", account: Account{Status: StatusActive, Schedulable: true, RateLimitResetAt: at(10 * time.Minute), OverloadUntil: at(4 * time.Hour)}, wantKind: CodexAccountAvailabilityOverload, wantRecov: 4 * time.Hour},
		{name: "nil account", wantKind: CodexAccountAvailabilityError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ptr *Account
			if tc.name != "nil account" {
				a := tc.account
				ptr = &a
			}
			kind, recoverAt := ResolveCodexAccountAvailability(ptr, now)
			if kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", kind, tc.wantKind)
			}
			if tc.wantRecov == 0 {
				if recoverAt != nil {
					t.Fatalf("recoverAt = %v, want nil", recoverAt)
				}
				return
			}
			if recoverAt == nil || !recoverAt.Equal(now.Add(tc.wantRecov)) {
				t.Fatalf("recoverAt = %v, want %v", recoverAt, now.Add(tc.wantRecov))
			}
		})
	}
}

func TestDescribeCodexProbeFailure(t *testing.T) {
	cases := []struct {
		raw        string
		status     int
		wantLevel  string
		wantSubstr string
	}{
		{"invalid probe event", 200, "WARN", "不是有效数据"},
		{"probe response failed", 200, "WARN", "上游返回失败"},
		{"unterminated probe event", 200, "WARN", "完成前被截断"},
		{`Post "https://chatgpt.com/...": context deadline exceeded`, 0, "WARN", "探针超过时间限制"},
		{"unexpected EOF", 0, "WARN", "连接被中断"},
		{"dial tcp: lookup x: no such host", 0, "WARN", "连不上"},
		{"", 401, "ERROR", "凭证已失效"},
		{"", 403, "WARN", "上游拒绝"},
		{"completely unknown", 200, "WARN", "未通过验收"},
	}
	for _, tc := range cases {
		message, level, detail := describeCodexProbeFailure(tc.raw, tc.status, "gpt-6-astra", "node-abc")
		if level != tc.wantLevel {
			t.Errorf("%q/%d level = %q, want %q", tc.raw, tc.status, level, tc.wantLevel)
		}
		if !strings.Contains(message, tc.wantSubstr) {
			t.Errorf("%q/%d message = %q, want substring %q", tc.raw, tc.status, message, tc.wantSubstr)
		}
		if tc.raw != "" && !strings.Contains(detail, tc.raw) {
			t.Errorf("%q/%d detail = %q, want original error", tc.raw, tc.status, detail)
		}
		if !strings.Contains(detail, "model=gpt-6-astra") || !strings.Contains(detail, "node=node-abc") {
			t.Errorf("%q/%d detail = %q, want model and node", tc.raw, tc.status, detail)
		}
	}
}

func TestManualHarvestShouldSwitch(t *testing.T) {
	cases := []struct {
		rule  string
		fails int
		kind  string
		want  bool
	}{
		{"never", 9, "invalid_state", false},
		{"every_request", 0, "success", true},
		{"rejected_only", 0, "invalid_state", true},
		{"rejected_only", 0, "probe_reissued", true},
		{"rejected_only", 0, "probe_route_changed", true},
		{"rejected_only", 0, "invalid_route", true},
		{"rejected_only", 2, "network_error", false},
		{"rejected_only", 2, "probe_inconclusive", false},
		{"rejected_or_2fail", 2, "network_error", true},
		{"rejected_or_2fail", 1, "network_error", false},
	}
	for _, tc := range cases {
		if got := manualHarvestShouldSwitch(tc.rule, tc.fails, tc.kind); got != tc.want {
			t.Errorf("%s fails=%d kind=%s -> %v, want %v", tc.rule, tc.fails, tc.kind, got, tc.want)
		}
	}
}

func TestNormalizeManualHarvestLegacyRules(t *testing.T) {
	for old, want := range map[string]string{"312_only": "rejected_only", "312_or_2fail": "rejected_or_2fail"} {
		got, err := NormalizeManualHarvestRequest(ManualHarvestRequest{NodeSwitchRule: old})
		if err != nil || got.NodeSwitchRule != want {
			t.Fatalf("legacy rule %s: %+v, %v", old, got, err)
		}
		if strings.Contains(manualHarvestSwitchLabel(got.NodeSwitchRule), "312") {
			t.Fatal("legacy rule leaked into the log")
		}
	}
}

func TestNormalizeManualHarvestRequest(t *testing.T) {
	got, err := NormalizeManualHarvestRequest(ManualHarvestRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeSwitchRule != ManualHarvestNodeSwitchRejectedOr2Fail || got.ProbeIntervalSeconds != 10 || got.MaxAttempts != 20 {
		t.Fatalf("defaults = %+v", got)
	}
	if _, err := NormalizeManualHarvestRequest(ManualHarvestRequest{NodeSwitchRule: "nope"}); err == nil {
		t.Fatal("expected invalid node_switch_rule")
	}
	if _, err := NormalizeManualHarvestRequest(ManualHarvestRequest{ProbeIntervalSeconds: 400}); err == nil {
		t.Fatal("expected probe interval error")
	}
}

func TestManualHarvestRunComplete(t *testing.T) {
	models := []string{"gpt-6-astra", "gpt-5.6-sol"}
	got := map[string]struct{}{}
	if manualHarvestRunComplete(false, models, got) || manualHarvestRunComplete(true, models, got) {
		t.Fatal("empty run must continue")
	}
	got["gpt-6-astra"] = struct{}{}
	if !manualHarvestRunComplete(true, models, got) {
		t.Fatal("stop_on_success should halt after first hit")
	}
	if manualHarvestRunComplete(false, models, got) {
		t.Fatal("both-model run must continue until sol hits")
	}
	if !manualHarvestModelDone(got, "gpt-6-astra") || manualHarvestModelDone(got, "gpt-5.6-sol") {
		t.Fatal("only astra should be marked done")
	}
	got["gpt-5.6-sol"] = struct{}{}
	if !manualHarvestRunComplete(false, models, got) {
		t.Fatal("both-model run should stop after every model hits")
	}
}

func TestManualHarvestLiveModels(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, TargetLength: 292, TTLSeconds: 3600,
		Models: []string{"gpt-6-astra", "gpt-5.6-sol"},
	}, nil)
	account := ticketTestAccount(2)
	models := []string{"gpt-6-astra", "gpt-5.6-sol"}
	if len(svc.manualHarvestLiveModels(account, models)) != 0 {
		t.Fatal("no tickets yet")
	}
	if err := svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: 2, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	got := svc.manualHarvestLiveModels(account, models)
	if !manualHarvestModelDone(got, "gpt-6-astra") || manualHarvestModelDone(got, "gpt-5.6-sol") {
		t.Fatalf("got=%v", got)
	}
	if !manualHarvestRunComplete(true, models, got) {
		t.Fatal("stop_on_success should halt on existing astra ticket")
	}
	if manualHarvestRunComplete(false, models, got) {
		t.Fatal("both-model run must still harvest sol")
	}
}

func TestCodexTicketChatHold(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292}, nil)
	account := ticketTestAccount(2)
	if svc.codexTicketChatHeld(2) {
		t.Fatal("idle")
	}
	first := svc.holdCodexTicketChat(account)
	second := svc.holdCodexTicketChat(account)
	if !svc.codexTicketChatHeld(2) {
		t.Fatal("held")
	}
	first()
	if !svc.codexTicketChatHeld(2) {
		t.Fatal("nested hold")
	}
	second()
	if svc.codexTicketChatHeld(2) {
		t.Fatal("released")
	}
}

func TestDescribeCodexHarvestOutcome(t *testing.T) {
	msg, level, detail := describeCodexHarvestOutcome("success", "", 200, "gpt-6-astra", "node-a", true)
	if level != "OK" || !strings.Contains(msg, "完整续接") || strings.Contains(detail, "len=") {
		t.Fatalf("success: level=%s msg=%s detail=%s", level, msg, detail)
	}
	msg, level, detail = describeCodexHarvestOutcome("invalid_state", "", 200, "gpt-6-astra", "node-a", false)
	if level != "WARN" || !strings.Contains(msg, "票据未通过校验") || strings.Contains(msg+detail, "降智") {
		t.Fatalf("rejected: level=%s msg=%s detail=%s", level, msg, detail)
	}
}
