//go:build integration

package repository

import "database/sql"

// ChannelOperationsTestDatabase exposes only the isolated integration harness
// to external-package HTTP tests; no production code exports a database handle.
func ChannelOperationsTestDatabase() *sql.DB { return integrationDB }
