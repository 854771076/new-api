# App 级套餐/商品/额度改造：审计修复与本地端到端验收

## Context

本特性（App 级套餐、商品、独立额度池、全局 App 切换器、OAuth 购买/充值）全部尚未提交，baseline = HEAD `b6751e593`。

`ocr` 未配置外部 LLM（AGENTS.md 已记录），因此按技能同样的维度人工审计了完整基线 diff（89 文件 / +7375 −345），并用两个子代理分别审查前端与 CLI/Skill 文档一致性。下面是要修复的真实缺陷，以及随后要执行的本地端到端验收。

## 一、需修复的缺陷

### High

1. **CLI `product update` 与后端 PUT 语义不兼容**
   - `cmd/newapi-cli/internal/commands/app_billing.go` 只发送 `--changed` 字段，后端 `AdminUpdateAppProduct` 是整体替换（`normalizeAppProductRequest` 要求 `app_id>0`、`name` 非空），仅改价格会 400，补全后又会把未传字段清零、`enabled` 回落 true。
   - 修复：新增 `GET /api/app-products/:id`（`controller.AdminGetAppProduct` + `router/api-router.go` + `middleware/access_token_routes.go` 的 `billing:read`），CLI 先 GET 当前记录、合并 changed 字段后再 PUT。
   - 同步更新 `commands_test.go` 与 `.agents/skills/newapi-cli/SKILL.md`（去掉「只发 changed」的描述，改为「先读后写全量提交」）。

2. **商品额度输入单位与订阅套餐不一致**
   - `web/src/features/app-products/lib/product-form.ts` 把 `quota_amount` 当原始 quota 输入，而订阅套餐表单（`web/src/features/subscriptions/lib/plan-form.ts`）用 `quotaUnitsToDollars` / `parseQuotaFromDollars` 以货币展示与录入；列表又用 `formatQuota` 按货币显示，管理员输入 10 实际只入账 10 quota。
   - 修复：product-form 的 schema/defaults/双向转换改为货币单位；drawer 字段标签改为与套餐一致的 `Quota ({{currency}})`。

3. **切换 App 后表格仍保留上一 App 的数据**
   - `web/src/features/app-products/index.tsx:78` 与 `web/src/features/users/components/users-table.tsx:184` 使用 `placeholderData: (prev) => prev`，queryKey 变化后旧行仍在，编辑/删除/调额会作用到旧 App 的行。
   - 修复：改为仅当 `previousQuery.queryKey[1]` 与新 appId 相同时才复用旧数据。

### Medium

4. **App 视图调额弹窗预览使用平台余额**：`users-mutate-drawer.tsx:601` 恒传 `parseQuotaFromDollars(quota_dollars)`；App 视图应传 `currentRow.app_quota`。
5. **订单金额币种错误**：`app-product-orders-dialog.tsx` 用平台展示货币符号，后端订单已返回 `currency` 与 `price_amount`；改按订单币种展示并补 TS 类型字段。
6. **缺少 i18n key `Orders`**：7 个 locale 均无该 key，补齐。
7. **`AppProduct.Enabled` 使用 `gorm:"default:true"`**，违反 AGENTS.md 布尔默认值约定，并导致 `AdminCreateAppProduct` 需要创建后二次 UPDATE。改为在 `normalizeAppProductRequest` 里落默认值，模型改为 `gorm:"not null"`，删除 workaround。
8. **Skill 文档与实现不符**（3 处）：
   - `references/standards/billing.md`：balance 充值实际按 `amount` 换算后的 quota 扣减/入账，不是直接按 amount。
   - `references/standards/identity.md`：`/oauth/userinfo` 的 username/name/email 受 scope 控制。
   - `references/standards/api-contract.md` / `SKILL.md`：`/oauth/topup/checkout` 省略 `payment_method` 时默认 `waffo_pancake`；只有套餐/商品 checkout 才强校验。

### 记录不修（已知限制）

- 平台视图（app_id 未传）返回全部 App 的商品且无归属列；App 选择持久化跨账号残留；待支付商品订单无超时。

## 二、验证

1. 后端：`go build ./...`；`go test ./model/ ./service/ ./cmd/newapi-cli/... -count=1`；`go test ./router/ -run TestAccessTokenRouteRulesCoverEveryDashboardRoute`。
2. 前端：`bun run typecheck`、相关 vitest、`bun run build`（在 `web/`）。
3. 本地端到端（SQLite 临时库，端口 13000）：
   - root 登录 → 建 OAuth App → 建 App 套餐/商品（API + CLI 各一次，CLI 用登录后创建的 PAT 配置）。
   - 建普通用户并充值平台余额 → 走 `/oauth/authorize` + consent + `/oauth/token` 拿 App token。
   - 校验 `/oauth/userinfo` 的 `app_quota`、`/oauth/subscription/plans`、`/oauth/products` 的 App 过滤。
   - `/oauth/topup/checkout`（balance）划转平台余额 → 校验平台余额减少、app_quota 增加。
   - `/oauth/products/checkout`（balance）购买商品 → 校验订单 success 且 app_quota 增加。
   - `/v1/*` 用 App token 调用一次（无可用上游时至少验证鉴权与额度路由）；异步任务接口返回 403 `app_task_unsupported`。
   - 管理端 `GET /api/user/?app_id=1` 返回 `app_quota`，调额接口生效。
   - 服务重启两次，确认迁移幂等、无重复 ALTER。
4. 若 MySQL/PostgreSQL 容器可用，补跑 `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN` 的 `go test ./model/`，并在汇报中写明版本与结果；不可用则明确说明仅完成 SQLite 验证。
