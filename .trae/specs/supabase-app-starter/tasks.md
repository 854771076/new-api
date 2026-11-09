# 实现任务队列 — Supabase 多应用快速上线（简化版）

**本队列是 `.trae/specs/supabase-app-starter/spec.md`（简化版）的 Plan 产物。用户 2026-11-09 已确认方向。**

---

## Task 1：OAuthApp 模型与 API 扩展（SupabaseProjectRef + AIModelWhitelist）

**目标 AC**：AC-1、AC-3  
**优先级**：high  
**前置**：无

### 实现

1. **`model/oauth_app.go`**：
   - 新增两个字段：
     ```go
     SupabaseProjectRef string `json:"-" gorm:"type:varchar(64);column:supabase_project_ref"`
     AIModelWhitelist   string `json:"-" gorm:"type:text;column:ai_model_whitelist"`
     ```
   - 新增 helper：
     ```go
     func (app *OAuthApp) GetAIModelWhitelist() []string        // 空字符串 → 返回 nil, false
     func (app *OAuthApp) SetAIModelWhitelist([]string) error    // 空数组 → 存 ""
     ```
   - 新增验证函数 `ValidateAIModelWhitelist(list []string) error`：
     - 元素 ≤ 128 字符、无空字符串、无重复；
     - nil / 空数组 → 合法（表示不限制）。

2. **`controller/oauth_app.go`**：
   - `OAuthAppResponse` 新增 `SupabaseProjectRef string` 字段；
   - `CreateOAuthAppRequest` 新增可选字段：`SupabaseProjectRef *string`、`AIModelWhitelist []string`；
   - `UpdateOAuthAppRequest` 新增可选字段：`SupabaseProjectRef *string`、`AIModelWhitelist []string`；
   - `toOAuthAppResponse` 映射 SupabaseProjectRef；
   - Create / Update 中调用 `ValidateAIModelWhitelist`，校验失败 → 400；
   - Update 中处理 SupabaseProjectRef 的空字符串 → 置 ""（不更新）；
   - **不对外透传 AIModelWhitelist**（OAuthAppResponse 里不加这个字段）。

### 测试

- **rule**：`go test ./model/...` SQLite 下 Create/Get/Update OAuthApp 含新字段 → OK。
- **rule**：`go test ./controller/...` OAuthApp Create 不传新字段 → 响应与改前一致（回归）。
- **rule**：Update 传 AIModelWhitelist 非法值 → 400 + 正确错误消息。
- **rule**：`go run` 三库启动 `AutoMigrate` 两次，第二次无 ALTER。

---

## Task 2：OAuthApp 级 AI 模型白名单检查

**目标 AC**：AC-2  
**优先级**：high  
**前置**：Task 1 完成

### 实现

1. **新建 `service/oauth_app_policy.go`**：
   - `LoadOAuthAppByClientID(clientID string) (*model.OAuthApp, error)` — Redis + 内存双层缓存：
     ```
     key = "oauth_app:{clientID}"
     TTL = 5 min
     ```
     - Redis 可用：先查 Redis hash（存 name / supabase_project_ref / aimodelwhitelist_json / status_json），miss → DB 查 → 写 Redis；
     - Redis 不可用：fallback 内存 `sync.Map[string]cachedOAuthApp` + TTL 5 min；
     - DB 查询失败：返回 cached stale + `SysLog` warn（含 client_id）。
   - `CheckOAuthAppModelWhitelist(clientID, modelName string) (allowed bool, err error)` — 调用 LoadOAuthAppByClientID，若 OAuthApp.AIModelWhitelist 为空字符串 → allowed=true；否则 modelName ∈ whitelist → allowed=true；否则 allowed=false。

2. **`middleware/oauth_token_auth.go`**（或新建 `middleware/oauth_context.go`）：
   - `setupContextForOAuthToken` 成功拿到 `clientID` 后，写入 context：
     ```go
     common.SetContextKey(c, constant.ContextKeyOAuthAppClientID, clientID)
     ```
   - 常量 `ContextKeyOAuthAppClientID = "oauth_app_client_id"` 加进 `constant/` 包（若已有类似 key 则复用）。

3. **`controller/relay.go`** 的 `Relay` 函数：
   - 在 `relaycommon.GenRelayInfo` 之后、`relay.PrepareRequestBilling` 之前插入：
     ```go
     if clientID := common.GetContextKeyString(c, constant.ContextKeyOAuthAppClientID); clientID != "" {
         allowed, err := service.CheckOAuthAppModelWhitelist(clientID, relayInfo.OriginModelName)
         if err == nil && !allowed {
             // 403 OpenAI compatible error
         }
     }
     ```
   - DB 查 OAuthApp 失败 → `SysLog` warn + 放行（fail-open，不阻断请求）。

### 测试

- **rule**：OAuth token + 白名单外 model → 403 + 正确 error body。
- **rule**：OAuth token + 白名单内 model → 正常 relay。
- **rule**：OAuth token + 白名单空 → 行为与改前一致。
- **rule**：Dashboard API key（sk-*）→ 白名单逻辑**完全不触发**（context key 为空）。
- **rule**：OAuthApp DB 查不到 / 缓存 stale → warn 日志 + 放行。
- **rubric**（质量，阈值 ≥ 3/4）：
  - 4：Redis + memory fallback 都单测覆盖；key 字母序稳定；fail-open 有日志。
  - 3：主要路径覆盖，memory fallback 未测。
  - 2：逻辑有竞态或 key 污染风险。
  - 1：误拦截 Dashboard 入口。

---

## Task 3：集成验证

**目标 AC**：AC-4  
**优先级**：medium  
**前置**：Task 1 + Task 2 完成

### 实现

1. **全量编译**：`go build ./...` 无错误。
2. **现有测试回归**：`go test ./...` 通过。
3. **三库 AutoMigrate**：SQLite + MySQL 5.7.8 + PostgreSQL 9.6 各启动两次，第二次无 ALTER。
4. **手动 cURL 链路**：
   - OAuth discovery `GET /.well-known/openid-configuration` → 200；
   - JWKS `GET /oauth/jwks` → 200；
   - 授权码流程（授权 / token / userinfo / refresh / revoke）→ 全链路 PASS；
   - Dashboard API key（`sk-*`）→ 直连 `/v1/chat/completions` → 正常、不受白名单影响；
   - OAuth access_token → 直连 `/v1/chat/completions` + 白名单外 model → 403；
   - OAuth access_token → 白名单内 model → 正常、用户 Quota 正确扣减。

### 测试

- **rule**：全量 build / test 通过。
- **rule**：三库迁移幂等。
- **rule**：6 条 cURL 链路 PASS。

---

## 依赖拓扑

```
Task 1 (OAuthApp 字段)
  └── Task 2 (白名单检查 + 缓存)
        └── Task 3 (集成验证)
```

---

## 不在本队列的项（显式排除）

- App 级 AI 配额 + 独立账本（billing 路径）；
- 支付回调到应用侧 HTTP 端点；
- id_token 扩展 claims；
- 完整 Supabase SQL 迁移 / Edge Function / 小程序/H5 模板（独立 TypeScript spec）；
- App 级限流（用全局 GlobalAPIRateLimit 兜底）。
