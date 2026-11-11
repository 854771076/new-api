# Tasks

- [x] Task 1: 后端数据模型 — `OAuthApp` 新增 `WaffoPancakeReturnURL` 字段
  - [x] 在 `model/oauth_app.go` 的 `OAuthApp` 结构体新增 `WaffoPancakeReturnURL string`（gorm `type:text;column:waffo_pancake_return_url`，json 标签 `waffo_pancake_return_url`）
  - [x] 确认 `AutoMigrate` 会为 SQLite / MySQL / PostgreSQL 三库新增该列（TEXT 列，不建索引）

- [x] Task 2: 后端 API — Create/Update OAuth app 透传返回地址
  - [x] `controller/oauth_app.go` 的 `CreateOAuthAppRequest` 增加 `WaffoPancakeReturnURL string json:"waffo_pancake_return_url"`
  - [x] `UpdateOAuthAppRequest` 增加 `WaffoPancakeReturnURL *string`
  - [x] 创建/更新时写入 `app.WaffoPancakeReturnURL`（更新用 trim，留空回退空字符串）
  - [x] 响应结构体（`OAuthAppResponse`）暴露该字段，供管理端读回

- [x] Task 3: 后端 Service — checkout 会话支持 SuccessURL
  - [x] `service/waffo_pancake.go` 的 `WaffoPancakeCreateSessionParams` 增加 `SuccessURL string`
  - [x] `CreateWaffoPancakeCheckoutSession` 中设置 `sdkParams.SuccessURL = optionalString(params.SuccessURL)`

- [x] Task 4: 后端解析辅助 — 按 app 解析返回地址
  - [x] 新增稳定业务函数 `ResolveWaffoPancakeReturnURL(appID int) string`：appID>0 时查 `OAuthApp`，其 `WaffoPancakeReturnURL` 非空则返回，否则回退 `setting.WaffoPancakeReturnURL`
  - [x] 放置于 `service/waffo_pancake.go`，三个 checkout 调用点复用，表达稳定领域概念

- [x] Task 5: 后端接线 — 三个 checkout 调用点传入 SuccessURL
  - [x] `controller/app_product.go` `CreateAppProductWaffoPancakeCheckout`：用 `product.AppId` 解析并传入
  - [x] `controller/subscription_payment_waffo_pancake.go` 套餐 checkout：用 `plan.AppId` 解析并传入
  - [x] `controller/topup_waffo_pancake.go` 钱包充值：appId>0 用 app 返回地址，appId=0 用全局值

- [x] Task 6: 前端类型与表单 — OAuth app 编辑页新增返回地址输入
  - [x] `web/src/features/oauth-apps/types.ts`：`OAuthApp` 与 `CreateOAuthAppRequest` 增加 `waffo_pancake_return_url`
  - [x] 创建/编辑表单新增「Payment return URL（Waffo Pancake）」输入框，可留空（占位提示回退全局）
  - [x] `web/src/i18n/locales/*.json` 补齐英文源 key（en 为源，zh/zh-TW/fr/ja/ru/vi 补翻译）

- [x] Task 7: 测试与验证
  - [x] 后端单测：`ResolveWaffoPancakeReturnURL` 覆盖 app 优先 / 全局回退 / app 字段空白；checkout 会话入参在传入 SuccessURL 时正确设置 SDK 字段
  - [x] 前端测试：表单字段渲染与提交（含留空）
  - [x] `GOWORK=off go build ./...`（root 模块）
  - [x] `bun check -b`、`bun run lint`、`bun run build`（web）
  - [x] 数据库矩阵：SQLite / MySQL / PostgreSQL 三库 AutoMigrate 新增列验证

# Task Dependencies

- Task 5 依赖 Task 3、Task 4
- Task 4 依赖 Task 1、Task 2
- Task 6 依赖 Task 2（字段契约稳定）
- Task 7 依赖 Task 1-6
