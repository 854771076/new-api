# App Token 异步任务支持（预扣/结算/退款 + 跨 App 隔离）

## Context

当前 App 级 OAuth token 一旦调用异步任务（video/image/task 提交）就被 `controller/relay.go` 的 `executeTaskSubmissionWith` 直接拒绝（403 `app_task_unsupported`），因为它能正确预扣 App 钱包，却无法在任务完成后把差额结算/退款归回同一个池：`service/task_billing.go` 的 `taskAdjustFunding` 只认平台钱包与订阅，`model.Task` 也没有 App 归属。

本改造放开该限制，并补齐三处连带问题：

1. 任务完成后的补扣/退款按 App 钱包结算，App 钱包 `used_quota` 与差额同步；
2. Midjourney 同样能被 App token 调用，但目前扣 App 池、失败却退平台余额，需一并修正；
3. 放开后 App token 能读到同一用户在其它 App 创建的任务，且 remix 会继承原任务计费倍率，需按 App 隔离。

## 设计决策

- **任务归属落库**：`model.Task` 与 `model.Midjourney` 各新增 `AppId int` 真实列（`gorm:"index;default:0"`），而不是塞进 `PrivateData` JSON。原因：跨 App 隔离需要在 SQL 层过滤，JSON 列无法跨 SQLite/MySQL/PostgreSQL 一致查询。
- **提交阶段不动**：`relay/relay_task.go` 的 `PreConsumeBilling` 与 `controller/relay.go` 的 `SettleBilling` 已经通过 `BillingSession`/`WalletFunding` 支持 App 钱包，包含 `recordAppWalletUsedQuota`。
- **轮询阶段按差额**：提交时已按 `result.Quota` 记过一次 App `used_quota`，所以 `RecalculateTaskQuota` 只调整 `actualQuota - task.Quota` 的差额，`RefundTaskQuota` 只回减当前 `task.Quota`，不得重记全量。
- **平台统计口径不变**：`model.UpdateUserUsedQuota`、渠道用量、请求计数保持无条件累加（与文本 relay 一致），App 钱包 `used_quota` 只是额外多记一份。
- **订阅优先**：`taskAdjustFunding` 与用量调整都必须先判 `BillingSourceSubscription`；App 级订阅任务的资金来自订阅，不写 App 钱包 `used_quota`。

## 变更清单

### 1. 数据模型与访问器

- `model/task.go`
  - `Task` 增加 `AppId int \`json:"app_id" gorm:"index;default:0"\``。
  - `InitTask` 写入 `relayInfo.AppId`。
  - 新增 `GetByTaskIdScoped(userId, appId int, taskId string)` 与 `GetByTaskIdsForPlatformsScoped(userID, appID int, platforms, taskIDs)`：`appId > 0` 时追加 `app_id = ?`；`appId == 0` 保持现有行为。原 `GetByTaskId` / `GetByTaskIdsForPlatforms` 保留给管理端与已有测试。
- `model/midjourney.go`
  - `Midjourney` 增加 `AppId int \`json:"-" gorm:"index;default:0"\``（与 `TokenId`、`BillingChannelId` 同组）。
- `model/user_app_wallet.go`
  - 新增 `AdjustUserAppUsedQuota(userId, appId, delta int) error`：`delta == 0` 直接返回；先 `EnsureUserAppWallet`，再用单条 UPDATE + `CASE WHEN used_quota + ? < 0 THEN 0 ELSE used_quota + ? END`（`GREATEST`/`MAX` 三库不一致，不能用）。

### 2. 放开限制与任务结算

- `controller/relay.go`：删除 `executeTaskSubmissionWith` 开头 `relayInfo.AppId > 0` 返回 403 `app_task_unsupported` 的分支及注释。
- `service/task_billing.go`
  - `taskAdjustFunding`：订阅分支保持；否则改为 `(&WalletFunding{userId: task.UserId, appId: task.AppId}).Settle(delta)`，复用现成的 App/平台分流（`service/funding_source.go:65-79`）。
  - 新增 `taskAdjustAppUsedQuota(task, delta)`：仅在 `task.AppId > 0 && !taskIsSubscription(task)` 时调用 `model.AdjustUserAppUsedQuota`。
  - `RefundTaskQuota` 传 `-quota`，`RecalculateTaskQuota` 传 `actualQuota - task.Quota`；两者保留现有 `model.UpdateUserUsedQuota` 与渠道用量回减。

### 3. Midjourney 退款归属

- `service/midjourney.go`
  - `PrepareMidjourneyTaskBilling`：写入 `task.AppId = relayInfo.AppId`（与 `Quota`/`TokenId` 同一持久化批次）。
  - `RefundMidjourneyQuota`：`task.AppId > 0` 时用 `model.IncreaseUserAppQuota(task.UserId, task.AppId, quota)` 并 `model.AdjustUserAppUsedQuota(-quota)`，不再 `IncreaseUserQuota`；Token/渠道/日志/`UpdateUserUsedQuota` 保持原样。
  - `SettleMidjourneyTaskBilling` 不改：`postConsumeQuotaWithResult` → `SettleBilling` 已 App 感知。

### 4. 跨 App 任务隔离

统一规则：App token（`appId > 0`）只能命中 `app_id = 本 App` 的任务；平台 token（`appId == 0`）保持现状。

替换为 scoped 访问器（appId 从 context 取：`common.GetContextKeyInt(c, constant.ContextKeyOAuthAppID)`；relay 内用 `info.AppId`）：

- `controller/task.go`：`GetTask`、删除/取消等 L40 / L69 / L287。
- `relay/relay_task.go`：remix 原任务查找 L68、L479。
- `middleware/task_plugin.go`：L459 / L505 / L1328 / L1399。
- `middleware/distributor.go`：L580。
- `router/plugin-router.go`：L481。
- `controller/oauth_app.go:403` 属模型白名单校验，不涉及任务内容，保持不动。

### 5. 测试

- `controller/relay_task_plugin_test.go`：把 `TestExecuteTaskSubmissionRejectsAppAccessTokens` 改为「App token 不再被拒绝」（断言 submit 闭包被调用、错误码不是 `app_task_unsupported`）。
- `service/task_billing_test.go`：扩展现有夹具（`TestMain` 迁移 `&model.UserAppWallet{}`、`truncate` 清 `user_app_wallets`、`makeTask` 支持 appId），新增：
  - App 任务失败退款：App 钱包余额恢复、`used_quota` 回减、平台 `users.quota` 不变；
  - 正向差额补扣与超额退款（负数 delta 不为负）；
  - App 订阅任务不写 App 钱包 `used_quota`；
  - Midjourney App 任务退款回 App 池。
- `model/app_wallet_test.go` 与 `TestAppModelsDatabaseMatrix`：`AdjustUserAppUsedQuota` 正/负/下限、`GetByTaskIdScoped` 跨 App 命中与不命中、新列迁移。

### 6. 文档同步（6 处）

- `.agents/skills/new-api-oauth-integration/SKILL.md` L185、L297
- `.../references/standards/identity.md` L385
- `.../references/standards/billing.md` L285
- `.../references/standards/api-contract.md` L160、L220

统一改为：App token 支持异步任务与 Midjourney，预扣/差额/退款均走 App 钱包，任务按 `app_id` 隔离；删除 `app_task_unsupported` 错误码说明。

## 验证

```powershell
go build ./...
go test ./controller -run "TestExecuteTaskSubmission"
go test ./service -run "TestRefundTaskQuota|TestRecalculate|TestSettleMidjourney|TestRefundMidjourney|TestWalletFunding"
go test ./model -run "TestUserAppWallet|TestAppModelsDatabaseMatrix|TestMigrationSchemaStability"
```

数据库（新增 `tasks.app_id`、`midjourneys.app_id` 列 + 索引，必须三库）：

```powershell
# SQLite 为默认；MySQL 8.0.45 / PostgreSQL 16.15 通过既有 DSN 环境变量
$env:TEST_MYSQL_DSN="...";     go test ./model -run "TestAppModelsDatabaseMatrix|TestMigrationSchemaStability"
$env:TEST_POSTGRES_DSN="...";  go test ./model -run "TestAppModelsDatabaseMatrix|TestMigrationSchemaStability"
```

- 全新库 AutoMigrate 连跑两次验证幂等；用升级库验证旧 `tasks` 数据默认 `app_id = 0` 且仍走平台结算。
- 交付说明记录数据库版本、命令与结果；任一未跑需显式声明。

端到端（本地，不依赖外网）：`controller` 集成测试用真实 SQLite 走完 `executeTaskSubmissionWith` → `settle`，断言 App 钱包扣减与任务 `app_id` 落库；`service` 测试覆盖轮询结算/退款闭环。若无法构造真实视频/图片上游，交付说明中写清未做真实上游 E2E 的原因。

完成后按 `AGENTS.md` 以 `git merge-base` 基线做 open-code-review 自查，并检查 Skill 文档与实际接口一致。

## 不做

- 不修复既有「null 上游 ID 批量失败不退款」等与 App 无关的漏洞（另行记录）。
- 不改文本 relay 的计费口径与平台 `used_quota` 语义。
