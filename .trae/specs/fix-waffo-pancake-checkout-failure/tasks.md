# Tasks

- [ ] Task 1: `service/waffo_pancake.go` 预校验支付配置
  - [ ] 1.1 在 `CreateWaffoPancakeCheckoutSession` 调用 SDK 前，校验 `params.ProductID` 非空，为空返回 `common.NewMessage("Waffo Pancake product id is not configured")`。
  - [ ] 1.2 校验 `params.ProductID` 以 `PROD_` 前缀开头，否则返回 `common.NewMessage("Waffo Pancake product id is invalid")`。
  - [ ] 1.3 将 `StoreID` 为空的裸错误改为 `common.NewMessage("Waffo Pancake store id is not configured")`。
  - [ ] 1.4 `gofmt` 该文件，确认 `strings` 已导入、无未使用 import。

- [ ] Task 2: 两个下单控制器透传 `*common.Message`
  - [ ] 2.1 在 `controller/topup_waffo_pancake.go` 的 `generateWaffoPancakeCheckout` 失败分支，用 `var msg *common.Message; if errors.As(err, &msg) { return nil, msg }` 透传，否则回退 `common.NewMessage("Failed to start payment")`；日志仍记录原始 `err.Error()`。
  - [ ] 2.2 在 `controller/subscription_payment_waffo_pancake.go` 的 `createWaffoPancakeSubscriptionOrder` 失败分支做同样处理。
  - [ ] 2.3 `gofmt` 两个文件，确认 `errors` 已导入。

- [ ] Task 3: 验证
  - [ ] 3.1 根模块 `go build ./...` 通过。
  - [ ] 3.2 根模块 `go test ./service/... ./controller/...`（聚焦 waffo_pancake）通过。
  - [ ] 3.3 人工确认：`service.CreateWaffoPancakeCheckoutSession` 的配置错误返回 `*common.Message`，可被 `errors.As` 识别。

# Task Dependencies
- Task 2 依赖 Task 1（依赖 `*common.Message` 类型作为可识别错误被返回）。
- Task 3 依赖 Task 1/2 全部完成。
