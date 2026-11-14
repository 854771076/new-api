# Improve-UI Audit: /concurrency 监控页

- Skill: `.agents/skills/improve-ui` (严格三证门:Contract / Runtime / Correction)
- 审计方式:源码追踪(routes → ConcurrencyAdminPage → 子组件)+ 实时页面 DOM 取证(127.0.0.1:3001,2026-10-14)
- 性质:只读审计,未改任何产品源码

## Design language

- Audited surface: `/concurrency` 管理页(`web/src/routes/_authenticated/concurrency/index.tsx` → `features/concurrency/index.tsx`:页头+KPI 行、Bounded wait queue 卡、In-flight trend 卡(含窗口选择器)、Active concurrency buckets 表、Channel load (EWMA routing) 卡)
- Design sources: 仓库无 `DESIGN.md` / design-memory;以 `web/AGENTS.md` 前端规范、`.agents/skills/shadcn-ui` 项目上下文、`web/src/styles/theme.css` token、以及本 feature 内的契约注释(Y-3/Y-4/Y-5,散见于 index.tsx / kpi-cards.tsx / concurrency-transform.ts)为准
- Documented decisions:
  - Y-5 利用率健康色:`limit<=0 → healthy;>1.0 → destructive;>=0.9 → warning`(concurrency-transform.ts:135-147)
  - 窗口/实时双 KPI 不变量:"window mode shows aggregates of the selected time window so **every element on the page describes the SAME data**"(kpi-cards.tsx:49-50,契约 Y-3 谱系)
  - 聚合轴约定:"Cap aggregates stay on the key/user axes to match the KPI labels"(concurrency-transform.ts:102-103)
  - 有界队列实时量按窗口门控的先例:Queued 徽标仅 live 模式渲染(index.tsx:217-223)
- Governing owners and consumers: `StatCard`(dashboard feature)← KpiCards;shadcn `Card`/`Table`/`Progress`/`Select` ← 全部卡片;scope-table 列/空态模式 ← RoutingHealthCard;`@/lib/format` 的 formatPercent / formatDateTimeStr ← 指标格式化
- Explicit exceptions: None documented(无任何来源为下述偏差记录例外;RoutingHealthCard 自身注释仅描述行为,未豁免不变量)

## Findings

| # | Problem | Evidence | Proposed change | Scope | Confidence |
| --- | --- | --- | --- | --- | --- |
| 1 | 页副标题声明范围为 "Per-key and per-user",但同页渲染 channel 行(scope 表 `Channel #{{id}}`)与整张 Channel load 卡——副标题自述范围小于页面实际内容 | Contract:同页用户可见文案自相矛盾(副标题枚举页面范围,页面内容超出该枚举)。Runtime: index.tsx:173 副标题;scope-table.tsx:100-104 channel 行(同一渲染树);index.tsx:249-254 RoutingHealthCard 常驻挂载。Correction:改写该副标题 i18n 键纳入 channel 数据(如 "In-flight request counts per key, per user and per channel. Refreshes every 3 seconds."),`bun run i18n:sync` 同步 7 locale | 页面文案(web/src/features/concurrency/index.tsx + locales) | High |
| 2 | 选择历史窗口时,页面其余元素(KPI/趋势/桶表)切换为窗口语义,唯 Channel load 卡无视窗口选择器持续展示实时 EWMA(含窗口外的 Last sample 时间戳)——违反本页 "every element describes the SAME data" 不变量 | Contract: kpi-cards.tsx:49-50 页面级不变量(Y-3 谱系);同页已确立实时专用数据的处理先例(Queued 徽标按 isLive 门控,index.tsx:217-223);routing-health-card.tsx:28-36 注释自述 "ignores the trend chart's window selector" 且无任何例外记录。Runtime: index.tsx:249-254 卡片无条件挂载,`windowMode` 未传入;卡内 3s 轮询独立于 `useConcurrencyHistory`。Correction:循 Queued 徽标先例,窗口模式(`!isLive`)隐藏该卡(一处条件渲染) | 页面组装(web/src/features/concurrency/index.tsx) | Medium |
| 3 | "Over-limit scopes" 卡描述用泛称 "concurrency buckets",但计算仅含 key/user 轴(渠道桶被跳过),且同排兄弟卡明确写 "per-key / per-user caps"——同一排两卡口径不一,而 scope 表以同一泛称 "Active concurrency buckets" 呈含 channel 的桶 | Contract: 代码内聚合轴约定自述 "match the KPI labels"(concurrency-transform.ts:102-103)+ 同排 kpi-cards.tsx:132 兄弟描述显式限轴,本卡 kpi-cards.tsx:154 未限轴 = 同页口径矛盾。Runtime: deriveKpi 在 kpi-cards 调用前于 concurrency-transform.ts:111-113 `if (s.kind === 'channel') continue`,over-limit 计数物理排除渠道;描述文案原样渲染。Correction:改写 kpi-cards.tsx:154 描述为与兄弟同款轴措辞("Number of per-key / per-user buckets currently exceeding their configured cap.") | KPI 文案(web/src/features/concurrency/components/kpi-cards.tsx + locales) | Medium |

## Improve first

**#1 副标题范围自矛盾**——每个访客都会读到,证据最直接(同页文案互斥),修复成本最低(一个 i18n 键 + 同步),且是后续 design-lab 页面级变体必须先对齐的事实基线。

## 处置说明

按本审计 skill 规则,存活发现本应停下询问"哪几条要出 plan";本次按用户既定流水线执行——问题清单直接作为 design-lab 阶段的输入,不单独出 improve-ui plan。
