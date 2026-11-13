# 每 App 独立支付返回地址（Waffo Pancake Return URL）Spec

## Why

Waffo Pancake 的支付返回地址（Return URL / SuccessURL）当前是**网关级全局单一配置**（`WaffoPancakeReturnURL`），所有外部 app 共用一个值。换装 app（`ai-outfit`）等不同 app 需要各自跳回自己的站点，例如 app A 支付成功后回到 `app-a.example.com/success`，app B 回到 `app-b.example.com/success`。

## What Changes

- 为每个 OAuth app 增加独立的 Waffo Pancake 支付返回地址字段（`waffo_pancake_return_url`）。
- 在 checkout 会话创建时按 app 传入 `SuccessURL`，覆盖商品在 mint 时烧录的全局返回地址。
- 未配置 app 级返回地址时，回退到现有全局 `WaffoPancakeReturnURL`（**向后兼容，非 breaking**）。

## Impact

- Affected specs: `add-app-scoped-plans-products-billing`（同属 app 级计费能力，本 spec 是它的增量）。
- Affected code（后端）：
  - `model/oauth_app.go` — 新增 `WaffoPancakeReturnURL` 字段
  - `controller/oauth_app.go` — Create/Update 请求体与响应体透传新字段
  - `service/waffo_pancake.go` — `WaffoPancakeCreateSessionParams` 增加 `SuccessURL`，并在 `CreateWaffoPancakeCheckoutSession` 中设置 SDK `SuccessURL`
  - `controller/app_product.go` — `CreateAppProductWaffoPancakeCheckout` 解析并传入 app 返回地址
  - `controller/subscription_payment_waffo_pancake.go` — 套餐 checkout 解析并传入返回地址
  - `controller/topup_waffo_pancake.go` — 钱包充值（appId>0）解析并传入返回地址
- Affected code（前端）：
  - `web/src/features/oauth-apps/types.ts`
  - `web/src/features/oauth-apps/`（创建/编辑表单）
  - `web/src/routes/_authenticated/oauth-apps/`
  - `web/src/i18n/locales/*.json`

## ADDED Requirements

### Requirement: 每个 OAuth app 可配置独立支付返回地址

系统 SHALL 在 OAuth app 上存储一个可选的 Waffo Pancake 支付返回地址，并在创建/更新 app 时接受该字段。

#### Scenario: 配置 app 级返回地址

- **WHEN** 管理员在创建或编辑 OAuth app 时填写 `waffo_pancake_return_url`
- **THEN** 该值被持久化到 `oauth_apps` 表，并能通过 app 详情接口读回

#### Scenario: 留空

- **WHEN** 管理员不填写该字段
- **THEN** 字段为空，支付时回退到全局 `WaffoPancakeReturnURL`

### Requirement: Checkout 按 app 传入 SuccessURL

系统 SHALL 在创建 Waffo Pancake checkout 会话时，根据当前 app 解析返回地址，并通过 SDK 的 `CreateCheckoutSessionParams.SuccessURL` 传入，覆盖商品默认返回地址。

#### Scenario: app 级返回地址优先

- **WHEN** 某 app 配置了 `waffo_pancake_return_url`
- **THEN** 该 app 的套餐订阅、商品购买、钱包充值（appId>0）checkout 会话的 `SuccessURL` 使用该 app 值

#### Scenario: 回退全局值

- **WHEN** 某 app 未配置返回地址（空字符串）
- **THEN** checkout 会话使用全局 `WaffoPancakeReturnURL`

#### Scenario: 非 app 场景

- **WHEN** 用户从 dashboard 发起充值（appId=0，无 app 归属）
- **THEN** checkout 会话使用全局 `WaffoPancakeReturnURL`

## MODIFIED Requirements

### Requirement: Waffo Pancake checkout 会话创建

`service.CreateWaffoPancakeCheckoutSession` 的入参 `WaffoPancakeCreateSessionParams` 增加可选 `SuccessURL` 字段，当非空时设置 SDK `sdkParams.SuccessURL`；为空时不设置（维持 SDK 默认行为）。
