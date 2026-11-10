# Spec: AI Outfit — UI 美化 + 数据通路修复

## Problem

`apps/ai-outfit/` 已完成 scaffolding 和数据库 seed（poses 1439 条、outfits 0 条），但：

1. **所有页面的卡片/列表没有渲染图片**：wardrobe 查 outfits、poses 查 poses，拿到了 `thumb_path`/`preview_path` 却没有调用 Supabase Storage 生成 signed URL，导致 UI 全部是纯文字卡片
2. **studio 和 characters 页面是骨架**：studio 只有三栏标题 + 禁用按钮；characters 是空状态
3. **wardrobe 0 条数据**：costume library zip 没解压，outfits 表为空（环境问题，spec 覆盖代码通路，解压作为 task 前置）
4. **UI 组件使用不充分**：全局 navbar 用裸 `<Link>` 而不是 DaisyUI 组件；没有空状态、加载骨架、error 提示；profile 没有下拉菜单；没有图标系统
5. **违反 ui-ux-pro-max 约束**：无 SVG 图标（用 emoji 作图标违反 `no-emoji-icons`）、无统一 spacing 节奏（8dp grid）、无 hover 反馈、无 skeleton shimmer、empty state 只有文字、touch target 可能不足 44px

## Goals

- **所有媒体卡片渲染缩略图**：wardrobe / poses / studio 选择器全部 `<img>` 渲染，带 signed URL
- **Studio 三栏可用**：人物上传 + 服装选择 + 姿势选择 + "开始生成"按钮真正工作
- **统一用 DaisyUI + Ant Design 框架组件**：替换裸 HTML，补空状态、加载骨架、下拉菜单、toast
- **遵循 ui-ux-pro-max skill 约束**：no-emoji-icons、8dp spacing、skeleton 加载、empty state 文案 + 行动按钮、hover/pressed 反馈、4.5:1 对比度、响应式
- **修复 data path**：SSR 页面服务端拉 signed URL 并传给组件，不用客户端再请求一次

## Non-Goals

- 不在本次 spec 解压 costume library zip 或重新 seed（由 task 0 前置）
- 不实现完整的 POST /api/generate 换装工作流（已有基础实现，studio 页接上即可）
- 不重写 admin 路由（已用 Refine + Ant Design，独立风格）
- 不引入新的 UI 框架（DaisyUI + Ant Design + Tailwind 已在项目中）

## Users

- C 端用户（AI 换装创作者）：登录 → studio 组合人物+服装+姿势 → 生成
- C 端浏览者：从 wardrobe 挑选单品、poses 挑姿势、history 看作品
- Admin：已有独立 `/admin` 路由

## Functional Requirements

### AC-1: 媒体卡片渲染缩略图 (rule)
wardrobe `/wardrobe` 和 poses `/poses` 页面，每个 outfit/pose 卡片必须：
- 渲染 `<img src={signedThumbUrl} alt={name_zh} loading="lazy">`
- signed URL 有效期 ≥ 3600s，用 Supabase admin client service_role 生成
- 有 aspect-ratio placeholder 防止 CLS（`aspect-[3/4]` 或 `aspect-square`）
- 图片加载失败显示 fallback 占位（DaisyUI badge + 文案）

**Evidence**: `/wardrobe` 和 `/poses` 页面 SSR 渲染的 HTML 中每个卡片包含 `<img>` 标签，且点击页面图片能正常显示缩略图。

### AC-2: SSR signed URL service (rule)
在 `src/lib/` 提供可复用的 server-side helper，接受 `bucket + objectPath` 返回 signed URL 字符串；wardrobe/poses/studio 选择器页面在 SSR 阶段批量生成、传递给组件。

**Evidence**: 至少一个页面文件 `page.tsx` 调用该 helper 并将结果作为 prop 传给卡片组件；helper 单元测试通过（如有）或 build 成功 + 浏览器验证。

### AC-3: 统一 UI 框架组件 (rubric)
评分 0-3，pass ≥ 2。证据：DaisyUI 和 Ant Design 组件替换裸 HTML 的数量和质量。
| 等级 | 证据 |
|------|------|
| 3 | navbar 用 `.navbar` + `.menu menu-horizontal` + `.dropdown` 菜单；按钮统一 `.btn btn-primary`/`.btn btn-outline`；空状态 `.alert alert-info` + 行动按钮；加载用骨架/`.skeleton`；form input 用 `.input`；toast 用 Ant Design `message` |
| 2 | 至少替换 3 处裸 HTML；加载用骨架；空状态有文案 |
| 1 | 仅少数替换，无加载/空状态 |

**Evidence**: git diff 或浏览对比 + build 通过。

### AC-4: Studio 三栏最小可用 (rule)
- Studio 页面三栏（人物 / 服装 / 姿势）都是真正的选择器（从 Postgres 读数据 + 渲染 signed URL 缩略图）
- "开始生成"按钮不再 disabled，点击后调 `POST /api/generate`，有 loading 状态
- 生成完成后 redirect 到 `/history` 或在 studio 内展示结果

**Evidence**: 浏览器端到端测试通过（上传人物 → 选服装 → 选姿势 → 生成 → history 出现新行）。

### AC-5: 图标系统无 emoji (rule)
所有导航、按钮、状态图标使用 SVG 图标（Lucide React），禁止使用 emoji 作结构性图标。

**Evidence**: `grep -r "🎨\\|🚀\\|✨\\|⚙️" apps/ai-outfit/src/app` 结果为空或仅在 `title`/`alt` 描述中。

### AC-6: Empty state + Loading state (rule)
- wardrobe/poses/history 页面数据为 0 时显示 DaisyUI `.alert` 组件 + 行动按钮（如"去 studio 试试"）
- studio 选择器加载中显示 DaisyUI `.skeleton` 网格占位
- 数据加载失败显示 `.alert alert-error` + retry 按钮

**Evidence**: 页面 HTML 结构包含 `.skeleton` / `.alert` 元素；浏览器手动清空数据源后仍能渲染友好空状态。

### AC-7: ui-ux-pro-max 约束合规 (rubric)
评分 0-5，pass ≥ 3。
| 维度 | 权重 | 检查 |
|------|------|------|
| 图标 (no-emoji) | 1 | AC-5 已覆盖 |
| 8dp spacing | 1 | 所有 padding/gap 是 4/8/12/16/24/32/48 倍数 |
| Skeleton 加载 | 1 | AC-6 已覆盖 |
| Empty state + 行动 | 1 | AC-6 已覆盖 |
| 响应式 (mobile-first) | 1 | 640px 断点测试：三栏 → 单栏堆叠；touch target ≥ 44px |

**Evidence**: 逐项 checklist + 375px / 1024px 视口下截图。

### AC-8: Build + TypeScript 通过 (rule)
`bun run build` 在 `apps/ai-outfit/` 目录下成功无错误；`tsc --noEmit` 无类型错误。

**Evidence**: CI 日志或本地 build 输出。

## Constraints & Assumptions

- **Supabase anon key 权限不足**：Storage signed URL 必须用 service_role；SSR 页面用 server client，客户端用 route handler `/api/media/[bucket]/[...path]` 302 redirect
- **poses 表已有 1439 条**（seed 完成）、outfits 表 0 条（costume zip 未解压）——本次 spec 覆盖 outfits 数据通路（页面 + helper），解压和重新 seed 作为 Task 0 前置执行
- **Next.js 16 + App Router + React 19 + Tailwind v4 + DaisyUI v5 + antd v6** 是已确认的技术栈，不改变
- **AGENTS.md 规则**：前端 UI 组件优先复用已有 shadcn 风格 + DaisyUI，国际化用 `t('English key')`，Number formatting 用 `@/lib/format`（但 apps/ai-outfit 还没有 `src/i18n/`，暂不强求）
- **无新依赖**：Lucide React 如未安装需加 `bun add lucide-react`（非常轻量）

## Open Questions

- 服装 zip 是否必须解压？是的，outfits 表空时 wardrobe 永远是空态
- studio generate 结果是 redirect 还是原地展示？先 redirect，简单且已有 history 页面
