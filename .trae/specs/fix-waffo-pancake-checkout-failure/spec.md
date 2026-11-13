# 修复 Waffo Pancake 下单失败：透出真实错误 + 预校验支付配置 Spec

## Why

充值（`/oauth/topup/checkout`）与订阅（`/oauth/subscription/checkout`）两条 Waffo Pancake 下单链路最终都收敛到 `service.CreateWaffoPancakeCheckoutSession`。该函数只在日志里记录真实失败原因，两个控制器把一切失败统一包装成 `common.NewMessage("Failed to start payment")`，于是前端 `checkoutMessage("failed to start payment")` 只能落入兜底，给用户展示「下单失败，请检查余额与支付配置后重试，或联系管理员。」（`UPSTREAM_CHECKOUT_FAILED`）。

真实根因被完全吞掉，运营/用户无法判断是**支付配置错误**（StoreID 未配置、ProductID 未配置或格式错误、MerchantID 不是 `MER_` 短 ID、PrivateKey 无法解析）还是**上游网关故障**（商品未发布、store 未开启生产、网络异常）。当前 `isWaffoPancakeTopUpEnabled()` 只校验「非空」而非「格式合法」，无法阻止错误格式的配置进入 SDK 调用。

## What Changes

- `service.CreateWaffoPancakeCheckoutSession` 在调用 SDK 前预校验 `ProductID`（非空 + `PROD_` 前缀）与 `StoreID`（非空），失败时返回带明确 Key 的 `*common.Message`，而非裸 `fmt.Errorf`。
- 两个下单控制器（`generateWaffoPancakeCheckout`、`createWaffoPancakeSubscriptionOrder`）在捕获到 `CreateWaffoPancakeCheckoutSession` 错误后，用 `errors.As` 透传 `*common.Message`，只有非 `*common.Message`（真实上游失败）才回退到 `Failed to start payment`；原始错误仍写入日志。
- 前端 `checkoutMessage` 已有的 `waffo` 关键词映射由此生效：上述配置错误 Key 均含「Waffo Pancake」，将命中 `UPSTREAM_PAYMENT_NOT_CONFIGURED`，与余额/金额/商品不可购的提示区分开。

## Impact

- Affected specs: Waffo Pancake 支付渠道（topup + 订阅下单）、OAuth 下单错误透出。
- Affected code:
  - `service/waffo_pancake.go`（预校验与错误类型）
  - `controller/topup_waffo_pancake.go`（`generateWaffoPancakeCheckout`）
  - `controller/subscription_payment_waffo_pancake.go`（`createWaffoPancakeSubscriptionOrder`）
  - 前端 `apps/ai-outfit/src/lib/gateway-errors.ts` 无需改动（已有 `waffo` 映射），仅作为验证点。

## ADDED Requirements

### Requirement: ProductID 预校验
系统在创建 Waffo Pancake checkout 会话前，SHALL 校验 `params.ProductID` 非空且以 `PROD_` 开头；StoreID 非空。校验失败时返回 `*common.Message`（Key 分别为「Waffo Pancake product id is not configured」「Waffo Pancake product id is invalid」「Waffo Pancake store id is not configured」）。

#### Scenario: ProductID 为空
- **WHEN** 运营商未配置 `WaffoPancakeProductID`（或计划未配置 `WaffoPancakeProductId`）就发起下单
- **THEN** 返回 `*common.Message`，Key「Waffo Pancake product id is not configured」，不进入 SDK 调用

#### Scenario: ProductID 格式错误
- **WHEN** `ProductID` 非空但不是 `PROD_` 短 ID（例如误填全量 UUID）
- **THEN** 返回 Key「Waffo Pancake product id is invalid」

### Requirement: 下单错误透出
两个下单控制器在捕获 `CreateWaffoPancakeCheckoutSession` 错误后，SHALL 用 `errors.As(err, &msg)`（`var msg *common.Message`）透传可识别的 `*common.Message`，仅对非 `*common.Message` 回退到「Failed to start payment」；无论何种情况均保留原始 `err.Error()` 到日志。

#### Scenario: 配置类错误被透传
- **WHEN** `CreateWaffoPancakeCheckoutSession` 返回 `*common.Message`（配置错误）
- **THEN** 控制器直接返回该 Message，前端命中 `UPSTREAM_PAYMENT_NOT_CONFIGURED`

#### Scenario: 真实上游失败回退
- **WHEN** SDK `Authenticated.Create` 返回非 `*common.Message` 错误（商品未发布/网络异常等）
- **THEN** 控制器返回「Failed to start payment」，前端命中兜底 `UPSTREAM_CHECKOUT_FAILED`，原始错误留在日志供运维定位
