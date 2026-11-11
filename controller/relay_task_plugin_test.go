package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func (b *taskSubmissionTestBilling) ReserveBeforeSubmit(quota int) error { return b.Reserve(quota) }

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

// The submit presenter may answer with the vendor's status; a native route that
// already relayed the vendor's event stream gets nothing more.
func TestPresentTaskSubmissionHonorsReplyWrapperAndRelayedStreams(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"accepted-test",name:"Accepted",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {__status:202,__body:{id:task.task_id}};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	present := func(recorder *httptest.ResponseRecorder, written bool) {
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
		c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
		c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})
		if written {
			_, _ = c.Writer.WriteString("data: {}\n\n")
		}
		presentTaskSubmission(c, &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: &model.Task{TaskID: "task_public"}, RelayInfo: &relaycommon.RelayInfo{}})
	}

	accepted := httptest.NewRecorder()
	present(accepted, false)
	assert.Equal(t, http.StatusAccepted, accepted.Code)
	assert.JSONEq(t, `{"id":"task_public"}`, accepted.Body.String())

	streamed := httptest.NewRecorder()
	present(streamed, true)
	assert.Equal(t, "data: {}\n\n", streamed.Body.String())
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionRefundsWhenInsertFails(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, false, &events)
	_ = database
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_insert_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, settleErr: errors.New("settlement failed")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_billing_settlement_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := taskSubmissionTestContext()
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{})
			previousDB := model.DB
			model.DB = database
			t.Cleanup(func() { model.DB = previousDB })
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

			c := taskSubmissionTestContext()
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
		})
	}
}

func TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 2)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		cancel()
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitted := false

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitted = true
		return nil, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.False(t, submitted)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitStarted := make(chan struct{})
	done := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError

	go func() {
		defer close(done)
		outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusInternalServerError)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not stop after disconnect")
	}

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	billing := &taskSubmissionTestBilling{
		events:   &events,
		onSettle: cancel,
	}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "task_public", outcome.Task.TaskID)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

// setupTaskSubmissionDatabase opens the dialect selected by
// TEST_TASK_DB_DIALECT (SQLite in memory by default) and records every task
// INSERT in events so tests can assert the reserve → insert → settle order.
// Without migrate the task table does not exist and inserts fail.
func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Unique table prefixes keep the fixture isolated from all existing tables.
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		dsn := os.Getenv("TEST_TASK_SQLITE_DSN")
		if dsn == "" {
			dsn = ":memory:"
		}
		driver = sqlite.Open(dsn)
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status string
		actual       any
		count        float64
	}{
		{"partial", "SUCCESS", 2, 2}, {"zero", "SUCCESS", 0, 0}, {"larger", "SUCCESS", 6, 6},
		{"invalid usage", "SUCCESS", -1, 4}, {"expression failure", "SUCCESS", 7, 4}, {"negative result", "SUCCESS", 8, 4}, {"missing usage", "SUCCESS", nil, 4}, {"failed", "FAILURE", 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.LockedChannel = &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			assert.Equal(t, want, stored.Quota)
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			assert.Equal(t, float64(4), info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota)
			assert.Equal(t, want, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			require.NoError(t, info.Billing.Settle(want))
			info.Billing.Refund(c)
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota, "terminal settlement is idempotent")
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "task_accepted", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

func TestExecuteTaskSubmissionRefundsWhenFinalReserveFails(t *testing.T) {
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, reserveErr: errors.New("insufficient funds")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: "SUCCESS"}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.Equal(t, []string{"reserve", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

// An app access token now submits async tasks like a platform token: billing
// reserves and settles against the app wallet, and the persisted task records
// the owning app so reads can be scoped to it.
func TestExecuteTaskSubmissionAllowsAppAccessTokens(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.UserAppWallet{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{
		"document-model": `u("units") > 0 ? tier("bulk", u("units") * 0.01) : tier("small", 0.0)`,
	})
	const source = `
export const meta={apiVersion:1,key:"app-settlement",name:"App settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status}};}
export function extractUsage(){return {units:4};}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"SUCCESS"}`))
	}))
	defer server.Close()

	const appId = 7
	initial := int(20 * common.QuotaPerUnit)
	user := model.User{Username: "task_app_user", AffCode: "task_app_aff", Quota: initial}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.UserAppWallet{UserId: user.Id, AppId: appId, Quota: initial}).Error)
	ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
	require.NoError(t, db.Create(&ch).Error)

	c := taskSubmissionTestContext()
	c.Set("group", "default")
	c.Set("username", user.Username)
	c.Set("task_request", map[string]any{"model": "document-model"})
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
	common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
	common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
	common.SetContextKey(c, constant.ContextKeyOAuthAppID, appId)

	info := taskSubmissionRelayInfo(nil)
	info.UserId = user.Id
	info.AppId = appId
	info.OriginModelName = "document-model"
	info.UserGroup = "default"
	info.UsingGroup = "default"
	info.IsPlayground = true
	info.UserSetting.BillingPreference = "wallet_only"
	info.PublicTaskID = model.GenerateTaskID()
	info.LockedChannel = &ch

	outcome, taskErr := executeTaskSubmission(c, info)
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	want := common.QuotaRound(4 * 0.01 * common.QuotaPerUnit)
	assert.Equal(t, want, outcome.Result.Quota)

	var stored model.Task
	require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
	assert.Equal(t, appId, stored.AppId)
	assert.Equal(t, want, stored.Quota)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), stored.Status)

	var wallet model.UserAppWallet
	require.NoError(t, db.Where("user_id = ? AND app_id = ?", user.Id, appId).First(&wallet).Error)
	assert.Equal(t, initial-want, wallet.Quota, "app wallet is charged")
	assert.Equal(t, want, wallet.UsedQuota, "app usage is recorded once at settle")

	var updatedUser model.User
	require.NoError(t, db.First(&updatedUser, user.Id).Error)
	assert.Equal(t, initial, updatedUser.Quota, "platform wallet is untouched")

	var logs []model.Log
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, want, logs[0].Quota)
}

func TestTaskAppPublicationCrash(t *testing.T) {
	if os.Getenv("TEST_TASK_CRASH_CHILD") == "1" {
		var driver gorm.Dialector
		switch common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT")) {
		case "", common.DatabaseTypeSQLite:
			driver = sqlite.Open(os.Getenv("TEST_TASK_SQLITE_DSN"))
		case common.DatabaseTypeMySQL:
			driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
		case common.DatabaseTypePostgreSQL:
			driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
		default:
			t.Fatal("unsupported crash-test database")
		}
		db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: os.Getenv("TEST_TASK_CRASH_PREFIX")}})
		require.NoError(t, err)
		model.DB = db
		if os.Getenv("TEST_TASK_CRASH_PHASE") == "before_commit" {
			require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:crash_before_commit", func(tx *gorm.DB) {
				if tx.Error == nil && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
					// Exit without running transaction rollback or any deferred cleanup.
					os.Exit(23)
				}
			}))
		}
		task := &model.Task{TaskID: "crash_task", UserId: 1, AppId: 7, Quota: 100}
		require.NoError(t, task.InsertWithAppUsage(context.Background(), task.Quota))
		os.Exit(23)
	}

	for _, phase := range []string{"before_commit", "after_commit"} {
		t.Run(phase, func(t *testing.T) {
			if dialect := os.Getenv("TEST_TASK_DB_DIALECT"); dialect == "" || dialect == "sqlite" {
				t.Setenv("TEST_TASK_SQLITE_DSN", filepath.Join(t.TempDir(), "publication.db"))
			}
			db, _ := openTaskDialectDatabase(t, &model.Task{}, &model.UserAppWallet{})
			require.NoError(t, db.Create(&model.UserAppWallet{UserId: 1, AppId: 7, Quota: 1000}).Error)
			executable, err := os.Executable()
			require.NoError(t, err)
			cmd := exec.Command(executable, "-test.run=^TestTaskAppPublicationCrash$")
			cmd.Env = append(os.Environ(), "TEST_TASK_CRASH_CHILD=1", "TEST_TASK_CRASH_PHASE="+phase,
				"TEST_TASK_CRASH_PREFIX="+db.NamingStrategy.(schema.NamingStrategy).TablePrefix)
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr, "%s", output)
			require.Equal(t, 23, exitErr.ExitCode(), "%s", output)
			var wallet model.UserAppWallet
			require.NoError(t, db.Where("user_id = ? AND app_id = ?", 1, 7).First(&wallet).Error)
			var count int64
			require.NoError(t, db.Model(&model.Task{}).Where("task_id = ?", "crash_task").Count(&count).Error)
			assert.Equal(t, 1000, wallet.Quota)
			if phase == "before_commit" {
				assert.Zero(t, count)
				assert.Zero(t, wallet.UsedQuota)
			} else {
				assert.EqualValues(t, 1, count)
				assert.Equal(t, 100, wallet.UsedQuota)
			}
		})
	}
}

func TestTaskAppCompletionTransactionDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Task{}, &model.UserAppWallet{})
	oldDB, oldType := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetDatabaseTypes(dialect, common.LogDatabaseType())
	t.Cleanup(func() { model.DB = oldDB; common.SetDatabaseTypes(oldType, common.LogDatabaseType()) })
	for index, tc := range []struct {
		name    string
		actual  int
		failure string
	}{
		{"upward marker rollback", 140, "marker"},
		{"downward marker rollback", 40, "marker"},
		{"refund usage rollback", 0, "usage"},
		{"downward capacity rollback", 40, "capacity"},
		{"refund capacity rollback", 0, "capacity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := index + 1
			initial := 900
			if tc.failure == "capacity" {
				initial = common.MaxWalletQuota - 10
			}
			require.NoError(t, db.Create(&model.UserAppWallet{UserId: userID, AppId: 7, Quota: initial, UsedQuota: 100}).Error)
			task := &model.Task{TaskID: tc.name, UserId: userID, AppId: 7, Status: model.TaskStatusInProgress, Quota: 100}
			require.NoError(t, task.Insert())
			injected := true
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:completion_rollback", func(tx *gorm.DB) {
				if !injected || tx.Statement.Schema == nil {
					return
				}
				if values, ok := tx.Statement.Dest.(map[string]any); ok {
					_, marker := values["quota"]
					_, usage := values["used_quota"]
					if tc.failure == "marker" && tx.Statement.Schema.Name == "Task" && marker || tc.failure == "usage" && tx.Statement.Schema.Name == "UserAppWallet" && usage {
						tx.AddError(errors.New("injected financial write failure"))
					}
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("test:completion_rollback")) })
			delta, err := task.SettleAppWalletQuota(context.Background(), tc.actual)
			require.Error(t, err)
			assert.Zero(t, delta)
			assert.Equal(t, 100, task.Quota)
			var wallet model.UserAppWallet
			require.NoError(t, db.Where("user_id = ? AND app_id = ?", userID, 7).First(&wallet).Error)
			assert.Equal(t, initial, wallet.Quota)
			assert.Equal(t, 100, wallet.UsedQuota)
			var persisted model.Task
			require.NoError(t, db.First(&persisted, task.ID).Error)
			assert.Equal(t, 100, persisted.Quota)
			injected = false
			if tc.failure == "capacity" {
				require.NoError(t, db.Model(&wallet).Update("quota", 900).Error)
				initial = 900
			}
			delta, err = task.SettleAppWalletQuota(context.Background(), tc.actual)
			require.NoError(t, err)
			assert.Equal(t, tc.actual-100, delta)
			stale := persisted
			delta, err = stale.SettleAppWalletQuota(context.Background(), tc.actual)
			require.NoError(t, err)
			assert.Zero(t, delta)
			require.NoError(t, db.First(&wallet, wallet.Id).Error)
			assert.Equal(t, initial+100-tc.actual, wallet.Quota)
			assert.Equal(t, tc.actual, wallet.UsedQuota)
		})
	}
}

type appWalletCrashPollingAdaptor struct {
	actual int
}

func (a *appWalletCrashPollingAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *appWalletCrashPollingAdaptor) FetchTask(string, string, *model.Task, string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}
func (a *appWalletCrashPollingAdaptor) ParseTaskResult(*model.Task, *http.Response, []byte) (*relaycommon.TaskInfo, error) {
	if a.actual == 0 {
		return relaycommon.FailTaskInfo("provider failed"), nil
	}
	return &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}, nil
}
func (a *appWalletCrashPollingAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return a.actual
}

// The subprocess exits at the real transaction boundary, without unwinding Go
// defers. Restart polling must reconcile the durable marker, not reapply funds.
func TestTaskAppCompletionCrash(t *testing.T) {
	if os.Getenv("TEST_TASK_COMPLETION_CHILD") == "1" {
		var driver gorm.Dialector
		dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
		switch dialect {
		case "", common.DatabaseTypeSQLite:
			dialect = common.DatabaseTypeSQLite
			driver = sqlite.Open(os.Getenv("TEST_TASK_SQLITE_DSN"))
		case common.DatabaseTypeMySQL:
			driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
		case common.DatabaseTypePostgreSQL:
			driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
		default:
			t.Fatal("unsupported completion crash-test database")
		}
		db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: os.Getenv("TEST_TASK_CRASH_PREFIX")}})
		require.NoError(t, err)
		pool := &taskSubmissionCommitPool{ConnPool: db.ConnPool}
		db.ConnPool, db.Statement.ConnPool = pool, pool
		model.DB, model.LOG_DB = db, db
		common.SetDatabaseTypes(dialect, dialect)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
		constant.TaskTimeoutMinutes = 0
		constant.TaskQueryLimit = 100
		require.NoError(t, db.Callback().Update().After("gorm:update").Before("gorm:commit_or_rollback_transaction").Register("test:completion_crash", func(tx *gorm.DB) {
			if tx.Error != nil || tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Task" {
				return
			}
			values, ok := tx.Statement.Dest.(map[string]any)
			if !ok {
				return
			}
			if _, marker := values["quota"]; !marker {
				return
			}
			if os.Getenv("TEST_TASK_CRASH_PHASE") == "before_commit" {
				os.Exit(23)
			}
			commitTx, ok := tx.Statement.ConnPool.(*taskSubmissionCommitTx)
			require.True(t, ok)
			commitTx.afterCommit = func() { os.Exit(23) }
		}))
		actual, err := strconv.Atoi(os.Getenv("TEST_TASK_COMPLETION_QUOTA"))
		require.NoError(t, err)
		service.GetTaskAdaptorFunc = func(constant.TaskPlatform) service.TaskPollingAdaptor {
			return &appWalletCrashPollingAdaptor{actual: actual}
		}
		service.RunTaskPollingOnce(context.Background(), nil)
		t.Fatal("financial crash boundary was not reached")
	}
	for _, actual := range []int{0, 40, 140} {
		for _, phase := range []string{"before_commit", "after_commit"} {
			t.Run(fmt.Sprintf("%d/%s", actual, phase), func(t *testing.T) {
				if dialect := os.Getenv("TEST_TASK_DB_DIALECT"); dialect == "" || dialect == "sqlite" {
					t.Setenv("TEST_TASK_SQLITE_DSN", filepath.Join(t.TempDir(), "completion.db"))
				}
				db, dialect := openTaskDialectDatabase(t, &model.Task{}, &model.UserAppWallet{}, &model.User{}, &model.Channel{}, &model.Log{})
				oldDB, oldLogDB := model.DB, model.LOG_DB
				oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
				oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
				oldFactory, oldTimeout, oldLimit := service.GetTaskAdaptorFunc, constant.TaskTimeoutMinutes, constant.TaskQueryLimit
				model.DB, model.LOG_DB = db, db
				common.SetDatabaseTypes(dialect, dialect)
				common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
				constant.TaskTimeoutMinutes = 0
				constant.TaskQueryLimit = 100
				service.GetTaskAdaptorFunc = func(constant.TaskPlatform) service.TaskPollingAdaptor {
					return &appWalletCrashPollingAdaptor{actual: actual}
				}
				t.Cleanup(func() {
					model.DB, model.LOG_DB = oldDB, oldLogDB
					common.SetDatabaseTypes(oldMain, oldLog)
					common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
					service.GetTaskAdaptorFunc, constant.TaskTimeoutMinutes, constant.TaskQueryLimit = oldFactory, oldTimeout, oldLimit
				})
				user := &model.User{Username: "completion_user", AffCode: "completion_aff", Quota: 1000, UsedQuota: 100, RequestCount: 1}
				require.NoError(t, db.Create(user).Error)
				channel := &model.Channel{Type: constant.ChannelTypeTaskPlugin, Name: "completion", Key: "test", Status: common.ChannelStatusEnabled, UsedQuota: 100}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, db.Create(&model.UserAppWallet{UserId: user.Id, AppId: 7, Quota: 900, UsedQuota: 100}).Error)
				task := &model.Task{TaskID: "completion_task", UserId: user.Id, AppId: 7, ChannelId: channel.Id, Platform: "wallet-crash", Quota: 100, Status: model.TaskStatusInProgress, Progress: "50%", SubmitTime: time.Now().Unix()}
				require.NoError(t, task.Insert())
				executable, err := os.Executable()
				require.NoError(t, err)
				cmd := exec.Command(executable, "-test.run=^TestTaskAppCompletionCrash$")
				cmd.Env = append(os.Environ(), "TEST_TASK_COMPLETION_CHILD=1", "TEST_TASK_CRASH_PHASE="+phase,
					"TEST_TASK_CRASH_PREFIX="+db.NamingStrategy.(schema.NamingStrategy).TablePrefix,
					"TEST_TASK_COMPLETION_QUOTA="+strconv.Itoa(actual))
				output, err := cmd.CombinedOutput()
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr, "%s", output)
				require.Equal(t, 23, exitErr.ExitCode(), "%s", output)
				var persisted model.Task
				require.NoError(t, db.First(&persisted, task.ID).Error)
				assert.EqualValues(t, model.TaskStatusInProgress, persisted.Status, "financial commit precedes terminal publication")
				want := 100
				if phase == "after_commit" {
					want = actual
				}
				assert.Equal(t, want, persisted.Quota)
				var wallet model.UserAppWallet
				require.NoError(t, db.Where("user_id = ? AND app_id = ?", user.Id, 7).First(&wallet).Error)
				assert.Equal(t, 1000-want, wallet.Quota)
				assert.Equal(t, want, wallet.UsedQuota)
				service.RunTaskPollingOnce(context.Background(), nil)
				service.RunTaskPollingOnce(context.Background(), nil)
				require.NoError(t, db.First(&persisted, task.ID).Error)
				wantStatus := model.TaskStatus(model.TaskStatusSuccess)
				if actual == 0 {
					wantStatus = model.TaskStatusFailure
				}
				assert.Equal(t, wantStatus, persisted.Status)
				assert.Equal(t, actual, persisted.Quota)
				require.NoError(t, db.First(&wallet, wallet.Id).Error)
				assert.Equal(t, 1000-actual, wallet.Quota)
				assert.Equal(t, actual, wallet.UsedQuota)
				var logs int64
				require.NoError(t, db.Model(&model.Log{}).Count(&logs).Error)
				if phase == "before_commit" {
					assert.EqualValues(t, 1, logs)
				} else {
					assert.Zero(t, logs, "ancillary logs are not crash-atomic with the wallet")
				}
			})
		}
	}
}

type taskSubmissionCommitPool struct {
	gorm.ConnPool
}

func (pool *taskSubmissionCommitPool) GetDBConn() (*sql.DB, error) {
	return pool.ConnPool.(*sql.DB), nil
}

func (pool *taskSubmissionCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := pool.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &taskSubmissionCommitTx{ConnPool: tx, TxCommitter: tx}, nil
}

type taskSubmissionCommitTx struct {
	gorm.ConnPool
	gorm.TxCommitter
	afterCommit func()
}

func (tx *taskSubmissionCommitTx) Commit() error {
	if err := tx.TxCommitter.Commit(); err != nil {
		return err
	}
	if tx.afterCommit != nil {
		tx.afterCommit()
	}
	return nil
}

func TestTaskAppUsageLifecycle(t *testing.T) {
	for index, tc := range []struct {
		name           string
		action         string
		streamed       bool
		prepareFailure bool
		insertFailure  bool
		refundFailure  bool
		ordinary       bool
		actual         int
	}{
		{name: "refund before submit settlement", action: "refund", actual: 100},
		{name: "downward completion before submit settlement", action: "recalculate", actual: 100},
		{name: "refund with downward submit adjustment", action: "refund", actual: 60},
		{name: "insertion failure rolls back usage", insertFailure: true, actual: 100},
		{name: "preparation failure prevents publication", prepareFailure: true, actual: 100},
		{name: "stream insertion failure remains billed", insertFailure: true, streamed: true, actual: 100},
		{name: "stream preparation failure still settles", prepareFailure: true, streamed: true, actual: 100},
		{name: "failed funding refund leaves unpublished usage rolled back", insertFailure: true, refundFailure: true, actual: 100},
		{name: "ordinary app settlement unchanged", ordinary: true, actual: 100},
		{name: "ordinary app downward settlement", ordinary: true, actual: 60},
		{name: "ordinary app upward settlement", ordinary: true, actual: 140},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.UserAppWallet{})
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
			model.DB, model.LOG_DB = db, db
			common.SetDatabaseTypes(dialect, dialect)
			common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMain, oldLog)
				common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
			})

			const initial, reserved, appId = 1000, 100, 7
			user := model.User{Username: fmt.Sprintf("usage_user_%d", index), AffCode: fmt.Sprintf("usage_aff_%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&model.UserAppWallet{UserId: user.Id, AppId: appId, Quota: initial}).Error)
			channel := model.Channel{Name: "usage provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&channel).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			info := taskSubmissionRelayInfo(nil)
			info.UserId, info.AppId = user.Id, appId
			info.UserGroup = "default"
			info.IsPlayground, info.ForcePreConsume = true, true
			info.UserSetting.BillingPreference = "wallet_only"
			quotaWarnings := false
			info.UserSetting.QuotaWarningEnabled = &quotaWarnings
			info.LockedChannel = &channel
			info.PublicTaskID = model.GenerateTaskID()
			info.PriceData.Quota = tc.actual
			require.Nil(t, service.PreConsumeBilling(c, reserved, info))
			var wallet model.UserAppWallet
			require.NoError(t, db.Where("user_id = ? AND app_id = ?", user.Id, appId).First(&wallet).Error)
			assert.Equal(t, initial-reserved, wallet.Quota)
			assert.Zero(t, wallet.UsedQuota, "reservations alone do not record usage")

			if tc.ordinary {
				require.NoError(t, info.Billing.Settle(tc.actual))
				require.NoError(t, info.Billing.Settle(tc.actual))
				info.Billing.Refund(c)
				require.NoError(t, db.First(&wallet, wallet.Id).Error)
				assert.Equal(t, initial-tc.actual, wallet.Quota)
				assert.Equal(t, tc.actual, wallet.UsedQuota)
				return
			}

			pool := &taskSubmissionCommitPool{ConnPool: db.ConnPool}
			db.ConnPool, db.Statement.ConnPool = pool, pool
			refundDone := make(chan struct{}, 1)
			preparationFailed := false
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:billing_failure", func(tx *gorm.DB) {
				if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "UserAppWallet" {
					return
				}
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok {
					return
				}
				if expr, ok := values["used_quota"].(clause.Expr); ok && expr.Vars[0].(int) > 0 && tc.prepareFailure && !preparationFailed {
					preparationFailed = true
					tx.AddError(errors.New("injected usage preparation failure"))
				}
				if expr, ok := values["quota"].(clause.Expr); ok && expr.SQL == "quota + ?" && tc.refundFailure {
					tx.AddError(errors.New("injected funding refund failure"))
				}
			}))
			require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("test:refund_done", func(tx *gorm.DB) {
				if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "UserAppWallet" {
					return
				}
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok {
					return
				}
				if expr, ok := values["used_quota"].(clause.Expr); ok && expr.Vars[0].(int) < 0 {
					refundDone <- struct{}{}
				} else if expr, ok := values["quota"].(clause.Expr); ok && expr.SQL == "quota + ?" && (tc.prepareFailure || tc.insertFailure || tc.refundFailure) {
					refundDone <- struct{}{}
				}
			}))
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:insert_failure", func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" && tc.insertFailure {
					tx.AddError(errors.New("injected task insertion failure"))
				}
			}))
			published := false
			require.NoError(t, db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register("test:early_completion", func(tx *gorm.DB) {
				if tx.Error != nil || tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Task" {
					return
				}
				commitTx, ok := tx.Statement.ConnPool.(*taskSubmissionCommitTx)
				require.True(t, ok)
				commitTx.afterCommit = func() {
					published = true
					tasks := model.GetAllUnFinishSyncTasks(10)
					require.Len(t, tasks, 1, "the poller can see the committed task before submit settlement")
					require.NoError(t, db.First(&wallet, wallet.Id).Error)
					assert.Equal(t, tc.actual, wallet.UsedQuota, "initial usage commits with publication")
					switch tc.action {
					case "refund":
						require.True(t, service.RefundTaskQuota(context.Background(), tasks[0], "provider failed"))
					case "recalculate":
						service.RecalculateTaskQuota(context.Background(), tasks[0], 40, common.NewMessage("actual usage"))
					}
				}
			}))

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				if tc.streamed {
					_, _ = c.Writer.WriteString("data: {}\n\n")
				}
				return &relay.TaskSubmitResult{Platform: "plugin", Quota: tc.actual}, nil
			})
			want := tc.actual
			if tc.prepareFailure || tc.insertFailure {
				require.NotNil(t, taskErr)
				assert.Equal(t, "task_insert_failed", taskErr.Code)
				assert.Nil(t, outcome)
				assert.False(t, published)
				if !tc.streamed {
					select {
					case <-refundDone:
					case <-time.After(5 * time.Second):
						t.Fatal("billing refund did not finish")
					}
					info.Billing.Refund(c)
					if !tc.refundFailure {
						want = 0
					}
				}
			} else {
				require.Nil(t, taskErr)
				require.NotNil(t, outcome)
				assert.True(t, published)
				if tc.action == "refund" {
					want = 0
				} else if tc.action == "recalculate" {
					want = 40
				}
				require.NoError(t, info.Billing.Settle(tc.actual))
				info.Billing.Refund(c)
			}
			require.NoError(t, db.First(&wallet, wallet.Id).Error)
			assert.Equal(t, initial-want, wallet.Quota)
			wantUsage := want
			if tc.insertFailure && !tc.streamed {
				wantUsage = 0
			}
			assert.Equal(t, wantUsage, wallet.UsedQuota, "only committed net usage remains")
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial, updated.Quota, "platform funds are untouched")
			if tc.streamed {
				var logs []model.Log
				require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
				require.Len(t, logs, 1)
				assert.Equal(t, tc.actual, logs[0].Quota)
				assert.False(t, info.Billing.NeedsRefund())
			}
		})
	}
}
