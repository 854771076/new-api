<!-- BEGIN:nextjs-agent-rules -->

## This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` (resolved from this file's directory; in monorepos the `next` package may not be visible from the repo root) before writing any code. Heed deprecation notices.

This block is written and re-added by `next dev` — verify at `node_modules/next/dist/server/lib/generate-agent-files.js`. Removing it from a diff only re-creates the uncommitted change; committing it with your work keeps the tree clean.

<!-- END:nextjs-agent-rules -->

## 消费端 UI（DaisyUI 5，强制）

适用范围：`src/app/**` 与 `src/components/**`，但 `src/app/admin/**` 除外（后台使用 Ant Design + Refine，禁止改成 DaisyUI，也禁止与消费端共用布局）。

- 样式入口只能是 Tailwind 4 CSS 配置：`src/app/globals.css` 保持 `@import "tailwindcss";` + `@plugin "daisyui";`。禁止新增 `tailwind.config.js/ts`，禁止 v3 的 `@tailwind base/components/utilities`。
- 先查再用：DaisyUI 已有的组件必须优先使用，不允许手写替代实现。
  - 容器 → `card` + `card-body`（紧凑用 `card-sm`）
  - 按钮 → `btn`（主操作 `btn-primary`，次要 `btn-ghost`/`btn-outline`）
  - 输入/下拉/多行 → `input` / `select` / `textarea`（DaisyUI 5 已自带边框）
  - 提示 → `alert alert-info|success|warning|error`，弱化用 `alert-soft`
  - 状态 → `badge badge-*`；表格 → `table table-zebra`；弹窗 → `<dialog class="modal">` + `modal-box`；加载 → `loading loading-spinner`
- 禁止使用 DaisyUI 5 中已不存在的类，写了不会报错但完全没有样式：
  - 修饰类：`input-bordered`、`textarea-bordered`、`select-bordered`、`card-compact`、`card-bordered`、`alert-sm`（alert 没有尺寸修饰符，改成 `text-sm`）
  - 整个组件：`join` / `join-item`（5.x 已移除，改用 `flex gap-*` 或 `label.input` 包裹图标+输入框）、`tabs-boxed`（改用 `tabs-box` 或 `tabs-border`）、`form-control` / `label-text`（改用 `fieldset` + `fieldset-legend`）
  - 从未存在：`chip` / `chip-primary` / `chip-outline`（筛选标签用 `btn btn-xs` 或 `badge`）
- 语义色只用 `base-100/200/300`、`base-content`、`primary`、`success`、`warning`、`error`（含 `-content` 与 `/10` 透明度变体）。禁止 `bg-gray-*`、`text-gray-*`、`bg-blue-*` 等原始调色板，禁止 `#hex` / `rgb()`。
- 背景必须搭配对应前景：`bg-primary` 配 `text-primary-content`；弱化文本用 `text-base-content/70`。
- 允许用 Tailwind 工具类做布局（`flex`、`grid`、`gap-*`、`max-w-*`、响应式断点），但不要用工具类重造 DaisyUI 组件的视觉。
- 验收命令（新改动的消费端页面必须通过，无输出为通过）：
  PowerShell:
  `Get-ChildItem -Recurse src -Include *.tsx | Where-Object FullName -notmatch '\\admin\\' | Select-String -Pattern 'input-bordered|textarea-bordered|select-bordered|card-compact|card-bordered|alert-sm|\bjoin-item\b|tabs-boxed|chip-|form-control|label-text'`
  bash:
  `grep -rn "input-bordered\|textarea-bordered\|select-bordered\|card-compact\|card-bordered\|alert-sm\|join-item\|tabs-boxed\|chip-\|form-control\|label-text" src --exclude-dir=admin`
- 提交/交付说明中必须写明：改动的消费端页面、复用/新增的 DaisyUI 组件、上述验收命令的实际结果。

## OAuth 登录回调（强制）

- 回调必须实现为服务端 Route Handler：`src/app/auth/callback/route.ts` 导出 `GET`，使用 `createServerSupabase().auth.exchangeCodeForSession(code)`。
- 禁止在客户端组件的 `useEffect` / `useLayoutEffect` 中调用 `exchangeCodeForSession`。React 19 StrictMode 会重复执行 effect，授权码被兑换两次，Supabase 返回 `invalid flow state, no valid flow state found`。
- 同一 `code` 只允许兑换一次；回调入口先 `auth.getUser()`，已有会话直接跳 `/studio`，避免重复访问旧回调 URL 时报错。
- 失败时不要回显底层英文错误，也不要回显 `code` / `code_verifier` / token；统一跳 `/login?error=<code>`，由登录页映射中文提示。
- `flow state` / `code verifier` 类错误视为会话态失效：清除 `sb-*auth-token*` Cookie 后跳 `/login?error=session_expired`。
- 验收：`grep -rn "exchangeCodeForSession" src` 只命中 `src/app/auth/callback/route.ts`。

