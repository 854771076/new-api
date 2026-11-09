# Supabase 多应用快速上线 — 网关侧补齐（简化版）

## 1. 决策前提（用户确认 2026-11-09）

- **沿用网关现有积分/订阅体系**（`model.User.Quota` + `service/billing.go` + `model/topup.go`），业务系统（Supabase）不建独立账本。
- **支付**：用户在网关完成充值（Stripe/Creem/Waffo/Epay → `CompleteSubscriptionOrder` → `creditTopUpQuota`），Supabase 侧通过 Edge Function 轮询网关 `/api/user/amount` 查询额度。**不做支付回调到应用侧 HTTP 端点**。
- **App 级 AI 额度**：不做。全部按用户 `Quota` 记账，复用现有 `PreConsumeBilling / SettleBilling / postConsumeQuotaWithResult`。
- **App 级 AI 模型白名单**：做。**细粒度 model_name** 级别，**默认不填 = 不检查**（向后兼容），仅拦截 OAuth token 入口。
- **id_token 扩展 claims**：本版不做。Supabase RLS 所需的 `gateway_user_id` 通过 OIDC 登录时 Edge Function `auth.admin.updateUserById` 写入 `auth.users.app_metadata` 解决。

## 2. 现状盘点（仅列本版涉及的）

| 项 | 网关现状 | 判定 |
|---|---|---|
| OAuth 2.0 / OIDC Provider 全套 | ✅ authorize+PKCE / token+id_token / userinfo / discovery / JWKS / introspect / revoke / logout | **已具备** |
| OAuthApp 管理（client_id/secret/redirect/scopes） | ✅ CRUD + secret rotate + hash-only 存储 | **已具备** |
| OAuth access token 作为 AI relay Bearer | ✅ `middleware.OAuthTokenAuth` 校验 + scope 限制 method + 注入 user quota/group | **已具备** |
| OAuthApp 级 Supabase 项目标识 | ❌ `OAuthApp` 无此字段 | **缺口 1**（必填补齐） |
| OAuthApp 级 AI 模型白名单 | ❌ 无 | **缺口 2**（本版补齐） |
| 支付回调到应用侧 | ❌ 无 | **本版不做** |
| App 级 AI 配额 | ❌ 无 | **本版不做** |
| id_token 扩展 claims | ❌ 无 | **本版不做** |

## 3. 功能需求

### FR-1 OAuthApp 模型扩展

- `OAuthApp` 新增两个**可选**字段（可空、零值表示不生效）：
  - `SupabaseProjectRef string` `gorm:"type:varchar(64);column:supabase_project_ref"` — 运营填，把网关 client_id ↔ Supabase 项目永久绑定，落在 DB 里而不是运营 Excel。
  - `AIModelWhitelist string` `gorm:"type:text;column:ai_model_whitelist"` — JSON 数组字符串，元素为 **model_name**（如 `gpt-4o-mini`、`claude-3-5-sonnet`）；`""` 或 `[]` 表示不限制。
- 新增 `GetAIModelWhitelist() ([]string, bool)` 和 `SetAIModelWhitelist([]string) error` helper，与现有 RedirectURIs/Scopes 模式一致。
- `controller/oauth_app.go` 的 `CreateOAuthAppRequest` / `UpdateOAuthAppRequest` 新增两个可选字段；`OAuthAppResponse` 公开 `supabase_project_ref`（`ai_model_whitelist` 只内部用，不对外透传）。
- `AIModelWhitelist` 校验：每个元素 ≤ 128 字符、非空字符串数组。

### FR-2 OAuthApp 级 AI 模型白名单中间件

- **检查位置**：`controller.Relay` 中，`relaycommon.GenRelayInfo` 之后、`relay.PrepareRequestBilling` 之前。此时 `relayInfo.OriginModelName` 已有值。
- **触发条件**：当前请求是 OAuth token 入口（通过 context 标记）**且**对应 OAuthApp 的 `AIModelWhitelist` 非空。
- **检查逻辑**：`relayInfo.OriginModelName` 是否在白名单中；不在 → 403 + OpenAI compatible error body + `insufficient_model_permission`。
- **缓存**：client_id → OAuthApp 配置走**内存 + Redis 双层缓存**（优先 Redis，fallback 内存 sync.Map + TTL 5min）。DB 查询失败时返回 cached stale 值并记 `SysLog` warn。
- **不拦截**：Dashboard API key / Personal Access Token / Browser session / 其它直连入口（这些走 `middleware.TokenAuth` 但不带 OAuth token context 标记）。

### FR-3 OAuth token 入口标记

- 在 `middleware/oauth_token_auth.go` 的 `setupContextForOAuthToken` 成功设置 context 后，写入一个稳定的 context key（如 `common.SetContextKey(c, constant.ContextKeyOAuthAppClientID, clientID)`），值为 token 里的 `client_id`。
- `controller.Relay` 里通过同一个 context key 判断"当前请求是 OAuth token 入口"。

## 4. 非功能需求

- **向后兼容**：不传新字段时 OAuthApp 创建/更新/读取行为不变；白名单字段为空时 Relay 行为不变；Dashboard 入口不受任何影响。
- **数据库兼容**：三库（SQLite / MySQL 5.7.8 / PostgreSQL 9.6）`AutoMigrate` 两次启动均幂等。
- **日志**：白名单拒绝 → `SysLog` info 日志，带 client_id + model_name + user_id；缓存 stale → warn。
- **不读 billing.md**：App 级配额不做，配额/账本逻辑完全复用现有用户维度。

## 5. 验收标准

### AC-1 OAuthApp 扩展模型与 API（rule）

- `model/oauth_app.go` 新增 2 字段 + getter/setter。
- `AutoMigrate` 三库两次启动均无 ALTER。
- Create/Update 不传新字段 → 行为与改前一致（现有回归测试通过）。
- AIModelWhitelist 校验：非数组 / 空数组 / 超长元素 → 请求被拒、返回 400。

**证据源**：`model/oauth_app.go` diff、controller diff、三库迁移日志、手动 cURL 回归。

### AC-2 OAuthApp 白名单检查（rule）

- OAuth token + 白名单外 model → 403 + OpenAI error body `insufficient_model_permission`。
- OAuth token + 白名单内 model → 正常 relay。
- OAuth token + 白名单为空 → 行为与改前一致。
- Dashboard API key（`sk-*`）→ **完全不经过白名单**，任意 model 可调用。
- OAuthApp 配置缓存命中 > 99%（连续 5 分钟相同 client_id 请求）。

**证据源**：`service/oauth_app_policy.go` 单测、手动 cURL 4 条路径验证、缓存统计（日志/metrics）。

### AC-3 OAuthApp SupabaseProjectRef 持久化（rule）

- Create OAuthApp 传 `supabase_project_ref` → DB 存储、GET 返回、三库启动幂等。

**证据源**：model diff + controller diff + 手动测试。

### AC-4 向后兼容（rule）

- 全量 `go build ./...` 通过；`go test ./...` 现有测试 + 新增测试全部通过。
- OAuth discovery / token / userinfo / Relay Dashboard 直连路径回归行为不变。

**证据源**：CI 或本地完整回归输出。

### AC-5 脚手架可跑通最小链路（rubric）

- 独立 TypeScript 脚手架（后续 spec）能跑通：OIDC 登录 → Supabase 数据查询 → 网关 AI 调用 → 用户已有额度扣减。
- 评分 ≥ 3/4：4 步链路无硬改即可跑通。

**证据源**：脚手架独立 spec 交付。本 Go 网关 spec 的 Completion Evidence 不含脚手架实现（Task 6 只做最小 JWT 手动验证）。
