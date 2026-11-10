# Tasks: 修复 OAuth 登录回调 + 统一消费端 DaisyUI 规范

## Task 1: 回调改为服务端 Route Handler（单次兑换）
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None

- [x] 删除 `apps/ai-outfit/src/app/auth/callback/page.tsx`
- [x] 新增 `apps/ai-outfit/src/app/auth/callback/route.ts`，导出 `GET`
- [x] 兑换前先 `auth.getUser()`：已有用户则 `NextResponse.redirect(new URL('/studio', request.url))`
- [x] 读取 `code`；缺失则跳 `/login?error=missing_code`
- [x] 读取 Supabase 回传的 `error` / `error_description`；存在则跳 `/login?error=oauth_denied`
- [x] `exchangeCodeForSession(code)` 只调用一次
- [x] 错误分类：`/flow state|code verifier|pkce/i` → 清 `sb-*-auth-token*` Cookie 后跳 `/login?error=session_expired`；其它 → `/login?error=oauth_failed`
- [x] 成功后跳 `/studio`
- [x] 复用 `lib/supabase/server.ts` 的 `createServerSupabase()`，未新建 client
- [x] 不记录 `code` / `code_verifier` / token

**Completion Evidence**:
- `grep -rn "exchangeCodeForSession" apps/ai-outfit/src` → 仅 `src/app/auth/callback/route.ts:26`。
- `grep -rn "useEffect" apps/ai-outfit/src/app/auth` → 无命中。
- 运行中的 dev server 实测：`GET /auth/callback` → 307 `/login?error=missing_code`；`GET /auth/callback?code=invalid-code-for-test` → 307 `/login?error=session_expired`。

---

## Task 2: 登录页错误提示 + 网关认证引导
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1

- [x] `/login` 接收 `searchParams.error`，映射中文文案
- [x] 提示使用 `alert alert-error alert-soft`，按钮在错误态显示"重新登录"
- [x] 底层 `error.message` 只放在 `<details>` 诊断区，不作为主提示
- [x] 新增 `src/components/gateway-auth-bootstrap.tsx` 调用 `ensureGatewayAuthListener()`，并在 `layout.tsx` 已登录分支渲染
- [x] 登出时 `SIGNED_OUT` 分支调用 `clearGatewayTokens()`（既有逻辑，监听器现已真正挂载）

**Completion Evidence**:
- `GET http://localhost:3000/login?error=session_expired` → 200，页面含"登录状态已失效，请重新登录。"与"重新登录"。
- `grep -rn "ensureGatewayAuthListener" apps/ai-outfit/src` → 定义 1 处 + bootstrap 调用 1 处。
- `grep -rn "error.message" src/app/login src/app/auth` → 无命中。

---

## Task 3: 消费端 DaisyUI 5 类名与结构统一
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None

- [x] 移除全部 `input-bordered`（wardrobe ×2、poses ×2、StudioClient ×2、ProfileClient ×1、CharactersClient ×1、CharacterUploader ×1）
- [x] 移除 `textarea-bordered`（CharacterUploader、CharactersClient）
- [x] `card-compact` → `card-sm`（CharactersClient、MediaCard）
- [x] `login-card.tsx` 主容器改为 `card` + `card-body`
- [x] `login-card.tsx` 错误块改为 `alert alert-error alert-soft`
- [x] `characters/page.tsx` 顶部面板改为 `card` + `card-body`
- [x] `StudioClient.tsx` 内联错误块改为 `alert alert-error alert-soft`
- [x] `history/page.tsx` 保持 `table table-sm table-zebra`
- [x] 未修改 `src/app/admin/**`
- [x] 顺带修正 `MediaCard.tsx` 中失效的 eslint-disable 注释写法（原注释把中文说明混进规则名，导致 lint 报 "Definition for rule ... was not found"）

**Completion Evidence**:
- `grep -rn "input-bordered|textarea-bordered|select-bordered|card-compact|card-bordered" apps/ai-outfit/src` → 无命中。
- 消费端页面已无 `rounded-box bg-base-100` 手写面板；剩余 `text-error` 仅用于删除按钮与诊断区（语义色，符合规范）。
- `bunx eslint src/app/login src/app/auth src/app/studio src/app/wardrobe src/app/poses src/app/characters src/app/profile src/app/history src/components src/app/layout.tsx` → 退出码 0。

---

## Task 4: `apps/ai-outfit/AGENTS.md` 增加强制规范
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: None

- [x] 新增「消费端 UI（DaisyUI 5，强制）」章节：安装方式、组件优先清单、禁用 DaisyUI 4 残留类、语义色约束、admin 例外、PowerShell/bash 验收命令
- [x] 新增「OAuth 登录回调（强制）」章节：服务端 Route Handler、单次兑换、已登录直接跳转、失败不回显敏感信息、flow state 清理策略、验收命令
- [x] 保留 `next dev` 自动生成的 nextjs-agent-rules 块原样

**Completion Evidence**: `AGENTS.md` 41 行；`grep -n "消费端 UI（DaisyUI 5"` 与 `grep -n "OAuth 登录回调"` 均命中。

---

## Task 5: Skill 规范增强（DaisyUI 强制与验收）
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: None

- [x] `references/standards/daisyui.md` 新增「## 8. 强制使用与验收」：8.1 DaisyUI 4→5 迁移对照、8.2 手写→DaisyUI 对照、8.3 验收命令、8.4 交付检查项
- [x] `SKILL.md` Step 6 增加 3 条硬性条款（有效类限制、组合而非重造、服务端单次兑换），并引用 `daisyui.md#8`
- [x] `SKILL.md` 仍 ≤ 350 行且 < 28KB

**Completion Evidence**: `daisyui.md` 253 行 / 10382 B；`SKILL.md` 312 行 / 27050 B；`grep -n "强制使用与验收" daisyui.md` 命中第 202 行。

---

## Task 6: 验证与回归
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1, Task 2, Task 3, Task 4, Task 5

- [x] `bun run build` 成功
- [x] 变更范围内 eslint 通过
- [x] 回调缺少 code / 无效 code 的失败路径实测
- [x] `/login?error=session_expired` 中文提示实测
- [x] 浏览器实测登录按钮 → 最终落在 `/studio`，全程无 `invalid flow state`
- [x] 复核 admin 未改动（无目标文件编辑）

**Completion Evidence**:
- `bun run build` → 成功；23 条路由，含 `ƒ /auth/callback`。
- `bunx eslint <所有变更的消费端目录>` → 退出码 0。
- **遗留（既有问题，非本次引入）**：仓库整体 `bun run lint` 仍有 3 个 error，全部位于本次未改动的文件：`src/app/admin/outfits/page.tsx:42`、`src/app/admin/poses/page.tsx:33`（`react-hooks/set-state-in-effect`）、`src/app/api/admin/[table]/route.ts:12`（`no-explicit-any`）。按 spec 的 Non-Goals（不改 admin 路由树）未处理。

---

# Task Dependencies

- Task 1 → Task 2
- Task 3 / Task 4 / Task 5 与 Task 1/2 无依赖，并行完成
- Task 6 依赖 Task 1–5
