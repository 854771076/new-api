package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogFilterAppId(t *testing.T) {
	previousDB := DB
	previousLogDB := LOG_DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&User{}, &Log{}))
	DB = db
	LOG_DB = db
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
		_ = sqlDB.Close()
	})

	user := &User{Username: "app-filter-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(user).Error)

	now := common.GetTimestamp()
	rows := []*Log{
		{UserId: user.Id, AppId: 7, CreatedAt: now - 10, Username: user.Username, ModelName: "m-a", Quota: 100, RequestId: "app7-a"},
		{UserId: user.Id, AppId: 7, CreatedAt: now - 5, Username: user.Username, ModelName: "m-b", Quota: 200, RequestId: "app7-b"},
		{UserId: user.Id, AppId: 9, CreatedAt: now - 2, Username: user.Username, ModelName: "m-c", Quota: 300, RequestId: "app9-a"},
		{UserId: user.Id, AppId: 0, CreatedAt: now - 1, Username: user.Username, ModelName: "m-d", Quota: 400, RequestId: "legacy"},
	}
	for _, row := range rows {
		require.NoError(t, db.Create(row).Error)
	}

	app7Logs, total, err := GetUserLogs(user.Id, LogFilter{AppId: 7}, 0, 50)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, app7Logs, 2)
	for _, log := range app7Logs {
		require.Equal(t, 7, log.AppId)
	}

	app9Logs, total, err := GetUserLogs(user.Id, LogFilter{AppId: 9}, 0, 50)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, app9Logs, 1)
	require.Equal(t, "app9-a", app9Logs[0].RequestId)

	// A second migration run must stay idempotent and keep the app_id column.
	require.NoError(t, db.AutoMigrate(&Log{}))
	require.True(t, db.Migrator().HasColumn(&Log{}, "app_id"))
}
