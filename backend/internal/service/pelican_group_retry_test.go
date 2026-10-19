package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanGroupRetriesPartialTransientFailures(t *testing.T) {
	for _, message := range []string{
		"The service is temporarily unavailable. Please retry later.",
		"Service temporarily unavailable",
		"Stream read error: unexpected EOF",
		"Stream ended before response.completed",
		"Generation stream ended before completion",
		"Chat Completions stream from /v1/chat/completions ended before [DONE]",
		"Chat Completions stream read error from /v1/chat/completions: connection reset by peer",
	} {
		t.Run(message, func(t *testing.T) {
			repo := newGroupTestRepoFake()
			router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
			svc, showcase := newGroupTestService(repo, router, answers(map[int64]ScheduledTestResult{
				11: {Status: "failed", ResponseText: "<html>incomplete", ErrorMessage: message},
				12: {Status: "success", ResponseText: "<svg>complete</svg>"},
			}))
			svc.execute(groupTestPlan(1), groupTestNow)
			require.Len(t, repo.results, 1, "one result is persisted for the scheduled sample")
			result := repo.results[0]
			require.Equal(t, "success", result.Status)
			require.EqualValues(t, 12, result.AccountID)
			require.Equal(t, "<svg>complete</svg>", result.ResponseText)
			require.Equal(t, []PelicanGroupTestAttempt{{AccountID: 11, AccountName: "a", Error: message}}, result.Attempts)
			require.Len(t, showcase.published, 1, "the successful replacement reaches the showcase")
			require.ElementsMatch(t, []int64{11, 12}, router.released)
			require.ElementsMatch(t, []int64{11}, router.excluded[1])
		})
	}
}

func TestPelicanGroupPartialRetryExhaustionKeepsFailure(t *testing.T) {
	for _, available := range []int{1, 3, 4} {
		t.Run(strconv.Itoa(available)+" accounts available", func(t *testing.T) {
			repo := newGroupTestRepoFake()
			router := &groupTestRouterFake{}
			for id := 1; id <= available; id++ {
				router.steps = append(router.steps, routeStep{account: account(int64(id), "pool")})
			}
			clock := groupTestNow
			calls := 0
			svc, showcase := newGroupTestService(repo, router, func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
				calls++
				clock = clock.Add(20 * time.Second)
				return &ScheduledTestResult{Status: "failed", ResponseText: "<html>partial", ErrorMessage: "Stream ended before response.completed", LatencyMs: 20000}, nil
			})
			svc.now = func() time.Time { return clock }
			svc.execute(groupTestPlan(1), groupTestNow)
			require.Equal(t, min(available, 3), calls)
			require.Len(t, repo.results, 1, "exhaustion still records a failure for this interval")
			result := repo.results[0]
			require.Equal(t, "failed", result.Status)
			require.EqualValues(t, calls, result.AccountID)
			require.Equal(t, "<html>partial", result.ResponseText)
			require.Equal(t, "Stream ended before response.completed", result.ErrorMessage)
			require.Len(t, result.Attempts, calls-1)
			require.EqualValues(t, calls*20000, result.LatencyMs, "elapsed time includes all attempts")
			require.Len(t, router.released, calls)
			require.Empty(t, showcase.published)
		})
	}
}

func TestPelicanGroupKeepsModelFailuresAfterPartialOutput(t *testing.T) {
	for _, message := range []string{
		pelicanErrEmptyOutput, pelicanErrCaptureLimit, pelicanErrHistoryLimit,
		pelicanErrMaxTokens, pelicanErrRefused + ": policy",
		"Model did not return HTML or SVG", "Unknown error",
	} {
		t.Run(message, func(t *testing.T) {
			router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
			svc, _ := newGroupTestService(newGroupTestRepoFake(), router, answers(map[int64]ScheduledTestResult{
				11: {Status: "failed", ResponseText: "partial answer", ErrorMessage: message},
			}))
			result := svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4})
			require.EqualValues(t, 11, result.AccountID)
			require.Equal(t, message, result.ErrorMessage)
			require.Equal(t, "partial answer", result.ResponseText)
			require.Empty(t, result.Attempts)
			require.Len(t, router.excluded, 1)
		})
	}
}

func TestPelicanGroupCancellationStopsPartialRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
	svc, _ := newGroupTestService(newGroupTestRepoFake(), router, func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
		cancel()
		return &ScheduledTestResult{Status: "failed", ResponseText: "partial", ErrorMessage: "Stream read error: context canceled"}, nil
	})
	result := svc.runSample(ctx, groupTestPlan(1), &Group{ID: 4})
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "partial", result.ResponseText)
	require.Empty(t, result.Attempts)
	require.Len(t, router.excluded, 1)
	require.Equal(t, []int64{11}, router.released)
}
