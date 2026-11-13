# Tasks

- [ ] Task 1: 消费端收银台直跳（去 iframe）
  - [ ] 1.1 修改 `Payments.tsx`：下单返回 `checkout_url`/`payment_url` 时，经 `safePaymentUrl` 校验后直接跳转收银台（`window.location.assign` 或等价跳转），移除 `<iframe>` 与「在新窗口打开」兜底链接
  - [ ] 1.2 余额支付路径保持原地展示结果，不跳转；关闭/清理原有支付弹窗逻辑 `closePayment`
  - [ ] 1.3 同步更新 `profile.browser.test.mjs` 断言：由断言 iframe `src` 改为断言跳转行为/无 iframe
  - [ ] 1.4 运行 `bunx tsc --noEmit` 与 profile 浏览器测试

- [ ] Task 2: 消费端支付厂商选择（后端 + ai-outfit 前端）
  - [ ] 2.1 后端：新增 `GET /oauth/payment/methods`（`api:read`），按当前 App 与实际配置返回可用支付方式（`balance` + 已配置在线厂商）
  - [ ] 2.2 后端：扩展 `POST /oauth/products/checkout`，支持 `stripe`/`creem`/`epay`/`waffo_pancake`，与订阅 checkout 对齐
  - [ ] 2.3 后端：扩展 `POST /oauth/topup/checkout`，支持 `stripe`/`creem`/`epay`/`waffo_pancake`
  - [ ] 2.4 前端 `profile-data.ts` / `gateway-contract.ts`：将 `PaymentMethod` 改为后端返回的厂商列表，移除硬编码 `balance | waffo_pancake`
  - [ ] 2.5 前端 `Payments.tsx`：渲染支付厂商选择控件（余额 + 在线厂商），提交时传所选 `payment_method`
  - [ ] 2.6 同步 `profile.browser.test.mjs` 断言厂商选择渲染与提交
  - [ ] 2.7 运行 `bunx tsc --noEmit` 与 profile 浏览器测试

- [ ] Task 3: 消费端订阅信息展示（后端 + ai-outfit 前端）
  - [ ] 3.1 后端：让 OAuth 用户信息接口（或新增 `GET /oauth/subscription` self 接口）返回当前 App 的激活订阅：套餐名、套餐额度（`total_amount`）、到期时间（`end_time`）
  - [ ] 3.2 后端：确认 OAuth self 订阅查询只返回当前 token 所属 App 的订阅，跨 App 不泄露
  - [ ] 3.3 前端 `profile-data.ts`：扩展 `userinfoSchema`（或新增订阅 schema）解析订阅字段
  - [ ] 3.4 前端 `ProfileClient.tsx`：新增「当前订阅」卡片，展示套餐名/额度/到期时间，未订阅显示空态
  - [ ] 3.5 同步 `profile.browser.test.mjs` 新增订阅展示断言
  - [ ] 3.6 运行 `bunx tsc --noEmit` 与 profile 浏览器测试

- [ ] Task 4: 后台钱包按 App 筛选（后端 + web 管理端）
  - [ ] 4.1 后端：补充 `user_app_wallets` 查询接口（按 `app_id` 过滤、返回余额/已用额度），管理端鉴权
  - [ ] 4.2 后端：充值记录/订阅管理接口支持 `app_id` 过滤（若缺失）
  - [ ] 4.3 `features/wallet/*` 接入 `useActiveApp()`，钱包/充值记录查询传 `app_id`
  - [ ] 4.4 在钱包页展示所选 App 的订阅与额度池（`user_app_wallets`）
  - [ ] 4.5 修复 `user-subscriptions-dialog.tsx` 中 `getAdminPlans()` 未传 `appId` 的遗漏
  - [ ] 4.6 i18n 补充新增文案（en/zh/zh-TW/fr/ja/ru/vi），运行前端类型检查/lint/build

- [ ] Task 5: 自助钱包按 App 展示订阅与积分（后端 + web 自助钱包）
  - [ ] 5.1 后端：自助接口（如 `/api/subscription/self` 与本人 `user_app_wallets`）返回各 App 订阅与额度
  - [ ] 5.2 `features/wallet/` 自助视图新增「各 App 订阅与积分」区块，复用现有卡片/表格组件
  - [ ] 5.3 i18n 补充文案，运行前端类型检查/lint/build

- [ ] Task 6: 验证与审计
  - [ ] 6.1 后端 `go build ./...` 与相关 `go test`；若触及 `relaykit/`/`tokenkit/` 则 `GOWORK=off go build ./...`
  - [ ] 6.2 前端 `bun run build`、相关测试通过
  - [ ] 6.3 若改动数据库模型/迁移，跑 SQLite/MySQL/PostgreSQL 三库迁移矩阵并记录版本与结果
  - [ ] 6.4 按 `open-code-review` 技能审计基线 diff，修复 High/Medium 问题并记录结论

# Task Dependencies

- Task 2 依赖 Task 1（同为 ai-outfit 前端，可并行；后端厂商列表/checkout 扩展独立）
- Task 3 依赖 Task 1（同属 ai-outfit 前端，可并行）
- Task 4 依赖 Task 1（web 端与 ai-outfit 端独立，可并行）
- Task 5 依赖 Task 4.1/4.2 的后端查询接口，可部分并行
- Task 6 依赖全部前置任务
