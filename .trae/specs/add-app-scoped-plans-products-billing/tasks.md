# Tasks

- [x] Task 1: 数据模型与迁移
  - [x] 为 `subscription_plans`、`subscription_orders`、`user_subscriptions`、`topups` 增加 `app_id`（默认 0、索引）
  - [x] 新增 `AppProduct`、`AppProductOrder`、`UserAppWallet` 模型与索引/唯一约束
  - [x] 接入 `model/main.go` 的 `AutoMigrate`，确认 SQLite 使用 `ADD COLUMN` 路径、MySQL/PostgreSQL 无方言问题
  - [x] 在 SQLite、MySQL、PostgreSQL 各跑一次全新库迁移和一次“最新发布版本库升级”，每种场景连续启动两次验证幂等
  - [x] 验证旧套餐/订单/订阅/充值升级后 `app_id` 均为 0 且控制台可正常读取

- [x] Task 2: App 额度池原子操作与缓存
  - [x] 实现额度池惰性创建与查询（`(user_id, app_id)` 唯一）
  - [x] 实现预扣、补扣、退款、增加额度、累计 `used_quota` 的原子操作，余额不足返回明确错误且余额不为负
  - [x] 复用 `common/quota_math.go` 的安全加减与饱和逻辑，避免溢出和负数
  - [x] 补充并发表与余额边界测试：并发预扣不超扣、退款幂等、平台钱包（app_id=0）行为不变

- [x] Task 3: Relay 计费接入 App 额度池
  - [x] `RelayInfo` 增加 `AppId`，`middleware/oauth_token_auth.go` 从 access token 写入 App 上下文并加载额度池余额
  - [x] `service/funding_source.go` / `service/billing_session.go` 的钱包资金来源按 `app_id` 路由：0 → `users.quota`，>0 → `user_app_wallets`
  - [x] 订阅预扣 `PreConsumeUserSubscription` 增加 `appId` 过滤；平台请求只看 `app_id = 0`
  - [x] 结算、差额补扣、退款、用量统计全部回到对应额度池
  - [x] 测试：App 余额不足拒绝、平台 Key 不受影响、App A 订阅不抵扣 App B、预扣/结算/退款闭环

- [x] Task 4: 订阅套餐 App 化（后端）
  - [x] 管理端套餐列表支持 `app_id` 筛选；创建/更新 DTO 增加 `app_id` 并校验 App 存在
  - [x] 控制台公开套餐接口只返回 `app_id = 0` 的启用套餐，保持现有用户购买流程
  - [x] 新增 `GET /oauth/subscription/plans`，按 token 所属 App 返回启用套餐
  - [x] 订阅记录写入 `app_id`，购买/续订/重置路径带上 App 归属
  - [x] 测试：A 的套餐不出现在 B、跨 App 购买 403、平台套餐不受影响

- [x] Task 5: App 商品与商品订单（后端）
  - [x] 实现商品 CRUD（管理员），按 `app_id` 筛选，校验价格/额度/限购
  - [x] 实现商品订单创建、查询与状态流转；`trade_no` 唯一
  - [x] 购买校验：商品必须属于当前 App、启用中、未超过每人限购
  - [x] 支付成功后将 `quota_amount` 入账当前 App 额度池，订单幂等
  - [x] 测试：限购、停用、跨 App、重复回调

- [x] Task 6: OAuth 购买、充值与管理端查询接口
  - [x] 新增 `POST /oauth/subscription/checkout`：余额支付走 App 额度池，在线支付复用现有支付服务并写入 `app_id`
  - [x] 新增 `GET /oauth/products` 与 `POST /oauth/products/checkout`
  - [x] 调整 `POST /oauth/topup/checkout` 入账目标为当前 App 额度池
  - [x] `/oauth/userinfo` 增加 `app_quota`、`app_used_quota`，平台 `quota` 语义不变
  - [x] 支付回调按订单 `app_id` 完成订阅/商品/充值入账，校验金额与订单归属
  - [x] 用户列表接口支持 `app_id` 参数，返回该 App 额度字段；管理员额度调整支持 App 维度
  - [x] 测试：scope 不足 403、跨 App 商品/套餐 403、回调金额不符拒绝、重复回调只入账一次

- [x] Task 7: 前端全局 App 切换器
  - [x] 新增 App 状态 store 与 `useActiveApp` hook，默认“平台”，使用 localStorage 持久化
  - [x] 在后台 Header 渲染切换器，仅管理员/超级管理员可见，数据来自现有 `/api/oauth-apps/`
  - [x] 切换时关闭相关页面的编辑弹窗并刷新查询；非涉及页面不消费该状态
  - [x] 测试：可见性、持久化、切换联动、无权限不渲染

- [x] Task 8: 订阅管理与商品管理页面
  - [x] 订阅管理页按切换器传递 `app_id`，列表展示 App 列，新建/编辑表单绑定当前 App
  - [x] 新增商品管理页（路由 `/app-products`），复用现有 DataTable、对话框、表单组件，提供列表、创建、编辑、停用与订单查看
  - [x] 商品表单包含名称、描述、价格、币种、额度、限购、支付渠道商品 ID、排序
  - [x] 处理空态、加载态、错误态与表单校验；切换 App 后清空旧数据
  - [x] 测试：筛选、创建/编辑写入正确 `app_id`、停用后不可购买

- [x] Task 9: 用户管理页 App 适配
  - [x] 用户列表跟随切换器传递 `app_id`，选择具体 App 时展示 App 额度与已用额度列
  - [x] 额度调整弹窗在 App 视图下作用于 App 额度池，操作后刷新列表
  - [x] 选择“平台”时保持现有行为与列布局
  - [x] 测试：筛选结果、额度调整目标、平台视图回归

- [x] Task 10: i18n、验证与审计
  - [x] 为所有新增文案补充 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi` 七个语言包
  - [x] 运行后端 `go build ./...`、相关 `go test`，以及 `GOWORK=off go build ./...`（如触及 `relaykit/`、`tokenkit/`）
  - [x] 运行前端类型检查、lint、构建与相关测试（`bun`）
  - [x] 完成三数据库迁移矩阵记录（版本、命令、结果）
  - [x] 按 `open-code-review` 技能对基线 diff 做审计，修复 High/Medium 问题并记录结论

# Task Dependencies

- Task 3 依赖 Task 1、Task 2
- Task 4、Task 5 依赖 Task 1
- Task 6 依赖 Task 4、Task 5
- Task 7 依赖 Task 6（用户信息/列表接口）与 Task 4/5 的查询接口
- Task 8 依赖 Task 7、Task 4、Task 5
- Task 9 依赖 Task 7、Task 6
- Task 10 依赖全部前置任务
