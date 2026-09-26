//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodex780RejectedModelDoesNotExhaustOtherModelsBudget(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		for _, rejection := range []string{"http400", "stream404", "stream401"} {
			t.Run(fmt.Sprintf("parallel=%t/%s", parallel, rejection), func(t *testing.T) {
				resetCodexHarvestFlow()
				t.Cleanup(resetCodexHarvestFlow)
				account := ticketTestAccount(41)
				var astra, sol atomic.Int64
				svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 780, TTLSeconds: 240}, &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
					var payload struct {
						Model string `json:"model"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						return nil, err
					}
					h := http.Header{}
					status := 200
					body := ""
					if payload.Model == "gpt-6-astra" {
						astra.Add(1)
						switch rejection {
						case "http400":
							status = 400
						case "stream404":
							body = "data: {\"type\":\"error\",\"error\":{\"code\":\"model_not_found\"}}\n\n"
						case "stream401":
							body = "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_api_key\"}}\n\n"
						}
					} else {
						sol.Add(1)
						h["Set-Cookie"] = mint780Pair(time.Now().Add(time.Hour), "unified-88")
						h.Set(openAICodexTurnStateHeader, mint780State(time.Now()))
						body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"model\":\"gpt-6-sol\"}}\n\n"
					}
					return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
				}})
				svc.accountRepo = &manualHarvestAccountRepo{account: account, persist: func(context.Context) error { return nil }}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req := ManualHarvestRequest{AccountID: 41, Models: []string{"gpt-6-astra", "gpt-6-sol"}, MaxAttempts: 8, CollectLanes: 2, ProbeIntervalSeconds: 1}
				var final ManualHarvestProgress
				emit := func(p ManualHarvestProgress) { final = p }
				var err error
				if parallel {
					err = svc.runParallelHarvest(ctx, req, account, emit, &fakeHarvestCollection{})
				} else {
					req.CollectLanes = 1
					err = svc.ExecuteManualHarvest(ctx, req, emit)
				}
				require.NoError(t, err)
				require.True(t, final.Done)
				require.Greater(t, astra.Load(), int64(0))
				require.LessOrEqual(t, astra.Load(), int64(req.CollectLanes))
				if rejection == "stream401" {
					require.Zero(t, sol.Load())
					require.Zero(t, final.TicketsStored)
				} else {
					require.Positive(t, sol.Load())
					require.Equal(t, 1, final.TicketsStored)
				}
			})
		}
	}
}
