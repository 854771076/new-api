# Checklist

- [ ] `CreateWaffoPancakeCheckoutSession` 在调用 SDK 前校验 `ProductID` 非空，为空返回 `*common.Message`（Key「Waffo Pancake product id is not configured」）。
- [ ] `ProductID` 非空但不以 `PROD_` 前缀开头时，返回 Key「Waffo Pancake product id is invalid」。
- [ ] `StoreID` 为空时返回 Key「Waffo Pancake store id is not configured」（不再是裸 `fmt.Errorf`）。
- [ ] `controller/topup_waffo_pancake.go` 的 `generateWaffoPancakeCheckout` 用 `errors.As` 透传 `*common.Message`，非 `*common.Message` 才回退 `Failed to start payment`；原始错误仍写入日志。
- [ ] `controller/subscription_payment_waffo_pancake.go` 的 `createWaffoPancakeSubscriptionOrder` 做同样透传处理。
- [ ] 配置错误（含「Waffo Pancake」关键词）经 OAuth `error_description` 到达前端后，命中已有 `waffo` 映射为 `UPSTREAM_PAYMENT_NOT_CONFIGURED`，而非 `UPSTREAM_CHECKOUT_FAILED`。
- [ ] 真实上游失败（SDK 非 `*common.Message` 错误）仍回退 `Failed to start payment`，前端命中兜底 `UPSTREAM_CHECKOUT_FAILED`，且原始错误在服务器日志可见。
- [ ] 根模块 `go build ./...` 通过。
- [ ] 相关 Go 测试（service/controller 中 waffo_pancake）通过。
- [ ] 未改动计费逻辑、支付配置字段或数据库迁移，未引入新的配置字段。
