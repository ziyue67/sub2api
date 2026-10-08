//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountOpsDeliveryNamesRoundTripAndExplicitReset(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"balance_low", "balance_threshold"} {
		t.Run(kind, func(t *testing.T) {
			var id int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('channel-name-fixture','openai','apikey','active',true) RETURNING id`).Scan(&id))
			defer cleanupOnce(t, id)
			repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
			event := service.AccountOpsEvent{AccountID: id, Kind: kind, Criteria: "name-rule"}
			if kind == "balance_threshold" {
				require.NoError(t, repo.ObserveThreshold(ctx, event, "active", time.Now(), true, true))
			} else {
				require.NoError(t, repo.Record(ctx, event))
			}
			claimed, err := repo.Claim(ctx)
			require.NoError(t, err)
			require.NotNil(t, claimed)
			var delivery service.AccountOpsDelivery
			require.NoError(t, json.Unmarshal([]byte(`{"provider":"email","name":"原值班邮箱","status":"failed","attempts":1}`), &delivery))
			owned, err := repo.SaveDelivery(ctx, claimed, "email:stable-identity", delivery)
			require.NoError(t, err)
			require.True(t, owned)
			require.NoError(t, repo.Complete(ctx, claimed, "failed", 0))
			retry, err := repo.Claim(ctx)
			require.NoError(t, err)
			require.NotNil(t, retry)
			raw, err := json.Marshal(retry.Deliveries["email:stable-identity"])
			require.NoError(t, err)
			require.Contains(t, string(raw), `"name":"原值班邮箱"`)
			require.NoError(t, json.Unmarshal([]byte(`{"provider":"email","name":"","status":"sent","attempts":2}`), &delivery))
			owned, err = repo.SaveDelivery(ctx, retry, "email:stable-identity", delivery)
			require.NoError(t, err)
			require.True(t, owned)
			require.NoError(t, repo.Complete(ctx, retry, "sent", time.Hour))
			items, err := repo.List(ctx, 0, 100)
			require.NoError(t, err)
			found := false
			for _, item := range items {
				if item.AccountID != id {
					continue
				}
				found = true
				raw, err := json.Marshal(item.Deliveries["email:stable-identity"])
				require.NoError(t, err)
				require.Contains(t, string(raw), `"provider":"email"`)
				require.Contains(t, string(raw), `"name":""`)
				require.NotContains(t, string(raw), "原值班邮箱")
			}
			require.True(t, found)
		})
	}
}
