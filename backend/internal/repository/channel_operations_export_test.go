//go:build integration

package repository

import (
	"database/sql"
	"github.com/redis/go-redis/v9"
)

// ChannelOperationsTestDatabase exposes only the isolated integration harness
// to external-package HTTP tests; no production code exports a database handle.
func ChannelOperationsTestDatabase() *sql.DB { return integrationDB }

// Issue594TestRedis exposes only the ephemeral integration cache used by the
// real HTTP scheduler/concurrency test. It never selects a configured endpoint.
func Issue594TestRedis() *redis.Client { return integrationRedis }
