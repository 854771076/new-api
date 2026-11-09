# Tasks

- [x] Task 1: OAuth 同意与登出链路加固
  - [x] SubTask 1.1: `OAuthAuthorizeConsent` 复用 `IsValidOAuthScope` 与 `app.ScopeAllowed`，非法 scope 返回 `invalid_scope`
  - [x] SubTask 1.2: 同意 POST 增加会话绑定 CSRF token 与 Origin / Sec-Fetch-Site 校验（后端校验 + `web/src/routes/oauth/consent.tsx` 表单携带）
  - [x] SubTask 1.3: 调整会话 Cookie SameSite（优先 Lax；使用 None 时必须配合 CSRF token），确认不影响跨站顶层 GET 授权流程
  - [x] SubTask 1.4: `/oauth/logout` 对 `id_token_hint` 验签并校验 `iss/aud/exp`；未认证路径不执行吊销
  - [x] SubTask 1.5: 回归测试：篡改 scope、跨站 POST、伪造 id_token_hint 均被拒；正常同意/登出仍成功

- [x] Task 2: CLI 授权码二次验证
  - [x] SubTask 2.1: `CreateCLIAuthCode` 增加 `RequireSecurityProof(VerificationScopeAccessTokenGenerate)`
  - [x] SubTask 2.2: `web/src/features/cli-auth` 在生成授权码前调用既有安全验证流程
  - [x] SubTask 2.3: 测试：无 proof 返回既有 step-up 错误码；有 proof 成功签发

- [x] Task 3: OAuth 应用生命周期与模型白名单
  - [x] SubTask 3.1: 模型白名单校验下沉到 `TokenAuth`/OAuth 中间件，覆盖 RelayTask、视频与任务插件入口
  - [x] SubTask 3.2: access token 校验要求 app 存在且启用，缺少 app 时 fail-closed
  - [x] SubTask 3.3: 应用创建/更新/删除后调用 `InvalidateOAuthAppCache`；禁用/删除时吊销关联令牌
  - [x] SubTask 3.4: 测试：任务入口白名单拦截、禁用后调用被拒、删除后不 fail-open

- [x] Task 4: Supabase 模板安全修复（模板与 demo 副本同步）
  - [x] SubTask 4.1: `user-sync` 从 Authorization JWT 取用户，忽略 body 身份；`gateway_user_id` 仅来自网关校验
  - [x] SubTask 4.2: `user_profiles` 客户端策略改为只读；`gateway_user_id` 写入仅 service_role
  - [x] SubTask 4.3: `seed_outfit_demo` 撤销 PUBLIC/anon/authenticated 执行；`app_config` 收敛为 `id=1` 单行
  - [x] SubTask 4.4: 修正 `ai_generations` 插入字段与错误检查、额度查询端点/凭据、openapi/SDK 契约、模板 `.gitignore` 与 `.env.local`

- [x] Task 5: CLI 健壮性
  - [x] SubTask 5.1: 非 loopback 强制 HTTPS，`CheckRedirect` 拒绝 scheme 降级与跨 origin
  - [x] SubTask 5.2: 2xx 非 JSON 信封按错误返回；响应体超过上限显式报错
  - [x] SubTask 5.3: `--quota` 与 `--unlimited-quota` 冲突时报 `usage_error`；`--expires-in <= 0` 报 `usage_error`
  - [x] SubTask 5.4: `--pretty` 列取并集、数字保留字面量、CJK/emoji 显示宽度对齐、标量数组回退、单元格换行处理
  - [x] SubTask 5.5: 配置目录权限收敛、拒绝符号链接、原子写；补充对应测试

- [x] Task 6: 前端加固
  - [x] SubTask 6.1: `oauth-apps` 校验 `success`，失败走统一错误处理；列表补错误态与重试；mutation 防重复提交
  - [x] SubTask 6.2: `consent` 路由补 `validateSearch`（字符串归一化、长度限制）
  - [x] SubTask 6.3: CLI 授权默认不勾选 `user:write`/`option:write`/`model:write`/`deployment:write`，提交前展示最终 scope 摘要
  - [x] SubTask 6.4: 按 `web/AGENTS.md` 复用既有组件，验证 7 个 locale 文案齐全

- [x] Task 7: 后端 Medium
  - [x] SubTask 7.1: refresh token 轮换改为原子条件更新，`RowsAffected` 校验，重放时吊销整条链
  - [x] SubTask 7.2: 修复 OAuth 路径 `token_id=0` 导致 billing 端点 nil 解引用（实现前完整阅读 `.agents/rules/billing.md`）
  - [x] SubTask 7.3: `POST /api/token/` 增加 `middleware.DisableCache()`

- [x] Task 8: 治理项
  - [x] SubTask 8.1: 恢复 `docker-compose.yml` 与两个 workflow 的官方镜像名、cosign 安装与签名、`id-token: write`
  - [x] SubTask 8.2: 还原 7 个 locale 的 `footer.newapi.projectAttributionSuffix` 键名

- [x] Task 9: 验证
  - [x] SubTask 9.1: `go build ./...`、相关包 `go test`、`gofmt -l`、`go vet`
  - [x] SubTask 9.2: refresh/授权码等 DB 行为在 SQLite、MySQL、PostgreSQL 上各跑一遍（含并发重放用例）
  - [x] SubTask 9.3: `bun run build` 与前端相关测试；CLI `--pretty`/退出码实机回测
  - [x] SubTask 9.4: 端到端负向用例：scope 提权、跨站同意、伪造 logout hint、禁用应用后调用、user-sync 伪造身份

# Task Dependencies

- Task 9 depends on Task 1–8
- Task 2 depends on Task 1.2（CSRF/会话策略确定后再接 step-up）
- Task 7.2 实现前必须先完整阅读 `.agents/rules/billing.md`
- Task 1、3、4、5、6、8 可并行；Task 6 与 Task 2.2 涉及同一前端目录，建议串行
