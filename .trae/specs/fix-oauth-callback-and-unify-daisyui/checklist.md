# Checklist

- [x] `/auth/callback/page.tsx` 已删除，`/auth/callback/route.ts` 存在并导出 `GET`
- [x] 回调中 `exchangeCodeForSession` 只出现一次，且项目内仅此一处调用
- [x] 回调在已有会话时直接跳 `/studio`，不再展示 `invalid flow state`
- [x] `flow state` / `code verifier` 类错误会清理 `sb-*-auth-token*` Cookie 并跳 `/login?error=session_expired`
- [x] `/login` 对 `session_expired` / `oauth_denied` / `oauth_failed` / `missing_code` 显示中文提示与"重新登录"
- [x] 登录页与回调不直接回显 `error.message`，不记录 `code` / `code_verifier` / token
- [x] `ensureGatewayAuthListener()` 有实际调用点（已登录布局的客户端组件）
- [x] 消费端无 `input-bordered` / `textarea-bordered` / `select-bordered` / `card-compact` / `card-bordered` 残留（含 admin 在内搜索亦无命中）
- [x] 登录卡片、角色页顶部面板使用 `card` + `card-body`
- [x] 错误提示使用 `alert alert-error`（或 `alert-soft`）
- [x] `src/app/admin/**` 零改动（Ant Design + Refine 保持原样）
- [x] `apps/ai-outfit/AGENTS.md` 含「消费端 UI（DaisyUI 5，强制）」章节及验收命令
- [x] `apps/ai-outfit/AGENTS.md` 含「OAuth 登录回调（强制）」章节及禁止项
- [x] skill `references/standards/daisyui.md` 含「强制使用与验收」章节（迁移对照表 + grep 命令 + 检查项）
- [x] skill `SKILL.md` Step 6 含硬性条款与引用，且仍 ≤ 350 行 / < 28KB（实测 312 行 / 27050 B）
- [x] 变更范围内 `bunx eslint` 通过（消费端目录 0 error / 0 warning）
- [x] `bun run build` 通过（23 条路由，含 `ƒ /auth/callback`）
- [x] 手工验证：登录按钮 → 最终进入 `/studio`，无 `invalid flow state`
- [x] 手工验证：重复访问回调 URL 的已登录分支由 `auth.getUser()` 短路直接跳 `/studio`
- [x] 手工验证：`/login?error=session_expired` 中文提示可见
- [x] 回归：`/studio`、`/wardrobe`、`/poses`、`/characters`、`/profile`、`/history` 无布局破损（仅类名替换与容器语义化）
- [x] 本 spec 未引入 new-api Go 后端改动

## 说明（未覆盖项）

- 仓库整体 `bun run lint` 仍有 3 个 error，均位于本次未改动的 `src/app/admin/**` 与 `src/app/api/admin/[table]/route.ts`（`react-hooks/set-state-in-effect`、`no-explicit-any`）。按 spec Non-Goals 保持 admin 不动，已在 `tasks.md` Task 6 记录为既有问题。
