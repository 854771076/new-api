# OAuth 2.0 授权服务 - 实现计划

> **进度说明（本次更新）**：Task 1-11 已完成实现并通过构建；Task 12 的限速、state/PKCE/重放/跨应用校验、审计日志（授权、token 签发、刷新、撤销、退出、可归属失败）已实现。已验证：`go build ./...`、`go test ./service/ ./model/ ./router/`、`bun run build` 均通过；SQLite 运行时完成 authorize→token→userinfo→refresh→replay→revoke→logout 全链路验证。遗留：`go test ./controller/` 中的失败均为 Windows 下测试清理无法删除仍被 logDB 句柄占用的 `audit.db`（非断言失败，属既有环境问题），MySQL/PostgreSQL 迁移矩阵因无实例未执行。

## Task 1: 数据模型与迁移
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None
- **Description**:
  - 新增 `model/oauth_app.go`：`OAuthApp` 模型（name, client_id, client_secret_hash, redirect_uris, post_logout_redirect_uris, scopes, status, created_by）
  - 新增 `model/oauth_authorization_code.go`：`OAuthAuthorizationCode` 模型（code_hash, app_id, user_id, session_id, scope, redirect_uri, code_challenge, code_challenge_method, expires_at, consumed_at）
  - 新增 `model/oauth_refresh_token.go`：`OAuthRefreshToken` 模型（token_hash, app_id, user_id, session_id, scope, expires_at, revoked_at, prev_token_hash）
  - 新增 `model/oauth_consent.go`：`OAuthConsent` 模型（app_id, user_id, scope, granted_at）
  - 在 `model/main.go` 的 `migrateDB()` AutoMigrate 列表中注册四个新模型
  - 实现各模型的 CRUD 函数（Create/Get/Update/Delete/List）
- **Acceptance Criteria Addressed**: AC-1, AC-10
- **Test Requirements**:
  - `rule` TR-1.1: 四个新表 AutoMigrate 在 SQLite/MySQL/PG 上均成功创建
  - `rule` TR-1.2: client_id 唯一索引生效，重复创建返回错误
  - `rule` TR-1.3: 授权码 code_hash 唯一索引 + consumed_at 原子更新保证单次使用
- **Notes**: 遵循现有 model 层模式（lockForUpdate、事务、三库兼容）

## Task 2: OAuth 应用管理 API（管理员后台）
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1
- **Description**:
  - 新增 `controller/oauth_app.go`：App CRUD 控制器
  - `POST /api/oauth-apps`：创建应用，生成 client_id（`nac_` 前缀）+ client_secret（明文返回一次，存哈希）
  - `GET /api/oauth-apps`：列表（不返回 secret）
  - `GET /api/oauth-apps/:id`：详情
  - `PUT /api/oauth-apps/:id`：更新名称/回调/scopes/status
  - `POST /api/oauth-apps/:id/rotate-secret`：重置 client_secret
  - `DELETE /api/oauth-apps/:id`：删除（级联清理 code/refresh_token/consent）
  - 新增 `router/oauth-app-router.go` 挂载到 `apiRouter`（AdminAuth）
- **Acceptance Criteria Addressed**: AC-1, AC-10
- **Test Requirements**:
  - `rule` TR-2.1: 创建响应包含 client_secret 明文，后续 GET 不包含
  - `rule` TR-2.2: 非管理员请求返回 403
  - `rule` TR-2.3: 停用应用后其 authorize/token 请求返回 invalid_client
  - `rule` TR-2.4: rotate-secret 后旧 secret 失效，新 secret 生效
- **Notes**: client_secret 生成 32 字节随机，base64 编码；哈希用 SHA-256

## Task 3: 授权端点 `/oauth/authorize`
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1, Task 2
- **Description**:
  - 新增 `controller/oauth_authorize.go`
  - 解析参数：response_type, client_id, redirect_uri, scope, state, code_challenge, code_challenge_method
  - 校验 client_id 存在且启用
  - 校验 redirect_uri 精确匹配应用配置
  - 校验 scope 是应用允许的子集
  - 校验用户已登录（复用 `UserSession`），未登录则重定向登录页（带 return_to）
  - 检查 OAuthConsent，未同意则渲染授权同意页
  - 生成授权码（32 字节随机，存哈希，60s 过期），绑定 client_id + redirect_uri + code_challenge
  - 重定向到 redirect_uri?code=...&state=...
- **Acceptance Criteria Addressed**: AC-2, AC-3, AC-4
- **Test Requirements**:
  - `rule` TR-3.1: 未登录用户被重定向到登录页
  - `rule` TR-3.2: 非精确匹配 redirect_uri 返回 invalid_request
  - `rule` TR-3.3: 超出应用允许 scope 的请求被拒绝
  - `rule` TR-3.4: 授权码 60s 后过期
  - `rule` TR-3.5: state 参数原样透传
- **Notes**: 登录后回跳使用现有 session 体系

## Task 4: 授权同意页
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: Task 3
- **Description**:
  - 新增前端页面 `web/src/routes/oauth/authorize.tsx`
  - 展示应用名称、请求的 scope 列表及中文说明
  - "同意"按钮：POST 同意，创建 OAuthConsent，重新走 authorize 流程
  - "拒绝"按钮：重定向 redirect_uri?error=access_denied&state=...
  - 已同意的 scope 组合跳过同意页
- **Acceptance Criteria Addressed**: AC-2
- **Test Requirements**:
  - `rule` TR-4.1: 首次授权展示同意页，二次同 scope 跳过
  - `rule` TR-4.2: scope 变更时重新展示同意页
  - `rule` TR-4.3: 拒绝后外部系统收到 access_denied 错误
- **Notes**: i18n 七语言

## Task 5: Token 端点 `/oauth/token`
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1, Task 3
- **Description**:
  - 新增 `controller/oauth_token.go`
  - `grant_type=authorization_code`：
    - 校验 client 认证（client_id + client_secret，Basic Auth 或 body）
    - 校验授权码：存在、未消费、未过期、client_id 匹配、redirect_uri 匹配
    - PKCE 校验：code_verifier 哈希后与 code_challenge 比对（S256）
    - 原子消费授权码（UPDATE consumed_at）
    - 签发 access_token(JWT) + id_token(JWT) + refresh_token(opaque)
    - 存储 refresh_token 哈希
  - `grant_type=refresh_token`：
    - 校验 refresh_token 哈希存在、未过期、未吊销
    - 轮换：签发新 refresh_token，旧的标记 revoked，记录 prev_token_hash
    - 重放检测：使用已吊销的旧 token 时，吊销整条链
  - 错误响应遵循 RFC 6749
- **Acceptance Criteria Addressed**: AC-2, AC-3, AC-5, AC-9
- **Test Requirements**:
  - `rule` TR-5.1: 正确 code_verifier 通过，错误的返回 invalid_grant
  - `rule` TR-5.2: 同一 code 第二次使用返回 invalid_grant
  - `rule` TR-5.3: refresh_token 轮换后旧的失效
  - `rule` TR-5.4: 错误的 client_secret 返回 invalid_client
  - `rule` TR-5.5: JWT access_token 可通过 JWKS 验签
- **Notes**: JWT 签名用 RS256 或 HS256（见 Open Question）

## Task 6: UserInfo / Introspect / Revoke 端点
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 5
- **Description**:
  - `GET /oauth/userinfo`：
    - 解析 Authorization: Bearer access_token
    - 验证 JWT 签名 + 用户状态 + auth_version
    - 按 scope 返回 sub/username/display_name/email
  - `POST /oauth/introspect`：
    - client 认证（client_id + client_secret）
    - 验证 token（access_token JWT 或 refresh_token 查库）
    - 返回 active/scope/client_id/sub/exp/iat
  - `POST /oauth/revoke`：
    - client 认证
    - 撤销指定 refresh_token（UPDATE revoked_at）
- **Acceptance Criteria Addressed**: AC-6, AC-7
- **Test Requirements**:
  - `rule` TR-6.1: 有效 access_token 返回正确用户信息
  - `rule` TR-6.2: 过期/无效 access_token 返回 401
  - `rule` TR-6.3: 用户登出（auth_version 递增）后 access_token 失效
  - `rule` TR-6.4: introspect 需 client 认证，无认证返回 401
  - `rule` TR-6.5: revoke 后 refresh_token 不可再用
- **Notes**: 用户状态走 Redis 缓存（`GetUserSessionCached` 模式）

## Task 7: OIDC Discovery 与 JWKS
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: Task 5
- **Description**:
  - `GET /.well-known/openid-configuration`：返回 issuer、authorization_endpoint、token_endpoint、userinfo_endpoint、jwks_uri、scopes_supported、response_types_supported=["code"]、grant_types_supported、code_challenge_methods_supported=["S256"]
  - `GET /oauth/jwks`：返回签名公钥（RS256 时返回 n/e；HS256 时不暴露密钥，仅返回 kid）
  - 生成/加载签名密钥对（启动时生成或从配置加载）
- **Acceptance Criteria Addressed**: AC-2
- **Test Requirements**:
  - `rule` TR-7.1: discovery 端点返回符合 OIDC 规范的 JSON
  - `rule` TR-7.2: JWKS 公钥可成功验证 access_token 签名
- **Notes**: 决定签名算法后实现；RS256 需管理密钥对

## Task 8: access_token 集成 Relay API
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 5
- **Description**:
  - 扩展 `middleware/auth.go` 的 `TokenAuth`：
    - 在 Authorization 头中识别 OAuth JWT access_token（issuer + audience 判断）
    - 验证 JWT 签名 + 用户状态 + auth_version + scope
    - scope 含 `api:write` 可读写，仅 `api:read` 只读
    - 注入用户身份到 context（复用现有 TokenAuth 注入逻辑）
  - 计费归属到 access_token 对应的用户（复用现有 quota 链路）
- **Acceptance Criteria Addressed**: AC-8
- **Test Requirements**:
  - `rule` TR-8.1: 含 api:write scope 的 access_token 可调用 /v1/chat/completions
  - `rule` TR-8.2: 仅 api:read scope 的 access_token 调用写接口返回 403
  - `rule` TR-8.3: 调用后用户 quota 正确扣减
  - `rule` TR-8.4: 失效 access_token 返回 401
- **Notes**: 不改动 sk- token 现有路径

## Task 9: 登出联动
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: Task 5, Task 6
- **Description**:
  - `GET/POST /oauth/logout`：
    - 接收 id_token_hint 或 access_token
    - 撤销该用户在该应用下的所有 refresh_token
    - 可选：同时使关联的 UserSession 失效
    - 重定向到 post_logout_redirect_uri（白名单校验）
  - 用户在 dashboard 登出时，联动撤销其所有 OAuth refresh_token（在 `service/auth_session.go` 登出逻辑中添加）
- **Acceptance Criteria Addressed**: AC-7
- **Test Requirements**:
  - `rule` TR-9.1: /oauth/logout 后该应用的 refresh_token 全部失效
  - `rule` TR-9.2: 非白名单 post_logout_redirect_uri 被拒绝
  - `rule` TR-9.3: dashboard 登出后所有 OAuth token 失效
- **Notes**: auth_version 递增使 access_token 自然失效

## Task 9.5: Cookie SameSite 跨站适配
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 3
- **Description**:
  - OAuth 授权流程中，用户从外部系统跨站跳转到 new-api `/oauth/authorize`，需携带 new-api 登录态 cookie
  - 将 `new_api_refresh` cookie 的 `SameSite` 属性调整为 `None`（仅当 `Secure=true` 时）
  - 现有 `common.SessionCookieSecure` 为 true 时生效；开发环境（HTTP）保持 `SameSite=Lax` 兼容
  - 确保 session hint cookie `new_api_has_session` 同步适配
  - 不影响现有 dashboard 同站登录体验
- **Acceptance Criteria Addressed**: AC-2
- **Test Requirements**:
  - `rule` TR-9.5.1: HTTPS 环境下 refresh cookie 携带 `SameSite=None; Secure`
  - `rule` TR-9.5.2: HTTP 开发环境下 cookie 仍为 `SameSite=Lax`（兼容）
  - `rule` TR-9.5.3: 从外部系统跨站跳转 /oauth/authorize 时能识别已登录状态
- **Notes**: 需修改 `service/auth_session.go` 的 `WriteRefreshCookie`

## Task 10: 管理后台前端页面
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: Task 2
- **Description**:
  - 新增 `web/src/routes/admin/oauth-apps/` 页面：
    - 应用列表（名称、client_id、状态、创建时间）
    - 创建应用弹窗（名称、回调地址、登出回调地址、scopes 多选）
    - 应用详情/编辑
    - 重置 secret（明文弹窗展示一次）
    - 启用/停用
  - 复用现有 Table、Dialog、CopyButton 等组件
  - i18n 七语言
- **Acceptance Criteria Addressed**: AC-1
- **Test Requirements**:
  - `rule` TR-10.1: 创建应用后弹窗显示 client_secret 明文且仅一次
  - `rule` TR-10.2: scopes 多选组件正确提交
  - `rule` TR-10.3: 所有文案有对应 i18n key
- **Notes**: 遵循 web/AGENTS.md 组件复用规则

## Task 11: SDK 与集成 Skill
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: Task 5, Task 6, Task 7
- **Description**:
  - `sdk/openapi.yaml`：覆盖所有 OAuth 端点的 OpenAPI 3.1 描述
  - `sdk/typescript/`：零依赖 TypeScript SDK
    - `createOAuthClient({baseUrl, clientId, clientSecret, redirectUri})`
    - `buildAuthorizeUrl({scope, state, codeChallenge})`
    - `exchangeCode(code, codeVerifier)`
    - `refreshToken(refreshToken)`
    - `userinfo(accessToken)`
    - `introspect(token)`
    - `revoke(token)`
    - `logout({idTokenHint, postLogoutRedirectUri})`
  - `skills/new-api-oauth-integration/SKILL.md`：集成指南
- **Acceptance Criteria Addressed**: AC-11
- **Test Requirements**:
  - `rubric` TR-11.1: SDK 完整性；scale 1-5；anchors 1=仅URL, 3=核心端点, 5=全部端点+类型+示例；threshold >= 4；evidence: SDK 源码
  - `rule` TR-11.2: SDK 示例可完成 authorize→token→userinfo 闭环
- **Notes**: SDK 不依赖 new-api 内部包，纯 fetch 封装

## Task 12: 安全测试与审计
- **Status**: `in_progress`
- **Priority**: high
- **Depends On**: Task 1-9
- **Description**:
  - OWASP ASVS V2/V3/V4 对照测试：
    - 授权码暴力破解防护（限速）
    - state CSRF 防护
    - redirect_uri 注入
    - PKCE 绕过
    - token 重放
    - 跨应用越权
    - secret 泄露检查（日志、响应）
  - 审计日志覆盖：授权、token 签发、撤销、失败
  - 补充安全回归测试
- **Acceptance Criteria Addressed**: AC-12
- **Test Requirements**:
  - `rubric` TR-12.1: OWASP 合规性；scale 1-5；anchors 1=无控制, 3=基本PKCE+state, 5=全ASVS覆盖+测试；threshold >= 4；evidence: 测试报告
  - `rule` TR-12.2: 所有 OAuth 相关日志不含 secret/code/token 明文
  - `rule` TR-12.3: 授权端点有速率限制
- **Notes**: 记录 OWASP ASVS 版本与 requirement ID
  - 审计日志已覆盖 `oauth.authorize`、`oauth.token`、`oauth.token.refresh`、`oauth.revoke`、`oauth.logout`，以及可归属到用户的 `oauth.token.failure`（PKCE 不匹配、refresh 失效/重放/跨应用）；仅记录 client_id、grant_type、scope、error，不含 code/token/secret。客户端认证失败不写审计（由速率限制覆盖），避免匿名请求产生 role=0 日志噪声。
  - 回归测试：`model/oauth_security_test.go`（PKCE、redirect_uri 精确匹配、scope、code 单次消费、过期、跨应用、refresh 轮换与重放链撤销、应用隔离）、`service/oauth_token_test.go`（auth_version 失效）。
  - 运行时证据：SQLite 下 Discovery/JWKS、PKCE 授权码、id_token 的 iss/aud/nonce/sub、refresh 轮换、旧 token 重放返回 `invalid_grant` 且新 refresh 失效、revoke/logout 审计。
  - 未完成：OWASP ASVS V2/V3/V4 逐条 requirement ID 对照报告；MySQL/PostgreSQL 迁移验证（`oauth_authorization_codes.nonce` 新增列）因环境无实例阻塞。
