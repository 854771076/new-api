# OAuth Demo 令牌生命周期修复 Spec

## Why

Supabase 官方明确不负责刷新 provider token，而参考应用把网关 access token（1 小时）直接当长期凭据使用，登录 1 小时后所有 AI 调用必然 401。同时参考应用存在身份声明写错、额度接口用错凭据、`user-sync` Edge Function 语法非法、skill 缺少环境初始化与自检、文档与实现不一致等问题，导致「新应用 30 分钟上线」的路径实际走不通。

## What Changes

- new-api 支持 **公共客户端（public client，PKCE、无 client_secret）**：`oauth_apps` 新增 `public_client` 字段，`/oauth/token` 与 `/oauth/revoke` 对公共客户端只校验 `client_id`，PKCE 仍强制；discovery 增加 `none` 认证方式；管理 API 与后台 OAuth Apps 页面可配置。
- 参考应用新增 **网关令牌管理器**：从 Supabase 会话读取 `provider_token` / `provider_refresh_token`，按 JWT `exp` 提前刷新，401 后重试一次，刷新失败则登出并提示重新登录。
- 修正身份来源：网关用户 ID 取 `identity_data.sub`；删除向 `user_metadata` 写 token 的逻辑（`user_metadata` 用户可改）。
- 修正额度展示：OAuth token 不能调用 `/api/user/amount`，个人中心改为展示网关用户 ID + 跳转网关查询/充值，不再显示恒为空的额度。
- 退出登录时用 `provider_refresh_token` 调 `/oauth/revoke` 吊销网关令牌。
- 修复 `supabase/functions/user-sync/index.ts` 非法 TS 语法（文件头是 `--` SQL 注释），并在登录后可选调用（失败不阻断）。
- skill 新增 **Step 0 环境初始化与自检**（`scripts/check-env.ps1` / `check-env.sh`），校验 bun / go / supabase CLI / cloudflared / newapi-cli，以及网关 discovery `issuer`、Supabase custom provider、`authorize` 是否返回 302。
- 修正文档与实现不一致：provider 标识统一 `custom:newapi`、Supabase 侧通过 Auth Admin API 创建、`templates/` 路径改为实际存在的 `references/web/outfit-demo`、README 路径与页面名、`.env.example` 增补说明。
- skill 增加「令牌生命周期与长任务」章节：写明 provider token 1 小时、PostgREST `anon` 3s / `authenticated` 8s、Edge Functions 150s/400s 等限制与推荐架构。
- **BREAKING**：`oauth_apps` 新增列，需三库迁移验证；但默认 `public_client=false`，现有应用行为不变。

## Impact

- Affected specs：`supabase-app-starter`（应用接入方式）、`oauth-provider`（客户端认证方式）
- Affected code：
  - 后端：`model/oauth_app.go`、`controller/oauth_app.go`、`controller/oauth_token.go`、`controller/oauth_userinfo.go`、`controller/oauth_discovery.go`、`middleware/oauth_token_auth.go`、`web/src/features/oauth-apps/`
  - 参考应用：`skills/new-api-oauth-integration/references/web/outfit-demo/src/**`、`supabase/functions/user-sync/index.ts`
  - skill：`skills/new-api-oauth-integration/SKILL.md`、`references/web/outfit-demo/README.md`、`.env.example`、新增 `scripts/check-env.*`

## ADDED Requirements

### Requirement: 公共 OAuth 客户端

系统 SHALL 允许管理员把 OAuth 应用标记为公共客户端（`public_client=true`）。公共客户端在 `/oauth/token` 与 `/oauth/revoke` 仅凭 `client_id` 认证，不得要求 `client_secret`；机密客户端必须继续校验 secret。

#### Scenario: 公共客户端刷新令牌
- **WHEN** 公共客户端提交 `grant_type=refresh_token&client_id=nac_xxx&refresh_token=<有效值>` 且不带 secret
- **THEN** 返回新的 access token、id_token 和轮换后的 refresh token

#### Scenario: 机密客户端不受影响
- **WHEN** 未标记为公共客户端的应用提交刷新请求但不带 secret 或 secret 错误
- **THEN** 返回 `401 invalid_client`

#### Scenario: 公共客户端仍强制 PKCE
- **WHEN** 公共客户端用授权码兑换令牌但缺少 `code_verifier`
- **THEN** 返回 `400 invalid_request`，不得因为无 secret 而跳过 PKCE

#### Scenario: 发现文档声明
- **WHEN** 请求 `/.well-known/openid-configuration`
- **THEN** `token_endpoint_auth_methods_supported` 包含 `none`

### Requirement: 客户端自动刷新网关令牌

参考应用 SHALL 在网关 access token 过期前使用 `provider_refresh_token` 刷新，并在收到 401 时重试一次。

#### Scenario: 过期前主动刷新
- **WHEN** 当前网关 access token 剩余有效期小于 60 秒
- **THEN** 发起刷新请求，后续 AI 调用使用新 token

#### Scenario: 401 后重试
- **WHEN** AI 请求返回 401 且存在可用的 provider refresh token
- **THEN** 刷新后重试一次；仍失败则登出并跳转登录页

#### Scenario: 刷新失败
- **WHEN** 刷新返回 `invalid_grant`（refresh token 过期或被吊销）
- **THEN** 清理本地令牌、登出 Supabase 会话、提示重新登录

### Requirement: 身份与额度展示正确

参考应用 SHALL 从 `identity_data.sub` 读取网关用户 ID，不得向 `user_metadata` 写入网关令牌；个人中心不得用 OAuth token 调用 Dashboard 额度接口。

#### Scenario: 个人中心
- **WHEN** 已登录用户打开个人中心
- **THEN** 展示网关用户 ID 与「去网关查看额度/充值」链接，不展示恒为 `—` 的额度字段

### Requirement: skill 环境初始化与自检

skill SHALL 提供 Step 0 初始化说明与可执行自检脚本，覆盖依赖与关键配置。

#### Scenario: 依赖缺失
- **WHEN** 运行 `scripts/check-env.ps1` 而缺少 `supabase` 或 `cloudflared`
- **THEN** 输出缺失项与安装提示，退出码非 0

#### Scenario: issuer 不一致
- **WHEN** 网关 discovery 的 `issuer` 与 Supabase provider 的 `issuer` 不一致
- **THEN** 脚本明确报告不一致，退出码非 0

#### Scenario: 配置正确
- **WHEN** 依赖齐全且 issuer 一致、`authorize?provider=custom:newapi` 返回 302
- **THEN** 全部检查通过，退出码 0

### Requirement: 可部署的 user-sync

`supabase/functions/user-sync/index.ts` SHALL 是合法 TypeScript 并能通过 `supabase functions deploy`；参考应用登录后可选调用，失败不阻断登录。

#### Scenario: 函数未部署
- **WHEN** `user-sync` 未部署或返回错误
- **THEN** 用户仍可完成登录并进入应用

### Requirement: 长任务与限制说明

skill SHALL 记录 provider token 1 小时、PostgREST 角色超时、Edge Functions 时长限制，以及「浏览器直连流式 + 结果落库 / 后台任务表 + Realtime」两种推荐模式。

#### Scenario: 阅读文档
- **WHEN** 集成方查阅 skill 的令牌生命周期章节
- **THEN** 能看到各限制的具体数值与对应规避方式

## MODIFIED Requirements

### Requirement: Supabase Custom Provider 配置

provider 标识 MUST 为 `custom:newapi`（含 `custom:` 前缀），MUST 通过 `POST /auth/v1/admin/custom-providers` 创建；不得用裸标识直接写 `auth.custom_oauth_providers`。

## REMOVED Requirements

### Requirement: 参考应用使用 client_secret

**Reason**：SPA 无法安全保存 secret，且 Supabase 直连模式的文档已声明「PKCE public client」。
**Migration**：将参考应用对应 OAuth App 标记为 `public_client=true`，Supabase provider 的 `client_secret` 置空。

### Requirement: OAuth token 读取网关额度

**Reason**：OAuth access token 只被 relay 与 `/oauth/userinfo` 接受。
**Migration**：个人中心改为链接跳转网关；如需程序化额度，改用个人访问令牌（PAT）。
