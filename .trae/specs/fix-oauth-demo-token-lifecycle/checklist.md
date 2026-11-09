# 验收清单 — OAuth Demo 令牌生命周期修复

## 后端：公共客户端

- [x] `oauth_apps` 新增 `public_client` 字段，默认 false，旧数据行为不变
- [x] 公共客户端仅凭 `client_id` 可调用 `/oauth/token` 的 `refresh_token` 与 `authorization_code`
- [x] 公共客户端仍强制 PKCE（缺 `code_verifier` 返回 400）
- [x] 机密客户端缺失/错误 secret 仍返回 401 `invalid_client`
- [x] `/oauth/revoke` 对公共客户端同样免 secret，且只能吊销本应用令牌
- [x] `/oauth/introspect` 与 `/oauth/revoke` 使用同一认证判定（审计修复）
- [x] discovery 的 `token_endpoint_auth_methods_supported` 含 `none`
- [x] 管理 API 可创建/更新 `public_client`，后台 OAuth Apps 页面可配置并展示
- [x] 新增 UI 文案在 7 个 locale 文件中齐全（en/fr/ja/ru/vi/zh/zh-TW，missing=0）
- [x] 相关 Go 测试覆盖成功、失败、绕过、吊销场景（`TestCreateOAuthAppPersistsPublicClient`、`TestUpdateOAuthAppToPublicClientClearsSecret` 等通过）

## 数据库

- [x] SQLite / MySQL 8.0 / PostgreSQL 16 各启动两次，第二次无 ALTER（MySQL 5.7.8、PostgreSQL 9.6 镜像不可拉取；改动为普通 boolean 列，非版本特定）
- [x] 旧库升级后 `public_client` 为 false（MySQL 旧行为 NULL，Go 读作 false），现有授权与刷新流程正常

## 参考应用：令牌刷新

- [x] 令牌管理器从 `provider_token` 解析过期时间，剩余 <60s 主动刷新
- [x] 网关 401 时刷新并重试一次，不出现无限重试
- [x] refresh token 失效时清理本地状态、登出并提示重新登录
- [x] 令牌不写入 `user_metadata`
- [x] 刷新请求不携带 `client_secret`
- [x] 登出时调用 `/oauth/revoke` 吊销网关 refresh token，且使用本地轮换后的当前 refresh token（审计修复，避免吊销已轮换的旧值）
- [x] 个人中心不再调用 Dashboard 额度接口，改为跳转链接
- [x] 网关用户 ID 来自 `identity_data.sub`

## Edge Function

- [x] `user-sync/index.ts` 为合法 TypeScript（无 SQL 注释残留），可部署
- [x] 函数未部署或报错时登录流程不中断（`try/catch` + `console.warn`）

## skill 文档与脚本

- [x] 新增 `scripts/check-env.ps1` 与 `check-env.sh`；缺依赖时退出码 1 并给出安装提示（实测）
- [x] issuer 不一致、provider 缺失、authorize 非 302 时累加失败并以非 0 退出（代码路径核验）
- [x] SKILL.md 含 Step 0 初始化、`custom:newapi` 标识、Auth Admin API 创建方式
- [x] 文档中的模板/参考路径均指向真实存在的目录（`templates/supabase-app-starter/` 与 `references/web/outfit-demo/` 均存在）
- [x] SKILL.md 含令牌生命周期与长任务限制章节（1h / 3s / 8s / 150s / 400s）
- [x] `.agents/skills` 与 `skills` 两份 SKILL.md 已同步一致
- [x] demo README 路径、页面名、验证清单已更新
- [x] `.env.example` 说明公共客户端与刷新行为

## 端到端

- [x] Supabase `custom:newapi` 使用占位 `client_secret`（管理接口强制非空，网关对公共客户端忽略），`authorize` 返回 302
- [x] 登录回调后可获得 `provider_token` 与 `provider_refresh_token`
- [x] 仅用 `client_id + refresh_token` 刷新成功，新 token 可调用 relay（`/v1/models` 200）
- [x] 旧 refresh token 复用返回 400 `invalid_grant`，`/oauth/revoke` 返回 200
- [x] 参考应用页面流程由构建/类型检查覆盖（`bun run build`、`bun run typecheck` 通过），浏览器 UI 未做人工回归
- [x] `bun run build`、`go build ./...`、相关 `go test` 通过（仅 Windows 临时目录清理导致的既有 Telegram/GitHub 绑定测试失败，与本次改动无关）

## 审计

- [x] open-code-review 已执行；`ocr` 因未配置 LLM 端点无法运行（`no valid LLM endpoint configured`），按 AGENTS.md 改为人工基线 diff 审计
- [x] 审计发现的中危问题已修复：登出吊销使用轮换后的 refresh token、`/oauth/introspect` 支持公共客户端、两份 SKILL.md 同步
