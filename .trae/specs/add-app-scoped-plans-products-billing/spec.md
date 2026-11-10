# App 级套餐、商品与独立额度池 Spec

## Why

当前订阅套餐、充值商品、钱包额度都是全站唯一的一套：所有外部 App 看到同一批套餐和商品，用户余额也跨 App 共用，无法让不同 App 独立定价、独立售卖、独立核算。同时后台缺少 App 维度入口，管理员只能看到混合数据，表单也无法指定归属 App。

## What Changes

- **App 成为计费租户**：订阅套餐、商品、订单、用户额度池全部增加 `app_id`（`0` 表示平台/控制台）。
- **新增三张表**：`app_products`（App 商品）、`app_product_orders`（商品订单）、`user_app_wallets`（用户在各 App 的独立额度池）。
- **套餐按 App 隔离**：每个 App 只能看到并购买自己的套餐；平台套餐只服务于控制台/平台 API Key。
- **独立额度池**：OAuth access token 发起的 relay 请求从该 App 的额度池预扣与结算；平台 API Key 继续使用 `users.quota`。
- **OAuth 购买链路**：新增套餐/商品列表与下单接口，外部 App 内可直接展示并购买；充值进入该 App 的额度池。
- **后台全局 App 切换器**：后台 Header 增加下拉切换，控件状态持久化；订阅管理、商品管理、用户管理跟随切换器筛选与写入 App。
- **BREAKING**：OAuth token 的 relay 余额来源从 `users.quota` 改为 App 额度池。升级后 App 用户初始额度为 0，需要先购买套餐/商品或充值；平台 API Key 行为不变。

## Impact

- Affected specs：`oauth-provider`、`add-multi-tenant-apps`（复用 App 概念，不实现 App Admin Key）、订阅与钱包相关能力
- Affected code：
  - 模型与迁移：`model/subscription.go`、`model/oauth_app.go`、`model/user.go`、`model/topup.go`、`model/main.go`
  - 鉴权与计费：`middleware/oauth_token_auth.go`、`relay/common/relay_info.go`、`service/billing.go`、`service/billing_session.go`、`service/funding_source.go`、`service/quota.go`、`service/log_info_generate.go`
  - 接口：`controller/subscription*.go`、`controller/oauth_*.go`、`controller/topup*.go`、`controller/user.go`、`router/api-router.go`、`router/oauth-provider-router.go`
  - 前端：`web/src/components/layout/**`、`web/src/stores/**`、`web/src/features/subscriptions/**`、`web/src/features/users/**`、新增 `web/src/features/app-products/**`、`web/src/i18n/locales/*.json`

## 数据模型

| 表 | 变更 |
| --- | --- |
| `subscription_plans` | 新增 `app_id int not null default 0`（索引），`0` = 平台套餐 |
| `subscription_orders` | 新增 `app_id int not null default 0`（索引） |
| `user_subscriptions` | 新增 `app_id int not null default 0`（索引），冗余自套餐，便于筛选 |
| `topups` | 新增 `app_id int not null default 0`（索引），`0` = 平台钱包 |
| `app_products`（新） | `id`、`app_id`、`name`、`description`、`price_amount`、`currency`、`quota_amount`、`enabled`、`sort_order`、`max_purchase_per_user`、`stripe_price_id`、`creem_product_id`、`waffo_pancake_product_id`、`created_at`、`updated_at` |
| `app_product_orders`（新） | `id`、`user_id`、`app_id`、`product_id`、`money`、`trade_no`（唯一）、`payment_method`、`payment_provider`、`status`、`provider_payload`、`create_time`、`complete_time` |
| `user_app_wallets`（新） | `id`、`user_id`、`app_id`、`quota`、`used_quota`、`created_at`、`updated_at`，唯一索引 `(user_id, app_id)` |

约束：

- `app_products.quota_amount > 0`；`price_amount >= 0`；`max_purchase_per_user >= 0`（0 = 不限）。
- 商品 v1 语义固定为“一次性购买后向该 App 额度池增加 `quota_amount`”，不引入通用商品类型枚举。
- 全部金额字段沿用现有 `float64` 金额风格；额度使用整数 quota 单位，写入前通过 `common/quota_math.go` 的安全转换，不出现负数或溢出。
- 日志库（含 ClickHouse）不在本次改动范围，`logs` 表结构不变。

## ADDED Requirements

### Requirement: App 维度数据模型与迁移

系统 SHALL 为套餐、订单、用户订阅、充值增加 `app_id` 字段，并新增 App 商品、商品订单、用户 App 额度池三张表；`app_id = 0` 表示平台数据。迁移 MUST 同时兼容 SQLite、MySQL、PostgreSQL，并在全新数据库和由最新发布版本升级的数据库上各执行两次启动迁移且保持幂等。

#### Scenario: 旧数据升级后仍可用

- **WHEN** 使用最新发布版本创建的数据库升级到本次版本
- **THEN** 原套餐、订单、用户订阅、充值记录的 `app_id` 均为 `0`，控制台行为与升级前一致

#### Scenario: 重复启动不重复改表

- **WHEN** 连续两次启动服务
- **THEN** 第二次启动不产生新的 schema 变更，数据、索引、唯一约束保持不变

#### Scenario: 新表在三种数据库上可用

- **WHEN** 分别在 SQLite、MySQL、PostgreSQL 上执行迁移并读写新表
- **THEN** 字段类型、索引、唯一约束均生效，无方言相关报错

### Requirement: App 独立额度池

系统 SHALL 为 `(user_id, app_id)` 维护独立额度池 `user_app_wallets`。OAuth access token 发起的 relay 请求 MUST 只使用该 token 所属 App 的额度池进行预扣、结算和退款；平台 API Key（无 App 上下文）MUST 继续使用 `users.quota`。额度池行在首次需要时惰性创建，初始额度为 0。

#### Scenario: App 额度不足

- **WHEN** 用户在本 App 额度池余额为 0，通过该 App 的 OAuth token 发起 AI 请求
- **THEN** 预扣失败并返回余额不足错误，不发生任何扣减，也不回退到平台钱包

#### Scenario: 并发生成不超扣

- **WHEN** 同一用户在同一 App 上并发发起多笔预扣
- **THEN** 所有预扣都以原子条件更新执行，累计扣减不超过额度池余额，不出现负余额

#### Scenario: 平台 API Key 不受影响

- **WHEN** 用户使用控制台创建的 `sk-` token 发起请求
- **THEN** 仍从 `users.quota` 预扣与结算，不读写任何 App 额度池

#### Scenario: 结算与退款归位

- **WHEN** 一次 App 请求预扣后实际用量更低、更高或请求失败
- **THEN** 差额补扣或退回到同一 App 的额度池，记录幂等，不串到其他 App

### Requirement: App 级订阅套餐

系统 SHALL 让每个订阅套餐归属一个 App（或平台）。管理端 MUST 能按 `app_id` 筛选、创建、更新套餐；外部 App MUST 只能看到并购买 `app_id` 等于自身 App 的套餐。订阅预扣 MUST 只使用同一 App 的订阅（平台请求只使用 `app_id = 0` 的订阅）。

#### Scenario: 套餐列表按 App 隔离

- **WHEN** App A 的 OAuth token 请求套餐列表
- **THEN** 只返回 `app_id = A` 且启用的套餐，不包含 App B 或平台套餐

#### Scenario: 跨 App 越权购买被拒绝

- **WHEN** App A 的 token 提交 App B 的 `plan_id` 下单
- **THEN** 返回 403，不创建订单、不扣款、不产生订阅

#### Scenario: 订阅不跨 App 抵扣

- **WHEN** 用户持有 App A 的有效订阅，通过 App B 调用模型
- **THEN** App B 的请求不会消耗 App A 的订阅额度

#### Scenario: 平台套餐仍可用于控制台

- **WHEN** 管理员在后台创建 `app_id = 0` 的套餐
- **THEN** 控制台用户仍能购买和使用该套餐，外部 App 看不到它

### Requirement: App 商品与商品订单

系统 SHALL 提供 App 商品管理能力，商品归属一个 App；用户购买成功后，系统 MUST 按订单金额与商品配置向该 App 的额度池入账，并记录商品订单。购买 MUST 校验商品归属、启用状态与每人限购次数；支付回调 MUST 幂等。

#### Scenario: 购买商品到账 App 额度池

- **WHEN** 用户通过 App A 购买启用中的商品 10 元 / 100000 quota 并支付成功
- **THEN** App A 的额度池增加 100000 quota，生成成功订单，平台钱包不变

#### Scenario: 达到限购次数

- **WHEN** 商品配置每人限购 1 次，用户已成功购买 1 次后再次下单
- **THEN** 返回业务错误，不创建支付订单

#### Scenario: 支付回调重复

- **WHEN** 支付平台对同一 `trade_no` 重复回调
- **THEN** 只入账一次，订单状态保持成功，额度池不重复增加

#### Scenario: 商品停用

- **WHEN** 商品被停用后用户仍提交下单
- **THEN** 返回业务错误，不创建订单

### Requirement: OAuth 套餐/商品购买与充值

系统 SHALL 提供以下 OAuth 接口，App 身份 MUST 从 access token 解析，禁止从请求体或查询参数接收 `app_id`：

- `GET /oauth/subscription/plans`（`api:read`）：返回当前 App 的启用套餐
- `POST /oauth/subscription/checkout`（`api:write`）：`{plan_id, payment_method}`，余额支付从当前 App 额度池扣款
- `GET /oauth/products`（`api:read`）：返回当前 App 的启用商品
- `POST /oauth/products/checkout`（`api:write`）：`{product_id, payment_method}`，返回支付链接或跳转信息
- `POST /oauth/topup/checkout`（`api:write`，已有）：充值入账目标改为当前 App 额度池

`GET /oauth/userinfo` 在 `api:read` 下 SHALL 增加 `app_quota` 与 `app_used_quota`；原 `quota` / `used_quota` 继续表示平台钱包，避免破坏已有调用方。

#### Scenario: 外部 App 内完成购买

- **WHEN** App A 的用户在 App A 内选择 App A 的套餐并选择在线支付
- **THEN** 返回可用的支付链接，支付成功后订阅与额度归属 App A

#### Scenario: 使用 App B 的商品 ID

- **WHEN** App A 的 token 提交 App B 的商品 `product_id`
- **THEN** 返回 403，不创建订单

#### Scenario: 用户信息返回双额度

- **WHEN** App A 的 token 携带 `api:read` 请求 `/oauth/userinfo`
- **THEN** 响应同时包含平台 `quota` 与 App A 的 `app_quota`、`app_used_quota`

### Requirement: 后台全局 App 切换器

后台控制台 SHALL 在 Header 提供全局 App 切换器，仅管理员与超级管理员可见。选项包含“平台”和全部 OAuth App；选择结果持久化，刷新后保持。切换器 SHALL 只影响本次涉及页面（订阅管理、商品管理、用户管理），其他页面不因切换器改变行为。

#### Scenario: 切换 App 后列表联动

- **WHEN** 管理员在订阅管理页把切换器从“平台”切到 App A
- **THEN** 订阅列表只展示 App A 的套餐，新建/编辑表单写入 `app_id = A`

#### Scenario: 刷新保持选择

- **WHEN** 管理员选择 App A 后刷新页面
- **THEN** 切换器仍显示 App A，相关列表按 App A 筛选

#### Scenario: 非管理员不可见

- **WHEN** 普通用户登录控制台
- **THEN** Header 不渲染 App 切换器，也无法访问相关管理接口

#### Scenario: 切换时关闭未提交表单

- **WHEN** 管理员在套餐编辑弹窗未保存时切换 App
- **THEN** 弹窗关闭，列表按新 App 刷新，不把旧 App 的数据写入新 App

### Requirement: 用户管理按 App 筛选

用户管理页 SHALL 跟随全局 App 切换器：选择具体 App 时，列表只展示在该 App 有额度池记录的用户，并展示该 App 的 `quota` / `used_quota`；额度调整弹窗 SHALL 作用于该 App 额度池。选择“平台”时恢复现有平台用户与 `users.quota` 行为。

#### Scenario: 筛选 App 用户

- **WHEN** 管理员在用户管理页选择 App A
- **THEN** 只列出在 App A 有额度池记录的用户，并显示 App A 额度列

#### Scenario: 调整 App 额度

- **WHEN** 管理员在 App A 视图下给某用户增加额度
- **THEN** 只修改该用户 App A 的额度池，平台 `users.quota` 不变

#### Scenario: 无记录用户不出现

- **WHEN** 用户从未使用过 App A
- **THEN** 在 App A 视图下不展示该用户

### Requirement: 越权与支付安全

所有 App 归属校验 MUST 在服务端完成：App 身份只来自 access token；套餐、商品、订单、额度池的读写都必须校验 `app_id` 与 `user_id` 归属。管理端接口 MUST 使用管理员鉴权。支付回调 MUST 校验订单状态与金额，并保持现有签名/验签与幂等逻辑；日志与审计 MUST NOT 记录 token、密钥或完整支付凭据。

#### Scenario: 伪造 app_id

- **WHEN** 调用方在下单请求体或查询参数中传入其他 `app_id`
- **THEN** 该字段被忽略或拒绝，实际归属仍由 token 决定

#### Scenario: 支付回调篡改金额

- **WHEN** 回调金额与订单金额不一致
- **THEN** 订单不完成、不入账，并记录不含敏感信息的安全日志

## MODIFIED Requirements

### Requirement: OAuth relay 鉴权上下文

`middleware/oauth_token_auth.go` 在验签成功后 SHALL 解析并写入当前 `app_id`，加载该 App 额度池余额作为 token 额度上下文，同时继续执行 scope 校验与模型白名单校验。额度池不存在时 SHALL 按 0 额度处理并惰性创建。

### Requirement: 订阅预扣与结算

`model.PreConsumeUserSubscription` 及其调用链 SHALL 增加 `appId` 维度：只匹配 `user_subscriptions.app_id` 与请求 App 一致的订阅；平台请求使用 `app_id = 0`。结算、退款与重置逻辑保持现有幂等语义，不得跨 App 抵扣。

### Requirement: 充值入账

平台充值保持 `app_id = 0` 并计入 `users.quota`；OAuth 充值与 App 支付回调 MUST 计入对应 `user_app_wallets`，订单与流水中保留 `app_id`。

## REMOVED Requirements

无。
