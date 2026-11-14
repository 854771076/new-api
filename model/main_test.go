//go:build integration

// Package model integration tests for the manual migration helpers. These run
// against real SQLite, MySQL, and PostgreSQL instances.
//
// Run with:
//
//	SQL_DSN='root:gsdpass@tcp(127.0.0.1:3307)/newapi?parseTime=true&charset=utf8mb4' \
//	PG_DSN='host=127.0.0.1 port=5433 user=postgres password=gsdpass dbname=newapi sslmode=disable' \
//	go test -tags integration -count=1 -run TestAddTokenConcurrency ./model/
package model

import (
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func mustOpenForTest(t *testing.T, name, dsn string, dialector gorm.Dialector) *gorm.DB {
	t.Helper()
	switch name {
	case "sqlite":
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	case "mysql":
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	case "postgres":
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err, "open %s", name)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	return db
}

// WithPrevailingDB swaps the package-level DB for the duration of fn so the
// migration helpers (which depend on the package DB) see the right handle.
func withPrevailingDB(t *testing.T, db *gorm.DB, fn func() error) {
	t.Helper()
	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
	require.NoError(t, fn())
}

// TestAddTokenConcurrencyColumn_GeneratesValidSQL covers the bug observed in
// the 0924 image: PostgreSQL rejects the SQL produced by
// addTokenConcurrencyColumn when the caller's definition already includes a
// NOT NULL + DEFAULT clause, because the PG branch appends a redundant
// "DEFAULT NULL" — producing two DEFAULT clauses.
//
// The fix lives in model/main.go: when the caller's definition is already
// fully constrained, skip the trailing DEFAULT NULL.
func TestAddTokenConcurrencyColumn_GeneratesValidSQL(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		db := mustOpenForTest(t, "sqlite", "", sqlite.Open(":memory:"))
		withPrevailingDB(t, db, func() error {
			require.NoError(t, db.Exec(`CREATE TABLE tokens (id INTEGER PRIMARY KEY)`).Error)
			require.NoError(t, db.Exec(`DROP TABLE tokens`).Error) // ensure column missing
			require.NoError(t, db.Exec(`CREATE TABLE tokens (id INTEGER PRIMARY KEY)`).Error)
			require.NoError(t, addTokenConcurrencyColumn("concurrency_limit", "INTEGER NOT NULL DEFAULT 0"))
			require.NoError(t, addTokenConcurrencyColumn("concurrency_limit_by_model", "TEXT NOT NULL DEFAULT ''"))
			require.True(t, db.Migrator().HasColumn(&Token{}, "concurrency_limit"))
			require.True(t, db.Migrator().HasColumn(&Token{}, "concurrency_limit_by_model"))
			return nil
		})
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("PG_DSN")
		if dsn == "" {
			t.Skip("PG_DSN not set; skipping PostgreSQL integration check")
		}
		db := mustOpenForTest(t, "postgres", dsn, postgres.Open(dsn))
		withPrevailingDB(t, db, func() error {
			require.NoError(t, db.Exec(`DROP TABLE IF EXISTS tokens`).Error)
			require.NoError(t, db.Exec(`CREATE TABLE tokens (id SERIAL PRIMARY KEY)`).Error)
			// With the bug present this call would emit
			//   ALTER TABLE tokens ADD COLUMN concurrency_limit INTEGER NOT NULL DEFAULT 0 DEFAULT NULL
			// and PostgreSQL would respond with SQLSTATE 42601.
			require.NoError(t, addTokenConcurrencyColumn("concurrency_limit", "INTEGER NOT NULL DEFAULT 0"))
			require.NoError(t, addTokenConcurrencyColumn("concurrency_limit_by_model", "TEXT NOT NULL DEFAULT ''"))
			require.True(t, db.Migrator().HasColumn(&Token{}, "concurrency_limit"))
			require.True(t, db.Migrator().HasColumn(&Token{}, "concurrency_limit_by_model"))
			return nil
		})
	})

	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("SQL_DSN")
		if dsn == "" {
			t.Skip("SQL_DSN not set; skipping MySQL integration check")
		}
		db := mustOpenForTest(t, "mysql", dsn, mysql.Open(dsn))
		withPrevailingDB(t, db, func() error {
			require.NoError(t, db.Exec(`DROP TABLE IF EXISTS tokens`).Error)
			require.NoError(t, db.Exec(`CREATE TABLE tokens (id INT PRIMARY KEY AUTO_INCREMENT)`).Error)
			require.NoError(t, addTokenConcurrencyColumn("concurrency_limit", "INTEGER NOT NULL DEFAULT 0"))
			require.NoError(t, addTokenConcurrencyColumn("concurrency_limit_by_model", "TEXT NOT NULL DEFAULT ''"))
			require.True(t, db.Migrator().HasColumn(&Token{}, "concurrency_limit"))
			require.True(t, db.Migrator().HasColumn(&Token{}, "concurrency_limit_by_model"))
			return nil
		})
	})
}

// ensure the test wiring above compiles when run in isolation.
var _ = fmt.Sprintf
