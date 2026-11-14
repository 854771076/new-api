package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupChannelUpdateTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func createChannelConcurrencyTestChannel(t *testing.T) Channel {
	t.Helper()
	channel := Channel{
		Id:     51,
		Name:   "concurrency-channel",
		Key:    "sk-test-channel",
		Models: "gpt-4",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, DB.Create(&channel).Error)
	return channel
}

// applyChannelConcurrencyEdit mirrors the admin UpdateChannel admission path
// for the channel concurrency limit: request JSON decoded into model.Channel,
// then persisted through the Select-less Channel.Update() (CHN-01, C-2 three-
// state semantics: explicit value and explicit 0 must reach the database).
func applyChannelConcurrencyEdit(t *testing.T, channelID int, limit int) {
	t.Helper()
	edit, err := GetChannelById(channelID, true)
	require.NoError(t, err)
	payload, err := common.Marshal(map[string]any{"concurrency_limit": limit})
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(payload, edit))
	require.NoError(t, edit.Update())
}

// TestChannelUpdatePersistsConcurrencyLimitThroughUpdate pins the explicit-value
// leg of the C-2 three-state contract: payload 5 decodes into the *int field,
// Channel.Update()'s struct Updates must carry it (a nil pointer would be
// skipped; a non-nil pointer is never zero for reflect IsZero), and both the
// raw column read and GetConcurrencyLimit() must report 5. A brand-new channel
// (nil field) reads 0 = unlimited.
func TestChannelUpdatePersistsConcurrencyLimitThroughUpdate(t *testing.T) {
	setupChannelUpdateTestState(t)

	channel := createChannelConcurrencyTestChannel(t)
	fresh, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 0, fresh.GetConcurrencyLimit(), "nil ConcurrencyLimit must read as 0 (unlimited)")

	applyChannelConcurrencyEdit(t, channel.Id, 5)

	var stored int
	require.NoError(t, DB.Raw("SELECT concurrency_limit FROM channels WHERE id = ?", channel.Id).Scan(&stored).Error)
	assert.Equal(t, 5, stored, "Channel.Update() must persist explicit concurrency_limit=5")

	reloaded, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 5, reloaded.GetConcurrencyLimit(), "GetConcurrencyLimit must dereference the stored 5")
}

// TestChannelUpdateClearsConcurrencyLimitToZeroThroughUpdate pins the clear-to-0
// leg (threat T-02-13): once the limit is 5, a payload of explicit 0 = "unlimited"
// must overwrite the stored 5 through the Select-less struct Updates — a plain
// int would be skipped here and silently keep 5 (the C-2 *int rationale).
func TestChannelUpdateClearsConcurrencyLimitToZeroThroughUpdate(t *testing.T) {
	setupChannelUpdateTestState(t)

	channel := createChannelConcurrencyTestChannel(t)

	applyChannelConcurrencyEdit(t, channel.Id, 5)
	applyChannelConcurrencyEdit(t, channel.Id, 0)

	var stored int
	require.NoError(t, DB.Raw("SELECT concurrency_limit FROM channels WHERE id = ?", channel.Id).Scan(&stored).Error)
	assert.Equal(t, 0, stored, "clearing to 0 (unlimited) must persist through Channel.Update(), not keep 5")

	reloaded, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 0, reloaded.GetConcurrencyLimit(), "GetConcurrencyLimit of explicit 0 must be 0 (unlimited)")
}

// TestChannelUpdateSkipsOmittedConcurrencyLimit pins the omitted-payload leg:
// an update whose JSON leaves concurrency_limit out leaves the pointer nil, the
// Select-less struct Updates skips the column, and the stored limit survives.
// Together with the two tests above this fixes the three-state contract
// (nil = keep / 0 = clear / positive = set) that SC-2's admission semantics rest on.
func TestChannelUpdateSkipsOmittedConcurrencyLimit(t *testing.T) {
	setupChannelUpdateTestState(t)

	channel := createChannelConcurrencyTestChannel(t)

	applyChannelConcurrencyEdit(t, channel.Id, 5)

	// Payload omits concurrency_limit: only the id binds (PatchChannel's inner
	// model.Channel zero value), so the pointer stays nil and the Select-less
	// struct Updates must skip the column, keeping the stored value.
	fresh := Channel{Id: channel.Id}
	require.NoError(t, fresh.Update())

	var stored int
	require.NoError(t, DB.Raw("SELECT concurrency_limit FROM channels WHERE id = ?", channel.Id).Scan(&stored).Error)
	assert.Equal(t, 5, stored, "omitted payload must keep the stored concurrency_limit, not reset it")

	reloaded, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 5, reloaded.GetConcurrencyLimit(), "preserved value must surface through GetConcurrencyLimit")
}

// TestChannelUpdateCachePropagatesConcurrencyLimitAcrossReloads pins the CHN-01
// SC-4 structural claim: the new column rides InitChannelCache's full-structure
// DB.Find with zero cache code. A save is visible after the saving node's
// InitChannelCache; a write from "another node" (direct DB update bypassing
// this node's cache) stays invisible until the periodic sync body runs
// (SyncChannelCache calls InitChannelCache every period), then reads back.
func TestChannelUpdateCachePropagatesConcurrencyLimitAcrossReloads(t *testing.T) {
	setupChannelUpdateTestState(t)
	originalMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCache
		InitChannelCache()
	})

	channel := createChannelConcurrencyTestChannel(t)
	applyChannelConcurrencyEdit(t, channel.Id, 7)

	InitChannelCache()
	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, 7, cached.GetConcurrencyLimit(), "InitChannelCache must load the new column into the in-memory table")

	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("concurrency_limit", 3).Error)
	stale, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, 7, stale.GetConcurrencyLimit(), "another node's write must stay invisible until the sync period runs")

	InitChannelCache() // the body SyncChannelCache executes every period
	refreshed, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, 3, refreshed.GetConcurrencyLimit(), "the periodic sync must pick up the other node's new limit")
}

// TestChannelUpdateMigrateChannelConcurrencyLimitAddsColumnOnUpgrade runs the
// CHN-01 migration contract on a real SQLite file database (three-database
// matrix, SQLite leg): fresh startups and an upgrade from a release-equivalent
// channels table (current schema minus concurrency_limit, with pre-existing
// rows), each started twice. The second startup must issue zero DDL for this
// migration's column, keep existing channel rows intact, and leave
// concurrency_limit defaulted to 0 on rows created before the column existed.
// MySQL/PostgreSQL legs are env-gated siblings added with the full matrix
// harness (02-09 Task 02).
func TestChannelUpdateMigrateChannelConcurrencyLimitAddsColumnOnUpgrade(t *testing.T) {
	dir, err := os.MkdirTemp("", "newapi-0209-matrix")
	require.NoError(t, err)
	matrixDB, err := gorm.Open(sqlite.Open(filepath.Join(dir, "matrix.db")), &gorm.Config{})
	require.NoError(t, err)
	matrixSQL, err := matrixDB.DB()
	require.NoError(t, err)

	original := DB
	DB = matrixDB
	t.Cleanup(func() {
		DB = original
		_ = matrixSQL.Close()
		_ = os.RemoveAll(dir)
	})
	testChannelConcurrencyLimitMigrationMatrix(t, common.DatabaseTypeSQLite, "SELECT sqlite_version()")
}

// TestChannelUpdateConcurrencyLimitMigrationMatrixMySQL is the real MySQL leg of
// the CHN-01 three-database migration matrix (C-5 hard gate). Gated on
// TEST_MYSQL_DSN; when the variable is set the test must run — the verify
// contract treats a skip as a half-run matrix failure.
func TestChannelUpdateConcurrencyLimitMigrationMatrixMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	matrixDB, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	matrixSQL, err := matrixDB.DB()
	require.NoError(t, err)
	original := DB
	DB = matrixDB
	t.Cleanup(func() {
		DB = original
		_ = matrixSQL.Close()
	})
	testChannelConcurrencyLimitMigrationMatrix(t, common.DatabaseTypeMySQL, "SELECT version()")
}

// TestChannelUpdateConcurrencyLimitMigrationMatrixPostgreSQL is the real
// PostgreSQL leg of the CHN-01 matrix, gated on TEST_POSTGRES_DSN (same
// half-run failure contract as the MySQL entry).
func TestChannelUpdateConcurrencyLimitMigrationMatrixPostgreSQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	matrixDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}), &gorm.Config{})
	require.NoError(t, err)
	matrixSQL, err := matrixDB.DB()
	require.NoError(t, err)
	original := DB
	DB = matrixDB
	t.Cleanup(func() {
		DB = original
		_ = matrixSQL.Close()
	})
	testChannelConcurrencyLimitMigrationMatrix(t, common.DatabaseTypePostgreSQL, "SELECT version()")
}

// testChannelConcurrencyLimitMigrationMatrix is the shared CHN-01 matrix body
// (dialect-parameterized so the MySQL/PostgreSQL env-gated entries reuse it).
// Idempotency is asserted for THIS migration only: statements touching
// concurrency_limit must vanish on the second startup; other startup DDL is
// logged rather than failed (the PG access_token TYPE quirk stays scoped to
// the users table and is already deferred).
func testChannelConcurrencyLimitMigrationMatrix(t *testing.T, dialect common.DatabaseType, versionQuery string) {
	t.Helper()

	original := DB
	originalMain, originalLog := common.MainDatabaseType(), common.LogDatabaseType()
	recorder := &migrationSQLRecorder{}
	DB = DB.Session(&gorm.Session{Logger: recorder})
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() {
		DB = original
		common.SetDatabaseTypes(originalMain, originalLog)
	})

	var version string
	require.NoError(t, DB.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s", dialect, version)

	splitDDL := func(statements []string) (migration, other []string) {
		for _, statement := range statements {
			if strings.Contains(strings.ToLower(statement), "concurrency_limit") {
				migration = append(migration, statement)
			} else {
				other = append(other, statement)
			}
		}
		return migration, other
	}

	if DB.Migrator().HasTable(&Channel{}) {
		require.NoError(t, DB.Migrator().DropTable(&Channel{}))
	}

	// Fresh database: the migration is a no-op (no channels table yet) and
	// AutoMigrate builds the column; second startup must be zero DDL for this
	// migration's column.
	for pass := range 2 {
		recorder.reset()
		require.NoError(t, migrateChannelConcurrencyLimit())
		require.NoError(t, DB.AutoMigrate(&Channel{}))
		if pass == 1 {
			migrationDDL, otherDDL := splitDDL(recorder.schemaMutations())
			assert.Empty(t, migrationDDL, "fresh second startup must not re-issue migration DDL")
			if len(otherDDL) > 0 {
				t.Logf("fresh second startup DDL outside this migration: %v", otherDDL)
			}
		}
	}
	assertChannelConcurrencyLimitColumn(t)

	// Upgrade: current schema minus the new column, with pre-existing rows —
	// the representative shape of a database created before this migration.
	require.NoError(t, DB.Migrator().DropTable(&Channel{}))
	require.NoError(t, DB.AutoMigrate(&Channel{}))
	require.NoError(t, DB.Create(&Channel{
		Id: 71, Name: "upgrade-channel", Key: "sk-seed", Models: "gpt-4,claude",
		Group: "default", Status: common.ChannelStatusEnabled, UsedQuota: 42,
	}).Error)
	require.NoError(t, DB.Exec("ALTER TABLE channels DROP COLUMN concurrency_limit").Error)

	for pass := range 2 {
		recorder.reset()
		require.NoError(t, migrateChannelConcurrencyLimit())
		require.NoError(t, DB.AutoMigrate(&Channel{}))
		migrationDDL, otherDDL := splitDDL(recorder.schemaMutations())
		if pass == 0 {
			t.Logf("upgrade startup DDL: %v", recorder.schemaMutations())
			require.NotEmpty(t, migrationDDL, "upgrade startup must add the missing column")
		}
		if len(otherDDL) > 0 {
			t.Logf("upgrade startup DDL outside this migration: %v", otherDDL)
		}
		if pass == 1 {
			assert.Empty(t, migrationDDL, "upgrade second startup must not re-issue migration DDL")
		}
	}
	assertChannelConcurrencyLimitColumn(t)

	var upgraded Channel
	require.NoError(t, DB.First(&upgraded, 71).Error)
	assert.Equal(t, "upgrade-channel", upgraded.Name, "existing channel rows must survive the migration")
	assert.Equal(t, "gpt-4,claude", upgraded.Models, "existing model routing data must survive the migration")
	assert.Equal(t, int64(42), upgraded.UsedQuota, "existing usage data must survive the migration")
	assert.Equal(t, 0, upgraded.GetConcurrencyLimit(), "pre-existing rows must default to 0 (unlimited)")
}

func assertChannelConcurrencyLimitColumn(t *testing.T) {
	t.Helper()
	columnTypes, err := DB.Migrator().ColumnTypes(&Channel{})
	require.NoError(t, err)
	for _, columnType := range columnTypes {
		if !strings.EqualFold(columnType.Name(), "concurrency_limit") {
			continue
		}
		assert.Contains(t, strings.ToLower(columnType.DatabaseTypeName()), "int",
			"concurrency_limit must be an integer column")
		defaultValue, ok := columnType.DefaultValue()
		require.True(t, ok, "concurrency_limit must report a column default")
		assert.Equal(t, "0", defaultValue, "concurrency_limit must default to 0 (unlimited)")
		return
	}
	require.Fail(t, "channels.concurrency_limit column is missing")
}
