package migrations

import (
	"strings"
	"testing"
)

func TestPaymentOrderSubscriptionRenewalModeMigrationKeepsHistoricalExtendDefault(t *testing.T) {
	content, err := FS.ReadFile("239_payment_order_subscription_renewal_mode.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(content)
	for _, expected := range []string{
		"subscription_renewal_mode VARCHAR(16) NOT NULL DEFAULT 'extend'",
		"conrelid = 'payment_orders'::regclass",
		"CHECK (subscription_renewal_mode IN ('restart', 'extend'))",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("migration missing %q", expected)
		}
	}
}
