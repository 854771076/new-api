# Checklist

## 数据与迁移
- [x] 套餐、订阅订单、用户订阅、充值表均包含 `app_id`（默认 0）与索引
- [x] `app_products`、`app_product_orders`、`user_app_wallets` 表结构与约束符合 spec
- [x] `user_app_wallets` 存在 `(user_id, app_id)` 唯一约束
- [x] 商品订单 `trade_no` 唯一，订单状态流转与幂等逻辑正确
- [x] SQLite、MySQL、PostgreSQL 全新库迁移通过
- [x] SQLite、MySQL、PostgreSQL 升级库迁移通过，旧数据 `app_id = 0`
- [x] 每种数据库连续启动两次迁移保持幂等
- [x] 日志库与 ClickHouse 未被无关修改

## 计费与额度池
- [x] OAuth token 的预扣、结算、退款只作用于所属 App 额度池
- [x] App 额度不足时拒绝请求且余额不为负、不回退平台钱包
- [x] 平台 API Key 仍使用 `users.quota`，行为与升级前一致
- [x] 并发预扣不超扣，补扣/退款幂等
- [x] 订阅预扣按 App 过滤，App A 订阅不抵扣 App B 请求
- [x] 额度运算使用安全转换，无溢出、负数与精度回退

## 套餐、商品与购买
- [x] 管理端套餐按 `app_id` 筛选、创建、更新，App 归属校验生效
- [x] 控制台公开套餐仅含 `app_id = 0`，现有购买流程回归通过
- [x] OAuth 套餐列表只返回当前 App 的启用套餐
- [x] OAuth 商品列表与下单只接受当前 App 的启用商品
- [x] 跨 App 提交 `plan_id` / `product_id` 返回 403 且不产生订单
- [x] 余额支付从当前 App 额度池扣款
- [x] 在线支付成功后的订阅/商品/充值均归属正确 App
- [x] 商品限购次数与停用状态生效
- [x] 支付回调重复时只入账一次
- [x] 支付金额与订单不一致时拒绝完成
- [x] `/oauth/userinfo` 返回 `app_quota`、`app_used_quota`，平台 `quota` 语义未变

## 后台前端
- [x] Header 全局 App 切换器仅管理员/超级管理员可见
- [x] 切换器选项包含“平台”与全部 OAuth App，刷新后保持选择
- [x] 切换 App 时关闭未提交弹窗并刷新相关列表
- [x] 订阅管理页按当前 App 展示与写入
- [x] 商品管理页完成列表、创建、编辑、停用与订单查看
- [x] 用户管理页按 App 筛选，展示 App 额度列
- [x] App 视图下额度调整只修改该 App 额度池
- [x] “平台”视图保持原用户管理行为
- [x] 空态、加载态、错误态与表单校验完整
- [x] 复用现有 DataTable、对话框、表单与共享组件，未重复造轮子

## 安全与质量
- [x] App 身份只取自 access token，请求体/查询参数中的 `app_id` 不被信任
- [x] 管理端接口均要求管理员鉴权
- [x] 订单、订阅、额度操作校验 `user_id` 与 `app_id` 归属
- [x] 日志与审计未记录 token、密钥或完整支付凭据
- [x] 新增文案覆盖 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`
- [x] 数字与金额展示复用现有格式化工具，Intl locale 经过 `toIntlLocale`
- [x] 后端构建与相关测试通过
- [x] 前端类型检查、lint、构建与相关测试通过
- [x] 已完成基线 diff 代码审计，High/Medium 问题已修复或说明
