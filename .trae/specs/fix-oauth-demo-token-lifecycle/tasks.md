# 实现任务队列 — OAuth Demo 令牌生命周期修复

**本队列是 `.trae/specs/fix-oauth-demo-token-lifecycle/spec.md` 的实现计划。**

---

## [x] Task 1：后端支持公共 OAuth 客户端（public_client）

**目标 AC**：公共客户端认证、发现文档、现有应用不回归
**优先级**：high
**前置**：无

### 实现

1. **`model/oauth_app.go`**
   - 新增字段：
     ```go
     PublicClient bool `json:"public_client" gorm:"column:public_client"`
     ```
   - `UpdateOAuthApp` 增加 `publicClient *bool` 参数（nil 表示不修改）。
2. **`controller/oauth_app.go`**
   - `OAuthAppResponse` 增加 `PublicClient bool`；
   - `CreateOAuthAppRequest` 增加 `PublicClient bool`；
   - `UpdateOAuthAppRequest` 增加 `PublicClient *bool`；
   - Create/Update 透传并写库。
3. **`controller/oauth_token.go`**
   - 把 `authenticateOAuthClient` 拆成两步：先解析候选 `client_id`/`client_secret`（Basic 或 body），再在拿到 `app` 后判定：
     - `app.PublicClient == true`：只要求 `client_id` 匹配，忽略 secret；
     - 否则：保持现有 secret 校验，缺失或错误 → `401 invalid_client`。
   - 公共客户端在 `authorization_code` 授权类型下仍必须提供 `code_verifier`（现有逻辑不变）。
   - 刷新令牌的 client 绑定校验（`existing.AppID != app.Id`）与轮换逻辑保持不变。
4. **`controller/oauth_userinfo.go`（OAuthRevoke）**
   - 使用同一认证辅助函数，公共客户端可无 secret 吊销。
5. **`controller/oauth_discovery.go`**
   - `token_endpoint_auth_methods_supported` 增加 `"none"`。
6. **后台 UI（`web/src/features/oauth-apps/`）**
   - 创建/编辑表单增加「公共客户端（PKCE，无 client secret）」开关；
   - 列表展示该标记；
   - 复用现有表单/开关组件，7 个 locale 文件补 key；
   - 创建公共客户端时不再强制展示一次性 secret（提示「公共客户端无 secret」）。

### 测试

- **rule**：公共客户端 + `refresh_token` + 仅 `client_id` → 200 且 refresh token 轮换。
- **rule**：机密客户端无 secret / 错误 secret → 401（回归）。
- **rule**：公共客户端授权码兑换缺 `code_verifier` → 400。
- **rule**：公共客户端吊销刷新令牌仅凭 `client_id` → 200，令牌失效。
- **rule**：discovery 含 `none`。
- **rule**：`go test ./controller/... ./model/...` 通过；`gofmt` 清理。
- **rule**：SQLite / MySQL 5.7.8 / PostgreSQL 9.6 三库各启动两次，第二次无 `ALTER TABLE`；旧库升级后 `public_client` 默认 false，现有应用行为不变。

---

## [x] Task 2：参考应用接入网关令牌自动刷新

**目标 AC**：过期前刷新、401 重试一次、刷新失败登出
**优先级**：high
**前置**：Task 1 完成

### 实现

1. **新增 `src/lib/gatewayAuth.ts`**
   - 状态：`accessToken`、`refreshToken`、`expiresAt`（从 JWT payload 的 `exp` 解析，不校验签名）；
   - `seedFromSession(session)`：读取 `provider_token` / `provider_refresh_token`；
   - 订阅 `supabase.auth.onAuthStateChange`，`SIGNED_IN` / `TOKEN_REFRESHED` 时重新播种，`SIGNED_OUT` 时清空；
   - `getAccessToken()`：剩余 <60s 时先刷新；
   - `refreshGatewayToken()`：`POST ${GATEWAY_BASE_URL}/oauth/token`，`grant_type=refresh_token&client_id=...&refresh_token=...`（不带 secret）；
   - 刷新失败区分 `invalid_grant`（登出）与其他错误（抛错）；
   - 令牌只存内存 + `sessionStorage`（按 Supabase user id 分键），不写 `user_metadata`。
2. **`src/lib/gateway.ts`**
   - 所有网关请求改用 `getAccessToken()`；
   - AI 请求收到 401 时刷新并重试一次；
   - 删除 `getGatewayQuota()`（OAuth token 无法调用 Dashboard 接口）。
3. **`src/pages/AuthCallback.tsx`**
   - 删除 `updateUser({ data: ... })`；
   - 只做 `exchangeCodeForSession`，然后触发 `seedFromSession`。
4. **`src/pages/Profile.tsx`**
   - 网关用户 ID 取 `user.identities[0]?.identity_data?.sub`；
   - 去掉额度数值展示，改为「在网关查看额度 / 充值」链接。
5. **`src/pages/Profile.tsx` 登出**
   - 先用 `provider_refresh_token` 调 `${GATEWAY_BASE_URL}/oauth/revoke`（`token=...&client_id=...`），忽略失败；
   - 再 `supabase.auth.signOut()`。

### 测试

- **rule**：用一个真实 refresh token 直接调刷新接口 → 200，新 access token 可访问 `/v1/models`。
- **rule**：手工把 `expiresAt` 设为 10 秒后 → `getAccessToken()` 触发刷新且只发一次请求。
- **rule**：模拟 401（错误 token）→ 刷新后重试一次成功；refresh token 无效时不无限重试。
- **rule**：`bun run build` 通过。

---

## [x] Task 3：修复并接入 user-sync Edge Function

**目标 AC**：函数可部署、失败不阻断登录
**优先级**：medium
**前置**：无（可与 Task 2 并行）

### 实现

1. 把 `supabase/functions/user-sync/index.ts` 文件头的 `--` SQL 注释改为合法 TS 注释（或删除）。
2. 保留 `verify_jwt` 默认行为；用调用者 JWT 写入 `user_profiles` 自身行（依赖现有 RLS）。
3. 参考应用登录成功后调用 `supabase.functions.invoke('user-sync')`，用 `try/catch` 包裹，失败仅 `console.warn`。

### 测试

- **rule**：`deno check`（或 `supabase functions serve` 启动）无语法/类型错误。
- **rule**：未部署函数时登录流程仍成功进入首页。

---

## [x] Task 4：skill 文档修正 + 环境初始化自检脚本

**目标 AC**：Step 0、自检脚本、文档与实现一致
**优先级**：high
**前置**：无（可与 Task 1/2 并行）

### 实现

1. 新增 `skills/new-api-oauth-integration/scripts/check-env.ps1` 与 `check-env.sh`：
   - 检查 `bun`、`go`、`supabase`、`cloudflared`、`newapi-cli` 是否存在并打印版本；
   - 读取 `GATEWAY_BASE_URL`、`GATEWAY_PUBLIC_URL`、`SUPABASE_URL`、`SUPABASE_SECRET_KEY`（可用环境变量或参数）；
   - 校验 `discovery.issuer == GATEWAY_PUBLIC_URL`；
   - 校验 Supabase provider `custom:newapi` 存在且 `issuer` 一致；
   - 请求 `authorize?provider=custom:newapi`，非 302 视为失败；
   - 任一项失败 → 打印修复提示，退出码 1。
2. `SKILL.md`：
   - 新增「Step 0：环境初始化与自检」，列安装命令、`supabase login/link`、`functions deploy`、脚本用法；
   - 修正 provider 标识为 `custom:newapi`，说明必须带 `custom:` 前缀并用 Auth Admin API 创建；
   - 修正 `templates/supabase-app-starter/` 为 `references/web/outfit-demo/`；
   - 参考应用配置改为公共客户端（无 secret），与 Task 1 一致；
   - 新增「令牌生命周期与长任务」章节（1h、3s/8s、150s/400s、两种推荐架构）；
   - 引用配套 `newapi-cli` skill 创建 OAuth App 的方式。
3. `references/web/outfit-demo/README.md`：
   - 修正目录路径与页面名；
   - 验证清单加入「超过 1 小时后仍可调用 AI（自动刷新）」；
   - 补充 `supabase functions deploy user-sync` 步骤。
4. `.env.example` 增补公共客户端与刷新说明。

### 测试

- **rule**：脚本在依赖齐全环境下退出码 0；临时改错 issuer 后退出码 1 且提示明确。
- **rule**：文档内不再出现 `templates/supabase-app-starter` 与裸 `new-api` provider 标识。

---

## [x] Task 5：重配线上 Supabase provider 并做端到端验收

**目标 AC**：公共客户端 + 刷新链路真实验证
**优先级**：high
**前置**：Task 1、Task 2 完成

### 实现

1. 把参考应用对应的 OAuth App 置为 `public_client=true`。
2. 用 Auth Admin API 更新 `custom:newapi`：`client_secret` 使用占位值（Supabase 管理接口强制要求非空，网关对公共客户端忽略该值），保留 `pkce_enabled=true`、`email_optional=true`。
3. 重启本地网关，确认 discovery 含 `none`。
4. 走完整链路：Supabase authorize → 网关登录/同意 → 回调 → 获取 `provider_token`/`provider_refresh_token`。
5. 用 refresh token 刷新，验证新 access token 可调用 `/v1/chat/completions`。

### 测试

- **rule**：`POST /oauth/token`（仅 client_id + refresh_token）返回 200。
- **rule**：刷新后的 token 调 relay 返回 200；旧 access token 仍可用至过期，旧 refresh token 失效。
- **rule**：`bun run dev` 下 demo 登录、生成、历史、登出全流程通过。

---

## [x] Task 6：open-code-review 审计与修复

**目标 AC**：审计无高危问题、核心项闭环
**优先级**：medium
**前置**：Task 1–5 完成

### 实现

1. 按 `open-code-review` skill 对本次变更（含未提交 diff）执行审计。
2. 修复审计发现的正确性 / 安全问题（尤其公共客户端绕过、令牌泄漏、重试风暴）。
3. 重新跑相关测试与构建。

### 测试

- **rule**：审计报告中的高危 / 中危问题全部修复或有明确结论。
- **rule**：`go build ./...`、相关 `go test`、`bun run build` 通过。

---

## 依赖拓扑

```
Task 1 (后端公共客户端) ─┬─> Task 2 (前端刷新) ─┬─> Task 5 (线上重配 + E2E) ─> Task 6 (审计)
                         │                      │
Task 3 (user-sync) ──────┴──────────────────────┘
Task 4 (skill 文档/脚本) ─────────────────────────┘
```

## 显式排除

- 引入 BFF 或自建后端代理；
- 修改网关 access token TTL（仍为 1 小时）；
- 后台任务表 / Realtime 的长任务实现（仅写文档建议）；
- 小程序端实现。
