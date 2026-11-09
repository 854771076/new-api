# 审计问题修复 Spec

## Why

`main` 基线审计发现 OAuth/OIDC 授权链路、Supabase 模板、newapi-cli 与前端存在可证实的 Critical/High 缺陷，其中同意页 scope 提权、user-sync 无鉴权、同意页 CSRF 可直接导致账号 API 被接管或用户档案被篡改；同时存在若干 Medium 级正确性与治理问题。需要一次性收口，避免带缺陷发布。

## What Changes

- **Critical**
  - 同意页 `POST /oauth/authorize` 复用 GET 路径的 scope 校验（`IsValidOAuthScope` + `app.ScopeAllowed`），不再信任客户端提交的 scope。
  - Supabase `user-sync` Edge Function 改为从请求 JWT 解析真实用户，忽略请求体中的 `user`/`gateway_user_id`；模板与 demo 副本同步修复。
  - 同意 POST 增加 CSRF token 与 Origin / Sec-Fetch-Site 校验；会话 Cookie 的 SameSite 策略改为不再对跨站 POST 携带凭据（优先 Lax，必要时 None + CSRF 补偿）。
- **High（认证/RLS）**
  - `/oauth/logout` 的 `id_token_hint` 必须验签并校验 `iss/aud/exp` 后才允许吊销；未认证路径不得执行吊销。
  - `CreateCLIAuthCode` 增加 `RequireSecurityProof(VerificationScopeAccessTokenGenerate)`，前端授权页走既有安全验证流程。
  - OAuth 应用模型白名单校验下沉到 `TokenAuth`/OAuth 中间件，覆盖 `RelayTask`、视频与任务插件入口。
  - 校验 OAuth access token 时要求 app 存在且启用，失败即关闭；应用创建/更新/删除后调用 `InvalidateOAuthAppCache`，禁用/删除时吊销关联令牌。
  - `user_profiles` 的 RLS 改为客户端只读，写入仅 service_role/Edge Function；`gateway_user_id` 不可由用户改写。
  - `seed_outfit_demo` 撤销 PUBLIC/anon/authenticated 执行权限；`app_config` 收敛为单行（`id=1`）。
- **High（CLI 健壮性）**
  - 非 loopback 地址强制 HTTPS；`CheckRedirect` 拒绝 scheme 降级与跨 origin 重定向。
  - 管理 API 的 2xx 响应必须是合法 JSON 信封，否则按错误返回；响应体超限需显式报错。
- **High（治理）**
  - 恢复官方镜像名与 cosign 签名流程（`docker-compose.yml`、`.github/workflows/*`）。
  - 还原 7 个 locale 中被转义的项目标识键 `footer.newapi.projectAttributionSuffix`。
- **Medium（选摘）**
  - refresh token 轮换改为原子条件更新并处理重放（吊销整条链）。
  - 修复 OAuth 路径 `token_id=0` 导致的 billing 端点 nil 解引用。
  - `POST /api/token/` 增加 `middleware.DisableCache()`。
  - CLI `--quota` 在 unlimited 下显式报错或自动切换；`--expires-in` 拒绝 `<=0`。
  - `--pretty` 列取所有行并集、数字保留原始字面量、CJK/emoji 按显示宽度对齐、标量数组回退。
  - CLI 配置文件拒绝符号链接、收敛目录权限、原子写入（Windows 说明为 best-effort）。
  - 前端 `oauth-apps` 校验 `success` 与错误态；`consent` 路由补 `validateSearch`；CLI 授权默认不勾选高危写权限并展示最终 scope 摘要。
  - 模板补 `ai_generations` 插入必需字段与错误检查、修正额度查询端点/凭据、补齐 openapi/SDK 与 `.gitignore`。

## Impact

- Affected specs：OAuth Provider、Supabase App Starter、newapi-cli、CLI 授权页
- Affected code：
  - `controller/oauth_authorize.go`、`controller/oauth_userinfo.go`、`controller/cli_auth.go`、`controller/billing.go`、`controller/oauth_app.go`、`controller/relay.go`
  - `middleware/oauth_token_auth.go`、`middleware/auth.go`、`service/oauth_token.go`、`service/oauth_app_policy.go`、`service/auth_session.go`、`model/oauth_refresh_token.go`、`model/main.go`
  - `cmd/newapi-cli/internal/{client,commands,config,output}`
  - `web/src/features/cli-auth`、`web/src/features/oauth-apps`、`web/src/routes/oauth/consent.tsx`、7 个 locale
  - `templates/supabase-app-starter/**`、`skills/new-api-oauth-integration/references/web/outfit-demo/**`、`sdk/**`
  - `docker-compose.yml`、`.github/workflows/docker-build.yml`、`.github/workflows/docker-image-branch.yml`
- 不改变计费公式与 OAuth 协议成功路径的对外语义；仅增加安全校验与错误分支。
- 不触碰工作区中与本 Spec 无关的其他未提交改动。

## ADDED Requirements

### Requirement: 同意提交必须服务端校验 scope
同意页 POST SHALL 与服务端保存的授权请求一致，并对每个 scope 执行格式校验与 `ScopeAllowed` 校验。

#### Scenario: 客户端提交未获准 scope
- **WHEN** 应用注册 scope 为 `openid`，却 POST `scope=api:write`
- **THEN** 返回 `invalid_scope` 错误重定向，不签发授权码，也不写入同意记录

### Requirement: 同意提交必须防 CSRF
同意 POST SHALL 校验与会话绑定的 CSRF token 以及 Origin / Sec-Fetch-Site；缺失或不匹配时拒绝。

#### Scenario: 跨站伪造同意
- **WHEN** 攻击者从第三方站点自动提交同意表单
- **THEN** 请求被拒绝，用户未被授权，未签发授权码

### Requirement: user-sync 只信任已认证身份
Edge Function SHALL 从 `Authorization` JWT 解析用户，仅允许操作该用户；`gateway_user_id` 必须来自经网关校验的数据。

#### Scenario: 伪造 body 覆盖他人档案
- **WHEN** 调用者 POST 受害者 `user.id`
- **THEN** 返回 401/403，`user_profiles` 不被修改

### Requirement: OAuth 所有 relay 入口统一应用模型白名单
`TokenAuth` 覆盖的 relay 入口（含任务、视频、插件）SHALL 统一执行 OAuthApp 模型白名单。

#### Scenario: 任务入口绕过白名单
- **WHEN** 受限应用通过任务接口请求被禁模型
- **THEN** 请求被拒绝

### Requirement: 应用禁用/删除即时失效
OAuth access token 校验 SHALL 要求 app 存在且启用；禁用/删除 SHALL 使缓存与关联令牌失效。

#### Scenario: 禁用后继续调用
- **WHEN** 管理员禁用应用后使用未过期 access token
- **THEN** 请求被拒绝

## MODIFIED Requirements

### Requirement: OAuth logout
`/oauth/logout` SHALL 仅基于已验签的 `id_token_hint`（或客户端认证 + 用户会话）确定用户并吊销 refresh token；未认证请求不得触发吊销。

### Requirement: CLI 登录与凭据传输
CLI SHALL 对非 loopback 地址强制 HTTPS，拒绝 scheme 降级/跨 origin 重定向；配置写入 SHALL 拒绝符号链接并原子替换。

### Requirement: CLI 输出契约
CLI SHALL 将 2xx 非 JSON 信封视为错误；`--pretty` SHALL 保留完整列、原始数字字面量与可读对齐，且不改变默认 JSON 输出。

### Requirement: Supabase 模板 RLS 与 seed
`user_profiles` 客户端策略 SHALL 为只读；`app_config` SHALL 为单行表；`seed_outfit_demo` SHALL 仅 service_role 可执行。

## REMOVED Requirements

### Requirement: 个人镜像仓库与未签名发布
**Reason**: 违反项目治理中 Docker 镜像标识与供应链可验证性要求。
**Migration**: 恢复 `calciumion/new-api` 与 cosign 签名；私有发布另建 workflow 或 compose overlay，不作为默认配置。
