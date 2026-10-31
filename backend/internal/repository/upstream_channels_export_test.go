//go:build integration

package repository

import (
	"context"
	"database/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"testing"
)

func UpstreamChannelsTestDatabase(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	return newImageUpgradeDatabase(t, context.Background())
}
