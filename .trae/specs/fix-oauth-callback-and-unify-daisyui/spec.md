# 修复 OAuth 登录回调 + 统一消费端 DaisyUI 规范

## Why

1. **登录失败**：`/auth/callback` 是客户端页面，在 `useEffect` 里调用 `exchangeCodeForSession(code)`。Next.js 16 + React 19 开发态 StrictMode 会重复执行 effect，同一个授权码被兑换两次；第二次请求打到 Supabase GoTrue 时返回 `invalid flow state, no valid flow state found`。页面把该英文错误原样展示，即使第一次兑换已经成功、会话已经建立，用户仍看到"登录失败"。
2. **DaisyUI 使用不统一**：消费端页面确实用了 DaisyUI（navbar / menu / drawer / btn / card / table / badge / alert / modal，11 个文件约 154 处），但混入了 DaisyUI 4 的残留类（`input-bordered`、`textarea-bordered`、`card-compact`，在已安装的 DaisyUI 5.7.50 中不存在），且登录卡片、角色页顶部面板等仍用手写 `rounded-box + shadow` 代替 `card`。skill 与 `apps/ai-outfit/AGENTS.md` 都没有可执行的强制条款与验收方法。

## What Changes

- `apps/ai-outfit/src/app/auth/callback/`：删除客户端 `page.tsx`，新增服务端 `route.ts`（GET），用 `createServerSupabase().auth.exchangeCodeForSession(code)` 单次兑换。
- 回调错误处理：
  - 已有 Supabase session → 直接 `redirect('/studio')`（避免"已登录但报错"）。
  - Supabase 回传 `error`（如 `access_denied`）→ `/login?error=oauth_denied`。
  - 兑换错误属于 flow state / code verifier 丢失类 → 清除 `sb-*-auth-token*` 相关 Cookie → `/login?error=session_expired`。
  - 其它兑换错误 → `/login?error=oauth_failed`。
- `/login` 读取 `error` 查询参数，展示中文友好提示与"重新登录"按钮，不再直接显示底层英文 `error.message`。
- 新增最小客户端引导组件，在已登录布局中调用 `ensureGatewayAuthListener()`，保证删除回调页后网关 token 仍会被 seed（当前 `ensureGatewayAuthListener` 全项目无调用点）。
- 消费端 DaisyUI 统一（admin 路由树保持不变）：
  - 移除所有 `input-bordered` / `textarea-bordered` / `card-compact`（DaisyUI 5 不存在）。
  - 登录卡片、角色页顶部面板改为 `card` + `card-body`。
  - 错误提示块改为 `alert alert-error alert-soft`（或 `alert alert-error`）。
  - 保留现有 `navbar` / `menu` / `drawer` / `table` / `badge` / `modal` 结构，不做视觉重构。
- `apps/ai-outfit/AGENTS.md`：新增"消费端 UI 强制规范"与"OAuth 回调规范"两节。
- `.agents/skills/new-api-oauth-integration/references/standards/daisyui.md`：新增"强制使用与验收"章节（禁用类清单、grep 验收命令、页面改造对照表）。
- `.agents/skills/new-api-oauth-integration/SKILL.md`：在 Step 6 增加硬性条款并引用上述章节（保持文件 ≤ 350 行 / < 28KB）。

## Impact

- Affected specs: `new-api-oauth-integration` skill、`apps/ai-outfit` 应用约定
- Affected code:
  - `apps/ai-outfit/src/app/auth/callback/`、`apps/ai-outfit/src/app/login/`
  - `apps/ai-outfit/src/app/layout.tsx`、`src/components/login-card.tsx`、`src/components/MediaCard.tsx`、`src/components/CharacterUploader.tsx`
  - `apps/ai-outfit/src/app/{studio,wardrobe,poses,characters,profile}/**`
  - `apps/ai-outfit/AGENTS.md`
  - `.agents/skills/new-api-oauth-integration/{SKILL.md,references/standards/daisyui.md}`

## Non-Goals

- 不改 admin 路由树（`src/app/admin/**` 继续使用 Ant Design + Refine）。
- 不做品牌主题 / 暗色模式改造，`data-theme` 维持现状。
- 不改 new-api Go 后端的 OAuth 端点。
- 不重写页面信息架构或视觉设计，只统一组件与类名用法。

## Constraints & Assumptions

- DaisyUI 5.7.50 中不存在 `input-bordered`、`textarea-bordered`、`card-compact`；`card-sm`、`input`、`textarea`、`btn-block`、`table-zebra`、`badge-soft` 存在（已在 `node_modules/daisyui/components/*.css` 核实）。
- `@supabase/ssr` 的 `createBrowserClient` 把 PKCE code verifier 写入同源 Cookie，服务端 Route Handler 可读取并完成兑换。
- Supabase 的 flow state 一次性消费，重复兑换必然失败，因此回调必须保证"每个授权码只兑换一次"。
- 不新增数据库迁移，不改 RLS。

## ADDED Requirements

### Requirement: OAuth 回调单次兑换

系统 SHALL 在服务端 Route Handler 中完成一次且仅一次 `exchangeCodeForSession`，不得在客户端 effect 中兑换授权码。

#### Scenario: 首次回调成功

- **WHEN** 浏览器携带 `?code=...` 访问 `/auth/callback`
- **THEN** 服务端兑换成功、写入会话 Cookie，并 302 跳转 `/studio`

#### Scenario: 重复访问同一回调 URL

- **WHEN** 已存在有效会话的用户再次访问带旧 `code` 的 `/auth/callback`
- **THEN** 直接跳转 `/studio`，不显示 `invalid flow state` 错误

### Requirement: 登录失败可恢复

系统 SHALL 把回调失败映射为可恢复的用户提示，并在会话态失效时清理残留认证 Cookie。

#### Scenario: flow state 丢失

- **WHEN** 兑换返回的错误信息包含 `flow state` 或 `code verifier`
- **THEN** 清除 `sb-*-auth-token*` Cookie 并跳转 `/login?error=session_expired`
- **AND** 登录页展示中文提示与"重新登录"按钮

### Requirement: 消费端 DaisyUI 强制统一

消费端页面 SHALL 只使用 DaisyUI 5 有效组件类，SHALL NOT 使用已移除的 DaisyUI 4 修饰类。

#### Scenario: 静态检查

- **WHEN** 在 `apps/ai-outfit/src`（排除 `src/app/admin/**`）搜索 `-bordered|card-compact`
- **THEN** 无命中

#### Scenario: 面板与提示组件

- **WHEN** 页面需要卡片容器或错误提示
- **THEN** 使用 `card` / `card-body` 与 `alert alert-error`

### Requirement: 规范可执行

项目文档 SHALL 包含 DaisyUI 与 OAuth 回调的可执行约束与验收方法。

#### Scenario: 约束落盘

- **WHEN** 开发者或 agent 修改消费端 UI 或登录流程
- **THEN** `apps/ai-outfit/AGENTS.md` 与 skill 的 `daisyui.md` 均给出强制条款、禁用类清单和 grep 验收命令

## Acceptance Criteria

### AC-1 回调服务端化（rule）

`apps/ai-outfit/src/app/auth/callback/page.tsx` 不存在；存在 `route.ts` 且导出 `GET`，其中调用 `exchangeCodeForSession`。`grep -rn "exchangeCodeForSession" apps/ai-outfit/src` 只命中 `auth/callback/route.ts`。

**Evidence**: 文件列表 + grep 输出。

### AC-2 重复兑换与已登录场景不报错（rule）

回调实现满足：兑换前若 `auth.getUser()` 有用户则直接跳 `/studio`；兑换错误信息命中 `flow state|code verifier` 时清 Cookie 后跳 `/login?error=session_expired`。

**Evidence**: 代码审查 + 手工访问同一回调 URL 两次，第二次落在 `/studio` 且无错误文案。

### AC-3 登录页友好提示（rule）

`/login?error=session_expired` 渲染中文提示且包含"重新登录"按钮；页面源码不再出现裸 `error.message` 作为唯一展示。

**Evidence**: 浏览器访问 `/login?error=session_expired` 截图/DOM。

### AC-4 网关 token 引导保留（rule）

删除客户端回调页后，项目中存在对 `ensureGatewayAuthListener()` 的调用点，且该调用位于已登录布局的客户端组件中。

**Evidence**: `grep -rn "ensureGatewayAuthListener" apps/ai-outfit/src` 至少 2 处（定义 + 调用）。

### AC-5 无 DaisyUI 4 残留类（rule）

`apps/ai-outfit/src` 排除 `src/app/admin/**` 后，`grep -rn "input-bordered|textarea-bordered|card-compact"` 无命中。

**Evidence**: grep 输出为空。

### AC-6 消费端结构与组件统一（rubric）

维度：消费端页面是否以 DaisyUI 组件为主承载容器、输入、状态与弹窗。

- 0：仍存在手写 `rounded-box + shadow` 主面板或裸错误 div
- 1：主要页面已改，仍有 1–2 处遗漏
- 2：登录、studio、wardrobe、poses、characters、profile、history 的容器/输入/错误提示均使用 DaisyUI 类，admin 未改动

**Evidence**: 逐文件检查清单 + 关键页面截图。

### AC-7 应用级规范落盘（rule）

`apps/ai-outfit/AGENTS.md` 包含"消费端 UI（DaisyUI 5）"与"OAuth 登录回调"两节，且分别写有禁止项与验收命令。

**Evidence**: grep 两个标题命中并读取内容。

### AC-8 Skill 规范落盘（rule）

`references/standards/daisyui.md` 新增"强制使用与验收"章节（含禁用类清单与 grep 命令）；`SKILL.md` Step 6 含指向该章节的硬性条款，且 `SKILL.md` 仍 ≤ 350 行、< 28KB。

**Evidence**: `grep -n "强制使用与验收"` 命中；`(Get-Content SKILL.md).Count` 与文件大小输出。

### AC-9 构建与类型检查（rule）

`cd apps/ai-outfit && bun run lint && bun run build` 成功。

**Evidence**: 命令输出尾部（含 route 数量）。

## Open Questions

- 无。已确认：本次只统一消费端，admin 保持 Ant Design；不做品牌主题/暗色模式。
