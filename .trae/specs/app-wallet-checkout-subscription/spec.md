# App 钱包筛选、收银台直跳与订阅展示 Spec

## Why
App 化计费（套餐/商品/独立额度池）已上线，但三处体验仍不完整：消费端下单后把支付页嵌在 iframe 里体验割裂；后台与自助钱包看不到「不同 app」的订阅和积分；消费端个人中心完全没有展示用户已订阅套餐的名称、额度和到期时间。

## What Changes
- 商品/套餐在线支付下单：提供支付厂商选择（余额 + 可用在线厂商），选定后从「弹窗内嵌 iframe」改为「直接跳转对应厂商收银台」。
- 后台「钱包」接入全局 App 切换器，按 app 筛选查看充值记录、订阅与额度池；自助钱包展示用户在各 app 的订阅与积分。
- 消费端（ai-outfit）个人中心新增「当前订阅」展示：套餐名、额度、到期时间。
- 涉及后端补充接口：OAuth 用户订阅查询、`user_app_wallets`（App 额度池）查询。

## Impact
- Affected specs:
  - `add-app-scoped-plans-products-billing`（钱包/额度池 app 化，本次补齐钱包页与 `user_app_wallets` 展示）
  - `per-app-waffo-pancake-return-url`（支付跳转与返回 URL 流程，本次改为直跳）
- Affected code:
  - 消费端 `apps/ai-outfit`：`Payments.tsx`、`ProfileClient.tsx`、`profile-data.ts`、`gateway-bff.ts`
  - 后台 `web/src`：`features/wallet/*`、`features/subscriptions/*`、`stores/active-app-store.ts`
  - 后端：`controller/oauth_userinfo.go`、`controller/oauth_app_purchase.go`、`controller/oauth_topup.go`、OAuth 订阅/额度查询、`model/user_app_wallet.go` 查询接口，新增支付厂商列表接口

## MODIFIED Requirements

### Requirement: 在线支付收银台直跳
用户选择在线支付渠道（如 Waffo Pancake）下单成功后，系统 SHALL 直接跳转到支付渠道返回的收银台 URL，而不是在页面弹窗内嵌 iframe。余额支付不产生收银台 URL，保持原地展示结果。

#### Scenario: 在线支付直跳收银台
- **WHEN** 用户在消费端选择在线支付渠道并提交订单，且网关返回 `checkout_url` / `payment_url`
- **THEN** 系统校验该 URL 合法（`https:` 且无凭据）后直接跳转到该收银台地址，不渲染 iframe

#### Scenario: 余额支付原地完成
- **WHEN** 用户选择余额支付
- **THEN** 不产生收银台 URL，保持原地展示支付结果，不跳转

## ADDED Requirements

### Requirement: 消费端支付厂商选择
消费端（ai-outfit）支付 SHALL 提供支付厂商选择：除「余额」外，用户能从后端返回的可用在线支付厂商（如 Stripe、Creem、Epay、Waffo Pancake）中选择其一，而不是硬编码单一在线渠道。可用厂商列表 MUST 由后端按当前 App 与实际配置返回，客户端不硬编码。选定厂商并提交订单后，按「在线支付收银台直跳」规则跳转到该厂商收银台。

#### Scenario: 展示可用支付厂商
- **WHEN** 用户在下单/支付页选择支付方式
- **THEN** 系统展示「余额」加后端返回的可用在线厂商列表，供用户选择

#### Scenario: 厂商列表按配置过滤
- **WHEN** 某在线厂商未配置或未启用
- **THEN** 该厂商不出现在可选列表，客户端不展示不可用厂商

#### Scenario: 选定厂商后直跳
- **WHEN** 用户选择某在线厂商并提交订单
- **THEN** 系统按该厂商创建订单并跳转到其收银台，不渲染 iframe

### Requirement: 后台钱包按 App 筛选
管理后台「钱包」页 SHALL 复用 Header 里已有的全局 App 切换器（`ActiveAppSwitcher` / `useActiveApp`，与用户管理、订阅管理、商品管理是同一个切换器），不新增独立的筛选控件。管理员选择某个 App 后，能看到该 App 维度的充值记录、订阅与额度池（`user_app_wallets`）。选择「平台」时保持现有平台钱包行为。

#### Scenario: 钱包页跟随全局切换器联动
- **WHEN** 管理员在 Header 的全局 App 切换器从「平台」切到 App A，并进入「钱包」页
- **THEN** 钱包页读取同一个 `useActiveApp()` 状态并按 App A 查询充值记录、订阅与额度池，与用户管理/订阅管理/商品管理的联动行为一致

#### Scenario: 管理员按 App 查看额度池
- **WHEN** 管理员在 App 切换器选择 App A，并进入钱包/额度视图
- **THEN** 系统展示 App A 下用户的订阅记录与该 App 的额度池余额/已用额度

#### Scenario: 平台视图回归
- **WHEN** 管理员在切换器选择「平台」
- **THEN** 系统保持现有平台钱包（`users.quota`）展示行为不变

### Requirement: 自助钱包按 App 展示订阅与积分
登录用户在后台自助「钱包」页 SHALL 能查看自己在各 App 的订阅与积分/额度（`user_app_wallets`），不再只显示平台级余额。

#### Scenario: 查看各 App 订阅与积分
- **WHEN** 用户打开自助钱包页
- **THEN** 系统列出该用户在各 App 的订阅状态与该 App 额度池余额/已用额度

### Requirement: 消费端订阅套餐信息展示
消费端（ai-outfit）个人中心 SHALL 展示当前 App 下用户已激活订阅的套餐名、额度和到期时间。未订阅时显示明确空态。

#### Scenario: 展示已订阅套餐详情
- **WHEN** 已订阅用户打开个人中心
- **THEN** 系统展示套餐名、套餐额度、到期时间

#### Scenario: 未订阅空态
- **WHEN** 用户在当前 App 无订阅
- **THEN** 系统展示「暂未订阅套餐」空态，不显示订阅卡片
