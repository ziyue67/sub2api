//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Fork migration 134 already initialized these flags. Re-running the actual
// upgrade/startup paths must preserve later operator choices, including false.
func TestGeminiImageGenerationUpgradePreservesOperatorChoices(t *testing.T) {
	sharedBefore := imageUpgradeGroupChoices(t, integrationDB)
	t.Run("isolated_database", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		db, client := newImageUpgradeDatabase(t, ctx)
		for _, platform := range []string{service.PlatformGemini, service.PlatformAntigravity, service.PlatformComposite} {
			for _, enabled := range []bool{false, true} {
				// Seed every operator choice before restarting: a blanket update
				// must not silently reopen an earlier fixture on a later iteration.
				_, err := client.Group.Create().
					SetName(fmt.Sprintf("image-upgrade-%s-%t", platform, enabled)).
					SetPlatform(platform).
					SetStatus(service.StatusActive).
					SetSubscriptionType(service.SubscriptionTypeStandard).
					SetRateMultiplier(1).
					SetAllowImageGeneration(enabled).
					Save(ctx)
				require.NoError(t, err)
			}
		}
		for range 2 {
			before := imageUpgradeGroupChoices(t, db)
			require.NoError(t, ApplyMigrations(ctx, db))
			require.NoError(t, ensureSimpleModeDefaultGroups(ctx, client))
			after := imageUpgradeGroupChoices(t, db)
			for id, choice := range before {
				require.Contains(t, after, id)
				require.Equal(t, choice, after[id], "upgrade/startup must preserve every existing group's image choice")
			}
		}
	})
	// Child cleanup has closed the pool and dropped the entire fixture database,
	// including defaults created by startup. The suite's database stays intact.
	require.Equal(t, sharedBefore, imageUpgradeGroupChoices(t, integrationDB), "upgrade fixture must not change or leak groups into the shared database")
}

func newImageUpgradeDatabase(t *testing.T, ctx context.Context) (*sql.DB, *dbent.Client) {
	t.Helper()
	dsn, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	require.Equal(t, "postgres", dsn.Scheme)
	databaseName := "image_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedName := pq.QuoteIdentifier(databaseName)
	_, err = integrationDB.ExecContext(ctx, "CREATE DATABASE "+quotedName)
	require.NoError(t, err)
	// Registered before the pool cleanup so LIFO closes all connections first.
	// This also runs after Fatal; no broad DELETE or shared schema reset is used.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(cleanupCtx, "DROP DATABASE "+quotedName)
		require.NoError(t, err, "drop isolated image-upgrade fixture database")
	})
	dsn.Path = "/" + databaseName
	db, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var connectedDatabase string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&connectedDatabase))
	require.Equal(t, databaseName, connectedDatabase, "migration/startup must target the isolated database")
	require.NoError(t, ApplyMigrations(ctx, db))
	// Closing db above also closes the Ent driver's underlying pool.
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return db, client
}

type imageUpgradeGroupChoice struct {
	Name    string
	Enabled bool
}

func imageUpgradeGroupChoices(t *testing.T, db *sql.DB) map[int64]imageUpgradeGroupChoice {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, "SELECT id, name, allow_image_generation FROM groups")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	choices := make(map[int64]imageUpgradeGroupChoice)
	for rows.Next() {
		var id int64
		var choice imageUpgradeGroupChoice
		require.NoError(t, rows.Scan(&id, &choice.Name, &choice.Enabled))
		choices[id] = choice
	}
	require.NoError(t, rows.Err())
	return choices
}
