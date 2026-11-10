# 外部应用开发统一规范 Skill（Web） Spec

## Why

`new-api-oauth-integration` 目前只回答「怎么接 OAuth/OIDC」，示例是一个 Rsbuild SPA，缺少「一个新外部应用从零怎么搭」的统一规范：前端框架与目录、UI 组件体系、部署方式、后端 Supabase 工作流、CLI 自举都不在 skill 里。结果每个接入方自行选型，示例代码与文字规范各说各话。

本变更把该 skill 升级为**外部应用 App 开发统一规范（当前只覆盖 Web 端）**，OAuth/计费作为其中「身份与计费接入」章节保留。

## What Changes

- 重新定位 `SKILL.md`：从「OAuth 集成指南」改为「外部应用开发统一规范（Web）」，新增技术栈矩阵、工程结构、前端规范、后端规范、部署规范、参考资料索引。
- 新增 `references/standards/`：`nextjs.md`、`daisyui.md`、`vercel.md`、`ui-ux.md`、`supabase.md`，每个文件标注官方文档链接与适用版本。
- 原样内置第三方 skill 到 `references/vendor/`：`nextjs-react-typescript`、`vercel-react-best-practices`、`ui-ux-pro-max`（保留 `LICENSE`/`README`/来源版本；不内置字体等二进制资源）。
- `references/web/outfit-demo` 从 Rsbuild + React Router 迁移到 **Next.js App Router + Tailwind v4 + DaisyUI + @supabase/ssr**，保留已验证的 OAuth 回调、网关令牌生命周期、`user-sync`、额度展示与生成调用。
- `scripts/check-env.ps1` / `check-env.sh` 增加 `-Install` / `--install`：自动安装 `bun`（或 Node）、`supabase`、`vercel`、`cloudflared`、`newapi-cli` 并校验版本；原有自检项保留。
- `SKILL.md` Step 0 更新为「一条命令自举 + 自检」，补充 Vercel CLI。
- **BREAKING**：示例应用不再使用 Rsbuild，`rsbuild dev/build` 失效，目录结构与构建命令变为 Next.js。

## Impact

- Affected specs: `new-api-oauth-integration`（重写定位与目录），`supabase-app-starter`（示例迁移）
- Affected code:
  - `.agents/skills/new-api-oauth-integration/SKILL.md`
  - `.agents/skills/new-api-oauth-integration/references/**`
  - `.agents/skills/new-api-oauth-integration/scripts/check-env.ps1`
  - `.agents/skills/new-api-oauth-integration/scripts/check-env.sh`
- 不影响根仓库 Go 代码与 `web/` 管理台。

## ADDED Requirements

### Requirement: Web 应用统一技术栈

skill SHALL 定义新外部 Web 应用的默认技术栈与偏离条件：

- 前端：Next.js（App Router）+ TypeScript 严格模式 + React 19 + Tailwind CSS v4 + DaisyUI 5
- 后端：Supabase（Auth / Postgres + RLS / Edge Functions）
- 部署：Vercel（CLI 驱动）
- 身份：new-api OAuth/OIDC（Supabase Custom OIDC Provider 或 BFF）
- 包管理：bun

#### Scenario: 新建外部应用

- **WHEN** 开发者按 skill 从零创建应用
- **THEN** SKILL.md 给出推荐目录结构、技术栈矩阵、从环境自举到部署的顺序步骤，并链接到对应 references

#### Scenario: 需要偏离默认栈

- **WHEN** 应用必须使用其他框架或后端
- **THEN** skill 要求显式记录偏离原因，并只复用本 skill 的身份/计费章节，不复用前端与后端规范

### Requirement: 参考资料索引

skill SHALL 在 `SKILL.md` 中维护一张「主题 → references 文件」索引，覆盖 Next.js、DaisyUI、Vercel、UI/UX、Supabase 与现有 OAuth/计费内容；每个内置第三方资料 SHALL 标注来源、版本与许可证。

#### Scenario: 查规范

- **WHEN** 开发者阅读 SKILL.md
- **THEN** 能在 30 秒内定位到具体主题文件，并知道该文件是原创精简规范还是第三方原文

### Requirement: Next.js 前端规范

`references/standards/nextjs.md` SHALL 覆盖：

- App Router 目录约定（`app/`、`route groups`、`layout.tsx`、`page.tsx`、`route.ts`）
- Server / Client Component 边界：默认服务端，交互与浏览器 API 才 `'use client'`
- 数据获取与缓存：服务端取数、`revalidate`、避免在客户端重复请求
- 环境变量：仅 `NEXT_PUBLIC_*` 暴露到浏览器，服务端密钥不经客户端
- 加载/错误/空状态：`loading.tsx`、`error.tsx`、`not-found.tsx`
- TypeScript 严格模式与共享类型位置

#### Scenario: 新增页面

- **WHEN** 开发者新增一个页面
- **THEN** 默认是 Server Component；只有表单、事件、`localStorage` 等交互才拆出 Client Component

### Requirement: DaisyUI 使用规范

`references/standards/daisyui.md` SHALL 说明：

- DaisyUI 5 与 Tailwind CSS v4 的接入方式（CSS 中 `@plugin "daisyui"`，无需 `tailwind.config.js`）
- 主题选择、`data-theme` 切换与暗色模式
- 组件优先：`btn`、`card`、`modal`、`alert`、`input`、`badge` 等语义类，避免逐像素自定义
- 自定义样式的边界与命名

#### Scenario: 实现 UI

- **WHEN** 开发页面组件
- **THEN** 优先使用 DaisyUI 语义类组合，主题色通过 token 而非硬编码颜色

### Requirement: Vercel CLI 部署规范

`references/standards/vercel.md` SHALL 覆盖：

- `vercel login` / `link` / `deploy` / `env pull` / `promote` / `logs`
- 预览环境与生产环境的变量差异
- 部署后必须同步更新的三处地址：new-api OAuth App 回调、Supabase Redirect URL allowlist、应用内 `NEXT_PUBLIC_*` 网关地址
- 与 Supabase Edge Functions 的联调顺序

#### Scenario: 首次部署到 Vercel

- **WHEN** 开发者按规范部署
- **THEN** 生产域名可用，OAuth 登录回调落在新域名且不再出现 `redirect_uri_mismatch`

### Requirement: Supabase 后端规范

`references/standards/supabase.md` SHALL 覆盖：

- Supabase CLI：`login`、`link`、`migration new`、`db push`、`db query`、`functions deploy`、`secrets set`
- Auth：Custom OIDC Provider（`custom:newapi`）创建/更新/删除，issuer 必须等于网关对外地址
- 数据：RLS 全表开启、按 `client_id`（`current_app_client_id()`）隔离行
- Edge Function：只用 `service_role` 在服务端，绝不信任请求体中的用户身份字段
- SSR：`@supabase/ssr` 的 cookie 会话与 Next.js 中间件协作

#### Scenario: 新增业务表

- **WHEN** 新增一张业务表
- **THEN** 表包含 `client_id`，开启 RLS，策略基于 `current_app_client_id()`，并在迁移文件中提交

### Requirement: UI/UX 规范

`references/standards/ui-ux.md` SHALL 提炼 ui-ux-pro-max 的通用部分：

- 设计 token（颜色、字号、圆角、间距）与层级
- 排版与可读性、对比度、焦点可见性、键盘可达性
- 加载、空状态、错误状态、禁用态
- 响应式断点与移动优先
- 动效克制原则（时长、缓动、减少动效偏好）

#### Scenario: 页面评审

- **WHEN** 按 checklist 评审一个页面
- **THEN** 各项（对比度、焦点、空/错/loading 态、移动端布局）均可逐条核对

### Requirement: CLI 自举与自检

`scripts/check-env.ps1` 与 `scripts/check-env.sh` SHALL 支持自动安装：

- 新增参数 `-Install`（PowerShell）与 `--install`（bash）
- 缺失时按平台安装：`bun`（或 Node/npm）、`supabase`、`vercel`、`cloudflared`、`newapi-cli`
- 安装后复验版本，并继续执行原有 discovery / provider / authorize 检查
- 不带参数时行为不变（只检查不安装）

#### Scenario: 新机器首次运行

- **WHEN** 机器缺少上述 CLI 且带 `-Install` 运行
- **THEN** 脚本安装缺失项、逐项打印 PASS/FAIL，任一项失败退出码非 0

#### Scenario: 已有环境

- **WHEN** 不带参数运行
- **THEN** 不执行任何安装动作，只做检查

### Requirement: outfit-demo 迁移到 Next.js

参考应用 SHALL 运行在 Next.js App Router 上并使用 DaisyUI：

- 路由：`/`（换装）、`/wardrobe`、`/history`、`/profile`、`/login`、`/auth/callback`
- 保留：Supabase 客户端单例、网关令牌持有与刷新（`gatewayAuth`）、`user-sync` 调用、额度/充值入口、生成调用
- 服务端组件承载数据读取，客户端组件承载交互
- `bun run build` 成功，可由 Vercel 部署

#### Scenario: 登录闭环

- **WHEN** 用户通过 `custom:newapi` 登录
- **THEN** 回调完成 code 兑换、写入 `user_profiles`、个人中心显示显示名与网关 User.Id

#### Scenario: 生产构建

- **WHEN** 在 `references/web/outfit-demo` 执行 `bun run build`
- **THEN** 构建成功且无 TypeScript 错误

## MODIFIED Requirements

### Requirement: OAuth / OIDC 与计费接入章节

完整保留现有 scope、令牌生命周期、安全约束与计费接口说明，但作为统一规范中的「身份接入」「AI 调用」「用户与计费」章节存在，示例代码改为 Next.js 版本（Server Route Handler / Client Component 分别给出）。

## REMOVED Requirements

### Requirement: Rsbuild SPA 示例

**Reason**: 统一技术栈改为 Next.js，避免维护两套示例。

**Migration**: 现有页面与逻辑迁移到 App Router；删除 `rsbuild.config.ts`、`index.html` 与 `react-router-dom` 依赖，构建命令改为 `next build`。
