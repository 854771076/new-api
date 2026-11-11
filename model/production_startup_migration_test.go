package model

import (
	"archive/tar"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	sqlmysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Set READINESS_OLD_SCHEMA=1 to build archived fixtures (requires full git history).
// READINESS_MYSQL_DSN and READINESS_POSTGRES_DSN must point to disposable
// servers with CREATE/DROP DATABASE permission. Original production table names
// are intentional: TablePrefix cannot isolate models with explicit TableName.
func TestProductionStartupSchemaUpgrade(t *testing.T) {
	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousPath, previousMaster := common.SQLitePath, common.IsMasterNode
	previousRetireAt := LegacyAccessTokenRetireAt()
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.SQLitePath, common.IsMasterNode = previousPath, previousMaster
		legacyAccessTokenRetireAt.Store(previousRetireAt)
		initCol()
	})
	common.IsMasterNode = true
	t.Setenv("SQL_MAX_IDLE_CONNS", "2")
	t.Setenv("SQL_MAX_OPEN_CONNS", "4")
	t.Setenv("SKIP_64BIT_QUOTA_SCHEMA_CHECK", "false")

	fixtures := []struct{ name, ref string }{
		{"fresh", ""},
		{"mergebase", "8faf1d45e8c101974dc7483324d3d91c8a462ffe"},
		{"pre_app_billing", "b6751e593f9f6b5dedfa6b6aa28e2a83a6e589b2"},
		{"latest_release", "7565092ab0db9b1c99163af65c07c1cae7264831"},
	}
	binaries := make(map[string]string)
	owner := t
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			adminDSN := os.Getenv("READINESS_" + strings.ToUpper(dialect) + "_DSN")
			if dialect != "sqlite" && adminDSN == "" {
				t.Skip("disposable server DSN not configured")
			}
			for _, fixture := range fixtures {
				t.Run(fixture.name, func(t *testing.T) {
					if fixture.ref != "" && os.Getenv("READINESS_OLD_SCHEMA") != "1" {
						t.Skip("READINESS_OLD_SCHEMA=1 enables archived production fixtures")
					}
					mainDSN, mainPath := disposableStartupDatabase(t, dialect, adminDSN)
					logDSN, logPath := disposableStartupDatabase(t, dialect, adminDSN)
					t.Setenv("SQL_DSN", mainDSN)
					t.Setenv("LOG_SQL_DSN", logDSN)
					common.SQLitePath = mainPath
					if fixture.ref != "" {
						binary := binaries[fixture.ref]
						if binary == "" {
							binary = archiveStartupFixture(t, owner, fixture.ref)
							binaries[fixture.ref] = binary
						}
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
						defer cancel()
						cmd := exec.CommandContext(ctx, binary, mainPath, logPath)
						cmd.Dir = filepath.Dir(binary)
						output, err := cmd.CombinedOutput()
						require.NoError(t, err, "old production startup %s: %s", fixture.ref, output)
					}

					// Open for inspection without migrating: the schema and rows here
					// were produced exclusively by the archived production startup.
					oldMain, _, err := chooseDB("SQL_DSN", false)
					require.NoError(t, err)
					t.Cleanup(func() { _ = closeDB(oldMain) })
					common.SQLitePath = logPath
					oldLog, _, err := chooseDB("LOG_SQL_DSN", true)
					require.NoError(t, err)
					t.Cleanup(func() { _ = closeDB(oldLog) })
					var before []startupTableSnapshot
					if fixture.ref != "" {
						before = snapshotStartupTables(t, oldMain, false)
						before = append(before, snapshotStartupTables(t, oldLog, true)...)
						if fixture.name != "latest_release" {
							assert.False(t, oldMain.Migrator().HasColumn("subscription_plans", "app_id"))
						}
						assert.False(t, oldMain.Migrator().HasColumn("tasks", "app_id"))
					}
					require.NoError(t, closeDB(oldMain))
					require.NoError(t, closeDB(oldLog))

					var afterFirst []startupTableSnapshot
					for pass := range 2 {
						common.SQLitePath = mainPath
						err := InitDB()
						// Even a failed migration must release its connection before
						// the disposable database is dropped by test cleanup.
						mainDB := DB
						t.Cleanup(func() { _ = closeDB(mainDB) })
						require.NoError(t, err, "current InitDB pass %d", pass+1)
						common.SQLitePath = logPath
						err = InitLogDB()
						logDB := LOG_DB
						t.Cleanup(func() { _ = closeDB(logDB) })
						require.NoError(t, err, "current InitLogDB pass %d", pass+1)
						assert.NotSame(t, DB, LOG_DB)
						assert.Equal(t, common.DatabaseType(dialect), common.MainDatabaseType())
						assert.Equal(t, common.DatabaseType(dialect), common.LogDatabaseType())
						for _, table := range before {
							db := DB
							if table.log {
								db = LOG_DB
							}
							assert.Equal(t, table.rows, startupRows(t, db, table.name, table.columns), "old contents %s pass %d", table.name, pass+1)
							indexes := startupIndexes(t, db, table.name)
							for _, index := range table.indexes {
								assert.Contains(t, indexes, index, "old index %s pass %d", table.name, pass+1)
							}
						}
						verifyStartupAppSchema(t, fixture.ref != "")
						after := snapshotStartupTables(t, DB, false)
						after = append(after, snapshotStartupTables(t, LOG_DB, true)...)
						if pass == 0 {
							afterFirst = after
						} else {
							assert.Equal(t, afterFirst, after, "second production startup must preserve schema indexes and rows")
						}
						require.NoError(t, CloseDB())
					}
					assertStartupUniqueness(t, mainDSN, mainPath)
					var version string
					t.Setenv("STARTUP_VERSION_DSN", mainDSN)
					common.SQLitePath = mainPath
					versionDB, _, err := chooseDB("STARTUP_VERSION_DSN", false)
					require.NoError(t, err)
					query := "SELECT version()"
					if dialect == "sqlite" {
						query = "SELECT sqlite_version()"
					}
					require.NoError(t, versionDB.Raw(query).Scan(&version).Error)
					require.NoError(t, closeDB(versionDB))
					t.Logf("%s %s: %s; old startup -> current startup twice; separate main/log", dialect, fixture.name, version)
				})
			}
		})
	}
}

func disposableStartupDatabase(t *testing.T, dialect, adminDSN string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "startup.db")
	if dialect == "sqlite" {
		return "local", path
	}
	// A generated identifier, never a caller-supplied database name, is the
	// only target of CREATE/DROP. The provided database is just the admin connection.
	name := "readiness_upgrade_" + strings.ReplaceAll(common.GetUUID(), "-", "")
	t.Setenv("STARTUP_ADMIN_DSN", adminDSN)
	admin, _, err := chooseDB("STARTUP_ADMIN_DSN", false)
	require.NoError(t, err)
	sqlAdmin, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlAdmin.Close() })
	var dsn string
	if dialect == "mysql" {
		require.NoError(t, admin.Exec("CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error)
		cfg, err := sqlmysql.ParseDSN(adminDSN)
		require.NoError(t, err)
		cfg.DBName = name
		dsn = cfg.FormatDSN()
	} else {
		require.NoError(t, admin.Exec(`CREATE DATABASE "`+name+`"`).Error)
		u, err := url.Parse(adminDSN)
		require.NoError(t, err)
		u.Path = "/" + name
		dsn = u.String()
	}
	t.Cleanup(func() {
		_, err := sqlAdmin.Exec("DROP DATABASE " + name)
		assert.NoError(t, err, "clean up disposable database %s", name)
	})
	return dsn, path
}

func archiveStartupFixture(t, owner *testing.T, ref string) string {
	t.Helper()
	// Keep cached binaries until all dialects finish; clean up outside the repo.
	root := owner.TempDir()
	archive := filepath.Join(root, "source.tar")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").CombinedOutput()
	require.NoError(t, err, "%s", output)
	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", "--output="+archive, ref)
	cmd.Dir = strings.TrimSpace(string(output))
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	file, err := os.Open(archive)
	require.NoError(t, err)
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		require.True(t, filepath.IsLocal(header.Name), "unsafe archive entry %s", header.Name)
		path := filepath.Join(root, filepath.FromSlash(header.Name))
		switch header.Typeflag {
		case tar.TypeDir:
			require.NoError(t, os.MkdirAll(path, 0o755))
		case tar.TypeReg:
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			out, err := os.Create(path)
			require.NoError(t, err)
			_, copyErr := io.Copy(out, reader)
			closeErr := out.Close()
			require.NoError(t, copyErr)
			require.NoError(t, closeErr)
		}
	}
	fixtureDir := filepath.Join(root, "readinessfixture")
	require.NoError(t, os.MkdirAll(fixtureDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fixtureDir, "main.go"), []byte(oldStartupFixtureSource), 0o600))
	binary := filepath.Join(root, "startup-fixture")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd = exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, "./readinessfixture")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "build original production model package %s: %s", ref, output)
	t.Logf("archived original production startup %s at %s", ref, root)
	return binary
}

type startupIndex struct {
	Name    string
	Columns string
	Unique  bool
}

type startupColumn struct {
	Name     string
	Type     string
	Default  sql.NullString
	Nullable sql.NullBool
}

type startupTableSnapshot struct {
	name        string
	log         bool
	columns     []string
	definitions []startupColumn
	rows        []map[string]any
	indexes     []startupIndex
}

func snapshotStartupTables(t *testing.T, db *gorm.DB, isLog bool) []startupTableSnapshot {
	t.Helper()
	tables := []string{"users", "tokens", "top_ups", "subscription_plans", "subscription_orders", "user_subscriptions", "subscription_pre_consume_records", "tasks", "midjourneys", "oauth_apps", "oauth_authorization_codes", "oauth_refresh_tokens", "cli_auth_codes", "oauth_consents", "user_app_wallets", "app_products", "app_product_orders", "logs"}
	if isLog {
		tables = []string{"logs", "audit_logs"}
	}
	var snapshots []startupTableSnapshot
	for _, table := range tables {
		if !db.Migrator().HasTable(table) {
			continue
		}
		columns, err := db.Migrator().ColumnTypes(table)
		require.NoError(t, err)
		names := make([]string, len(columns))
		definitions := make([]startupColumn, len(columns))
		for i, column := range columns {
			names[i] = column.Name()
			defaultValue, hasDefault := column.DefaultValue()
			nullable, hasNullable := column.Nullable()
			definitions[i] = startupColumn{column.Name(), column.DatabaseTypeName(), sql.NullString{String: defaultValue, Valid: hasDefault}, sql.NullBool{Bool: nullable, Valid: hasNullable}}
		}
		slices.Sort(names)
		slices.SortFunc(definitions, func(a, b startupColumn) int { return strings.Compare(a.Name, b.Name) })
		snapshots = append(snapshots, startupTableSnapshot{table, isLog, names, definitions, startupRows(t, db, table, names), startupIndexes(t, db, table)})
	}
	return snapshots
}

func startupRows(t *testing.T, db *gorm.DB, table string, columns []string) []map[string]any {
	t.Helper()
	quoted := make([]string, len(columns))
	for i, column := range columns {
		var builder strings.Builder
		db.Dialector.QuoteTo(&builder, column)
		quoted[i] = builder.String()
	}
	var rows []map[string]any
	require.NoError(t, db.Table(table).Select(strings.Join(quoted, ",")).Order(quoted[0]).Find(&rows).Error)
	// Drivers can expose []byte or string for text/JSON. Normalize only that
	// representation, preserving every value and SQL NULL distinction.
	for _, row := range rows {
		for key, value := range row {
			if bytes, ok := value.([]byte); ok {
				row[key] = string(bytes)
			}
		}
	}
	return rows
}

func startupIndexes(t *testing.T, db *gorm.DB, table string) []startupIndex {
	t.Helper()
	var indexes []startupIndex
	if db.Dialector.Name() == "sqlite" {
		var entries []struct {
			Name   string
			Unique bool
		}
		require.NoError(t, db.Raw("PRAGMA index_list(`"+table+"`)").Scan(&entries).Error)
		for _, entry := range entries {
			var columns []struct{ Name string }
			require.NoError(t, db.Raw("PRAGMA index_info(`"+entry.Name+"`)").Scan(&columns).Error)
			names := make([]string, len(columns))
			for i, column := range columns {
				names[i] = column.Name
			}
			indexes = append(indexes, startupIndex{entry.Name, strings.Join(names, ","), entry.Unique})
		}
	} else if db.Dialector.Name() == "mysql" {
		require.NoError(t, db.Raw(`SELECT INDEX_NAME AS name, GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) AS columns, MIN(NON_UNIQUE) = 0 AS "unique" FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? GROUP BY INDEX_NAME`, table).Scan(&indexes).Error)
	} else {
		require.NoError(t, db.Raw(`SELECT i.relname AS name, string_agg(a.attname, ',' ORDER BY k.ordinality) AS columns, x.indisunique AS "unique" FROM pg_catalog.pg_index x JOIN pg_catalog.pg_class t ON t.oid = x.indrelid JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace JOIN pg_catalog.pg_class i ON i.oid = x.indexrelid JOIN LATERAL unnest(x.indkey) WITH ORDINALITY k(attnum, ordinality) ON true JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum WHERE n.nspname = current_schema() AND t.relname = ? GROUP BY i.relname, x.indisunique`, table).Scan(&indexes).Error)
	}
	slices.SortFunc(indexes, func(a, b startupIndex) int { return strings.Compare(a.Name, b.Name) })
	return indexes
}

func verifyStartupAppSchema(t *testing.T, seeded bool) {
	t.Helper()
	for _, table := range []string{"subscription_plans", "subscription_orders", "user_subscriptions", "top_ups", "tasks", "midjourneys"} {
		assert.True(t, DB.Migrator().HasColumn(table, "app_id"), table)
		columns, err := DB.Migrator().ColumnTypes(table)
		require.NoError(t, err)
		for _, column := range columns {
			if column.Name() == "app_id" {
				value, ok := column.DefaultValue()
				assert.True(t, ok, table)
				assert.Equal(t, "0", value, "%s app_id database default", table)
			}
		}
		assert.Contains(t, startupIndexes(t, DB, table), startupIndex{"idx_" + table + "_app_id", "app_id", false}, table)
		if seeded {
			var values []sql.NullInt64
			require.NoError(t, DB.Table(table).Pluck("app_id", &values).Error)
			for _, value := range values {
				assert.True(t, value.Valid, table)
				assert.Zero(t, value.Int64, table)
			}
		}
	}
	for _, expected := range []struct{ table, name, columns string }{
		{"user_app_wallets", "idx_user_app_wallet", "user_id,app_id"},
		{"oauth_consents", "idx_oauth_consent_app_user", "app_id,user_id"},
		{"oauth_apps", "idx_oauth_apps_client_id", "client_id"},
	} {
		assert.Contains(t, startupIndexes(t, DB, expected.table), startupIndex{expected.name, expected.columns, true})
	}
	if seeded {
		var task Task
		require.NoError(t, DB.Where("task_id = ?", "readiness-old-task").First(&task).Error)
		assert.Equal(t, TaskStatus(TaskStatusInProgress), task.Status)
		assert.Equal(t, "subscription", task.PrivateData.BillingSource)
		assert.Equal(t, 101, task.PrivateData.SubscriptionId)
		assert.Equal(t, 73, task.Quota)
		assert.Equal(t, "retained input", task.Properties.Input)
		assert.Equal(t, "old-upstream-id", task.PrivateData.UpstreamTaskID)
		var mj Midjourney
		require.NoError(t, DB.Where("mj_id = ?", "readiness-old-mj").First(&mj).Error)
		assert.Equal(t, "IN_PROGRESS", mj.Status)
		assert.Equal(t, 29, mj.Quota)
	}
}

func assertStartupUniqueness(t *testing.T, dsn, path string) {
	t.Helper()
	t.Setenv("STARTUP_UNIQUE_DSN", dsn)
	common.SQLitePath = path
	db, _, err := chooseDB("STARTUP_UNIQUE_DSN", false)
	require.NoError(t, err)
	defer closeDB(db)
	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	// Transaction-per-pair handles PostgreSQL's aborted transaction after a
	// uniqueness violation and rolls back all probes, keeping fixtures unchanged.
	for _, probe := range []struct {
		table string
		row   map[string]any
	}{
		{"user_app_wallets", map[string]any{"user_id": 9001, "app_id": 9001}},
		{"oauth_consents", map[string]any{"app_id": 9001, "user_id": 9001, "scope": "openid", "granted_at": 1}},
		{"tokens", map[string]any{"key": strings.Repeat("c", 48), "user_id": 9001}},
		{"top_ups", map[string]any{"trade_no": "readiness-duplicate"}},
		{"subscription_orders", map[string]any{"trade_no": "readiness-duplicate"}},
		{"app_product_orders", map[string]any{"trade_no": "readiness-duplicate"}},
		{"oauth_apps", map[string]any{"name": "fixture", "client_id": "readiness-duplicate", "client_secret_hash": strings.Repeat("a", 64)}},
		{"oauth_authorization_codes", map[string]any{"code_hash": strings.Repeat("a", 64), "app_id": 9001, "user_id": 9001, "session_id": "fixture", "scope": "openid", "redirect_uri": "https://example.invalid/cb", "code_challenge": "fixture", "code_challenge_method": "S256", "expires_at": 1, "created_at": 1}},
		{"oauth_refresh_tokens", map[string]any{"token_hash": strings.Repeat("a", 64), "app_id": 9001, "user_id": 9001, "session_id": "fixture", "scope": "openid", "expires_at": 1, "created_at": 1}},
		{"cli_auth_codes", map[string]any{"code_hash": strings.Repeat("a", 64), "user_id": 9001, "name": "fixture", "scopes": "[]", "expires_at": 1, "created_at": 1}},
	} {
		tx := db.Begin()
		require.NoError(t, tx.Error)
		first := make(map[string]any, len(probe.row))
		for key, value := range probe.row {
			first[key] = value
		}
		err := tx.Table(probe.table).Create(&first).Error
		if !assert.NoError(t, err, "%s first insert", probe.table) {
			_ = tx.Rollback().Error
			continue
		}
		if probe.table == "user_app_wallets" || probe.table == "oauth_consents" {
			for _, dimension := range []string{"user_id", "app_id"} {
				otherPair := make(map[string]any, len(probe.row))
				for key, value := range probe.row {
					otherPair[key] = value
				}
				otherPair[dimension] = 9002
				assert.NoError(t, tx.Table(probe.table).Create(&otherPair).Error, "%s allows distinct %s", probe.table, dimension)
			}
		}
		duplicate := probe.row
		assert.Error(t, tx.Table(probe.table).Create(&duplicate).Error, "%s must reject duplicate business key", probe.table)
		require.NoError(t, tx.Rollback().Error)
	}
}

// This program is compiled inside each unmodified git archive, so table DDL,
// migration ordering, custom dialectors and log startup come from that revision.
// Seed only inert, non-secret fixture values; no live credentials or requests.
const oldStartupFixtureSource = `package main
import (
 "encoding/json"
 "fmt"
 "os"
 "strings"
 "github.com/QuantumNous/new-api/common"
 "github.com/QuantumNous/new-api/model"
 "gorm.io/gorm"
)
func must(err error) { if err != nil { panic(err) } }
func insert(db *gorm.DB, table string, row map[string]any) {
 if db.Migrator().HasTable(table) { must(db.Table(table).Create(&row).Error) }
}
func main() {
 common.IsMasterNode = true
 common.SQLitePath = os.Args[1]
 must(model.InitDB())
 common.SQLitePath = os.Args[2]
 must(model.InitLogDB())
 insert(model.DB, "users", map[string]any{"id":101,"username":"old-fixture","password":"not-a-login-credential","quota":123456789,"used_quota":321,"aff_quota":15,"aff_history":20,"setting":"{\"theme\":\"dark\"}","group":"default"})
 insert(model.DB, "tokens", map[string]any{"id":101,"user_id":101,"key":"012345678901234567890123456789012345678901234567","name":"old token","remain_quota":9999})
 insert(model.DB, "subscription_plans", map[string]any{"id":101,"title":"old plan","price_amount":12.345678,"currency":"USD","enabled":false,"allow_balance_pay":false,"allow_wallet_overflow":false,"total_amount":987654,"duration_unit":"month","duration_value":3})
 insert(model.DB, "subscription_orders", map[string]any{"id":101,"user_id":101,"plan_id":101,"trade_no":"old-sub-trade","money":12.345678,"status":"success","create_time":1700000000,"complete_time":1700000001,"provider_payload":"{\"receipt\":\"retained\"}"})
 insert(model.DB, "top_ups", map[string]any{"id":101,"user_id":101,"trade_no":"old-topup-trade","amount":123,"money":4.25,"status":"pending","create_time":1700000000})
 insert(model.DB, "user_subscriptions", map[string]any{"id":101,"user_id":101,"plan_id":101,"amount_total":987654,"amount_used":73,"start_time":1700000000,"end_time":4102444800,"status":"active","allow_wallet_overflow":false,"source":"order"})
 insert(model.DB, "subscription_pre_consume_records", map[string]any{"id":101,"request_id":"old-reservation","user_id":101,"user_subscription_id":101,"pre_consumed":73,"status":"consumed"})
 // Use the actual old DTO/Valuers, including SQLite's BLOB representation
 // for RawMessage, rather than a text-only synthetic task payload.
 task := model.Task{ID:101,TaskID:"readiness-old-task",UserId:101,Platform:"openai",ChannelId:7,Quota:73,Status:model.TaskStatusInProgress,Progress:"35%",SubmitTime:1700000000,Properties:model.Properties{Input:"retained input"},PrivateData:model.TaskPrivateData{UpstreamTaskID:"old-upstream-id",BillingSource:"subscription",SubscriptionId:101,TokenId:101},Data:json.RawMessage("{\"id\":\"old-upstream-id\",\"status\":\"in_progress\"}")}
 must(model.DB.Create(&task).Error)
 insert(model.DB, "midjourneys", map[string]any{"id":101,"user_id":101,"mj_id":"readiness-old-mj","quota":29,"status":"IN_PROGRESS","progress":"40%","prompt":"retained MJ prompt","channel_id":7,"submit_time":1700000000,"properties":"{\"seed\":42}"})
 insert(model.DB, "oauth_apps", map[string]any{"id":101,"name":"old app","client_id":"old-client","client_secret_hash":strings.Repeat("b",64),"redirect_uris":"[\"https://example.invalid/cb\"]"})
 insert(model.DB, "oauth_consents", map[string]any{"app_id":101,"user_id":101,"scope":"openid","granted_at":1700000000})
 insert(model.DB, "oauth_authorization_codes", map[string]any{"code_hash":strings.Repeat("b",64),"app_id":101,"user_id":101,"session_id":"old-session","scope":"openid","redirect_uri":"https://example.invalid/cb","code_challenge":"old-challenge","code_challenge_method":"S256","expires_at":4102444800,"created_at":1700000000})
 insert(model.DB, "oauth_refresh_tokens", map[string]any{"token_hash":strings.Repeat("b",64),"app_id":101,"user_id":101,"session_id":"old-session","scope":"openid","expires_at":4102444800,"created_at":1700000000})
 insert(model.DB, "cli_auth_codes", map[string]any{"code_hash":strings.Repeat("b",64),"user_id":101,"name":"old CLI","scopes":"[]","expires_at":4102444800,"created_at":1700000000})
 insert(model.DB, "user_app_wallets", map[string]any{"user_id":101,"app_id":101,"quota":2468,"used_quota":135})
 insert(model.DB, "app_products", map[string]any{"id":101,"app_id":101,"name":"old product","price_amount":2.5,"currency":"USD","quota_amount":300,"enabled":false})
 insert(model.DB, "app_product_orders", map[string]any{"id":101,"app_id":101,"user_id":101,"product_id":101,"trade_no":"old-product-trade","money":2.5,"status":"pending"})
 insert(model.DB, "logs", map[string]any{"id":101,"user_id":101,"content":"main fixture","request_id":"old-main-log","quota":73})
 insert(model.LOG_DB, "logs", map[string]any{"id":101,"user_id":101,"content":"separate log fixture","request_id":"old-separate-log","quota":29})
 insert(model.LOG_DB, "audit_logs", map[string]any{"event_id":"old-audit-event","user_id":101,"created_at":1700000000,"category":"operation","action":"fixture","content":"retained audit fixture"})
 must(model.CloseDB())
 fmt.Println("archived production InitDB/InitLogDB seeded successfully")
}
`
