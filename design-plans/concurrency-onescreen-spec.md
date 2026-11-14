# /concurrency 一屏重构视觉规格 — Variant E（One-screen · Stable）

用户拍板：**E 风格（语义色 + 控制台质感）+ 一屏展示不滚动 + 稳定感**。
本规格是 frontend-design（定方向）+ ui-ux-pro-max（风格/配色/UX 规则）阶段的收敛产出，实施以本文件为准。

## 0. 方向陈述

"Ops console"：E 的语义色带、色调 chip、克制微动效，压进固定一屏。
唯一大胆元素 = **verdict band**（结论带）；其余全部克制。
明确否决（ui-ux-pro-max --design-system 输出中的玻璃拟态/新字体/新调色板）：离 brief 太远，BRIEF 已锁定 E，只用仓库既有 token。

## 1. 一屏布局（核心约束）

外壳事实（已实测）：
- `SidebarInset` = `h-[calc(100svh - app-header-height)] min-h-0 overflow-hidden`（固定视口高、裁剪）；
- `AnimatedOutlet` = `flex min-h-0 flex-1 flex-col`；页面 root 是其直接子节点。
- 因此页面 root 必须 `h-full min-h-0`，**页面自身永不产生滚动/溢出**（root.scrollHeight ≤ clientHeight 是验收断言）。

结构（root = `flex h-full min-h-0 flex-col gap-3 p-4`，shrink-0 区 + 一个弹性区）：

```
[header]     标题+DegradedBadge+副标题 | (右) Window Select + Live 指示 + scope 提示
[verdict]    结论带（固定 min-h，色变不改尺寸）
[kpi]        4 × StatCard（等高，描述单行截断）
[content]    flex-1 min-h-0 —— lg 及以上: grid 2×2:
               QueueSettingsCard   | In-flight trend
               Active buckets 表   | Channel load (EWMA) 表
             lg 以下: 单列堆叠，该区域整体作为唯一的内部滚动区
```

- 每个卡片 body（表单/图表/表格）= `min-h-0 overflow-y-auto`（或图表 `h-full`），**页级永不滚动**。
- 1080p 高度预算：header≈52 + verdict≈64 + kpi≈96 + gap/padding≈70 → content 区 ≈730px，2 行各≈360px。表体可见约 6 行，趋势图 ~300px。
- 偏离 E mock 之处（一屏适配，需在交付说明中记录）：
  1. buckets + EWMA 由上下堆叠全宽改为 **2×2 并排**；
  2. verdict 的 fixture 长句砍掉 —— 生产文案 = 短标题 + chips（数据驱动，见 §3）；
  3. EWMA 卡片徽标是 **"Live · 3s poll"** 而非 E mock 的 "Follows window"：真实端点是 live-only 3s 轮询（既有契约），不能对用户撒谎。

## 2. 稳定感规则（Hard rules）

1. **无页级滚动**（§1）。
2. **数据刷新零布局位移**（ui-ux-pro-max "Content Jumping" 规则）：
   - verdict `min-h` 固定，色/文案变化只换颜色不改高度；
   - KPI 卡等高（grid `h-full`），描述 `line-clamp-1` + `title` 属性保全文；
   - chips/badges 有固定槽位；表行高固定；所有数字 `tabular-nums`；
   - utilisation = 进度条 + 定宽 % chip 组合槽。
3. **动效预算（只保留这些）**：
   - 卡片 hover 仅 `border-color` 150ms（**禁止 translateY 抬升**——E mock 的 lift/rise 全部砍掉，"稳定"优先，且 frontend-design 明示 per-card hover 动效是 AI 味默认值）；
   - 进度条 width 300ms 过渡（应答数据变化）；
   - live 圆点 pulse：box-shadow-only 2.2s，`aria-hidden`，`prefers-reduced-motion` 下静止。
   - 不新增任何入场动画/错峰动画（沿用页面现有 page-transition 即可）。
4. 语义 tone 切换只影响颜色类，不影响尺寸。

## 3. Verdict band（新组件 `verdict-band.tsx`）

- 结构：`border-l-[7px]` 色轨 + `bg-{tone}/10` 底 + 图标 + 标题（单行）+ 右侧 4 枚 chips。
- tone 判定（优先级，纯函数放 `lib/concurrency-transform.ts`）：
  1. overLimit > 0 → destructive（标题：Action needed — {{n}} buckets over cap 语义）
  2. degraded → warning
  3. rejected > 0 → warning
  4. queued > 0 → warning
  5. 否则 → success（All buckets within caps 语义）
- 4 chips 常驻（数量不变 → 无位移）：`Over-limit {{n}}` / `Rejected {{n}}` / `Queued {{n}}` / Degraded 态；tone：over=destructive、rejected/queued=warning（0 时 muted）、degraded 无=success/有=warning。
- 数据来源：`deriveKpi(scopes)`、`trendRejectionDelta(displayPoints)`、`snapshot.queued`、`snapshot.degraded` —— 与 KPI/趋势同一套源，不新增请求。
- 可访问性：用 `<section aria-labelledby>`，**不要** `role="status"`（= aria-live polite，3s 轮询会刷屏 SR）。
- `Over-limit` 口径 = deriveKpi（仅 key/user，channel 不计）——与其 KPI 卡描述一致。

## 4. 各卡变更

- **header**：右移 window Select + CompactDateTimeRangePicker（custom 时出现，属"应答用户操作"的允许变化）；scope 提示 `KPI, trend and buckets follow this window`；isLive 时静态圆点 + `Streaming` 文案（历史窗口不显示，诚实）。副标题改为 FIX#1：`In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.`
- **KpiCards**：复用 StatCard（不动 Y-3 契约：四卡、标题、描述、tone、loading/error）；描述 FIX#3 → `Number of per-key / per-user buckets currently exceeding their configured cap.`；`line-clamp-1`+title；grid 内 `h-full`。
- **QueueSettingsCard**：整体进 2×2 左上格；body 内容自适应（3 字段可紧凑排列），body `overflow-y-auto min-h-0`；组件内部逻辑/测试不动。
- **TrendChart**：高度由固定 `h-[180px]` 改为填满卡片体（`h-full min-h-[160px]`，ResponsiveContainer height="100%"），空态占位同高；卡头保留 `Queued / Rejected in window` 元信息（合同）。
- **ScopeTable**：utilisation 格 = Progress + 定宽 tone % chip（tone 复用 `utilizationTone`）；卡头加 over-cap badge（计表内所有 kind 的超限行）+ hint `Sorted by utilisation, hottest first`；行高压缩（`h-9` 级）；表体 sticky head + `overflow-y-auto min-h-0`。**该文件 2 个既有 oxlint no-nested-ternary error 必须顺手修掉**（文件被本次改动触及，规范要求 0 error）。
- **RoutingHealthCard**：Multiplier 改填充 chip（>1 success / =1 muted / <1 warning，文案数值不变）；卡头加 live 圆点 + `Live · 3s poll` 徽标；表格 body `min-h-0 overflow-y-auto`（去掉固定 `max-h-[440px]`，交由卡片体裁剪）；空态/错误态分离与脚注合同不变（现有测试必须过）。

## 5. 颜色与排版（只用既有 token）

- 语义：`text-success / text-warning / text-destructive`、`bg-{tone}/10`、`border-{tone}`——全部来自 `theme.css` 既有变量（oklch），**禁止新 hex、禁止毛玻璃/blur、禁止新字体**。
- 数字一律 `tabular-nums`；型号名沿用 `font-mono text-xs`；无全大写 eyebrow 标签（frontend-design 反模式）。
- 两主题（light/dark）都要成立：所有 tint 在 dark 下对比度 ≥4.5:1（web-design-guidelines 阶段复核）。

## 6. i18n

- 所有新文案 `t('English key')`，然后 `cd web && bun run i18n:sync`（禁止手改 locale 硬加 key）。
- 关键新 key（草拟，实施时可微调措辞但须英文源字符串做 key）：
  `Action needed — {{n}} buckets over cap`、`All buckets within caps`、`Over-limit {{n}}`、`Rejected {{n}}`、`Queued {{n}}`、`Backend degraded`、`Backend healthy`、`KPI, trend and buckets follow this window`、`Streaming`、`Live · 3s poll`、`Sorted by utilisation, hottest first`、`{{n}} over cap`、副标题与 FIX#3 描述句。

## 7. 测试与验证（实施方必须跑）

- Verdict 判定纯函数用例 → 追加进现有 `src/features/concurrency/__tests__/concurrency-transform.test.ts`（不新建文件）。
- 布局契约回归：页面 root 不溢出/内部滚动区策略 —— 并入现有测试文件之一（按 web/AGENTS.md §3.14，断言溢出策略等稳定契约，不断言完整 class 串）。
- 现有 5 个 concurrency 测试文件全绿（degraded-badge / queue-settings-card / routing-health-card / transform / queued-display）。
- `bun run typecheck`；`bunx oxlint -c .oxlintrc.json <触碰文件>` 0 error；`bunx vitest run` 触碰文件；`bun run build`；`bun run i18n:sync`。
- **不提交、不 commit**；返回值交付：改动文件清单、新 i18n keys、契约变化（如有）、验证命令与结果。
