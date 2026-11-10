# App 级套餐/商品/额度改造 — open-code-review 复审与修复计划

## Context

用户要求用 `open-code-review` 技能对当前工作区（App 级套餐、统一商品表、App 额度池、OAuth 购买、全局 App 切换器）做审计。

`ocr` 需要外部 LLM provider，本环境未配置（AGENTS.md 已记录该替代流程），因此按其要求在本地直接审查基线差异并套用该技能的审查维度（bug / security / performance / maintainability / test / documentation）与严重级别。

审查范围：`git merge-base origin/main HEAD` = `8faf1d45` 起的分支差异中的本轮特性文件（后端 model/controller/router/middleware/CLI + 前端 app-products、subscriptions、users、全局切换器 + 7 语言 locale）。已逐文件复核：

- 后端：`model/app_product.go`、`model/user_app_wallet.go`、`model/user_quota_adjustment.go`、`model/topup.go`、`controller/app_product.go`、`controller/oauth_app_purchase.go`、`controller/oauth_subscription.go`、`controller/oauth_topup.go`、支付四个 provider 的重构、`controller/user.go`、`controller/user_quota.go`、`middleware/oauth_token_auth.go`、`service/funding_source.go`、`service/billing_session.go`、`router/*`
- 前端：`features/app-products/*`、`features/subscriptions/*`、`features/users/*`、`components/layout/components/active-app-switcher.tsx`、`hooks/use-active-app.ts`、`stores/active-app-store.ts`、路由与 sidebar

结论：无 Critical / High。以下为需要修复的 Medium 与低成本 Low。

## 待修复问题

### Medium 1 — 编辑套餐会把 `app_id` 覆盖为当前全局 App

`web/src/features/subscriptions/components/subscriptions-mutate-drawer.tsx` 提交时调用
`formValuesToPlanPayload(values, appId)`，`lib/plan-form.ts` 无条件写入 `app_id`。
编辑场景下（`isEdit`）应保留 `currentRow.plan.app_id`，否则在列表数据短暂陈旧或 App 切换竞态时，会把属于 App A 的套餐静默改成 App B，导致该套餐被错误 App 的 OAuth 购买入口售卖。

修复：编辑时传 `currentRow?.plan?.app_id ?? appId`，仅新建时使用当前 App。

### Medium 2 — 商品排序在管理端与购买端语义相反

`model/app_product.go`：

- `ListAppProducts`（管理端）：`sort_order desc, id desc`
- `ListEnabledAppProducts`（OAuth 购买端）：`sort_order asc, id asc`

同一字段在两个入口的高低优先级相反，管理员在后台看到的顺序与用户在 App 内看到的顺序不一致。

修复：统一为 `sort_order desc, id desc`（与 `SubscriptionPlan` 管理列表一致，数值大者靠前）。

### Low 3 — 平台判断使用字面量 0

`web/src/features/app-products/index.tsx` 用 `appId === 0` 判断平台视图。改为复用
`useActiveApp()` 的 `isPlatform`（该文件已引入该 hook），避免魔法数字。

### Low 4 — 订单弹窗重复校验响应

`web/src/features/app-products/components/app-product-orders-dialog.tsx` 对
`getAppProductOrders(...)` 再包一层 `requireServerSuccess`，而该 API 函数内部已调用。
去掉外层包装，直接透传。

## 需要修改的文件

| 文件 | 变更 |
| --- | --- |
| `web/src/features/subscriptions/components/subscriptions-mutate-drawer.tsx` | 编辑时保留原 `app_id` |
| `model/app_product.go` | `ListEnabledAppProducts` 排序改为 `sort_order desc, id desc` |
| `web/src/features/app-products/index.tsx` | 用 `isPlatform` 替代 `appId === 0` |
| `web/src/features/app-products/components/app-product-orders-dialog.tsx` | 去掉重复 `requireServerSuccess` |

## 不改动的项（已评估）

- `AppProduct.Enabled` 的 `gorm:"default:true"` 与 controller 的 map 更新：`model` 测试固化了「零值商品默认启用」契约，移除会破坏 `TestAppProductOrderTradeNoUnique`，保留。
- `UpdateAppProductOrderStatus` / `CompleteAppProductOrder` 中 `trade_no` 的手工引号分支：`trade_no` 非保留字，但现有写法在三种数据库下均已通过验证，改动收益低，不动。
- 平台视图商品/订单列表 `app_id = 0` 返回全部 App 数据：与后端文档一致（0 = 不筛选），属预期行为。
- 用户列表 `Unscoped()`：与平台 `GetAllUsers` / `SearchUsers` 的既有实现一致，不是本轮引入的偏差。

## 验证

1. `go build ./...`（根模块）与 `GOWORK=off go build ./...`（`relaykit/`、`tokenkit/` 如有涉及则不适用，本轮未改）。
2. `go test ./model/ -run 'AppProduct|AppWallet'` 与 `go test ./controller/ -run 'AppProduct|Subscription'`。
3. `web/` 下 `bun check -b`；对 4 个改动文件跑 oxlint（仅这些文件，仓库级 lint 噪音来自 `scripts/sync-i18n.mjs`，与本轮无关）。
4. `bun run test`（或 vitest 定向）覆盖 `features/app-products/__tests__`、`active-app-switcher.test.tsx`、`features/users/components/__tests__/quota-display.test.tsx`。
5. 本轮不涉及 schema 变更，无需重跑三库矩阵；沿用上一轮 SQLite/MySQL/PostgreSQL 结论。
6. 复核 7 个 locale 已包含本轮新增 key（已确认存在：`Quota ({{currency}})`、`Active App`、`App Quota` 等）。
