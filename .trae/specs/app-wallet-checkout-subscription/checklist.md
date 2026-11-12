# Checklist

## 收银台直跳（去 iframe）
- [ ] 在线支付下单后跳转收银台 URL，页面无 `<iframe>` 渲染支付页
- [ ] 跳转前 URL 经过 `safePaymentUrl` 校验（仅 https 且无凭据）
- [ ] 余额支付不跳转，原地展示结果
- [ ] 支付弹窗/关闭逻辑清理无残留

## 消费端支付厂商选择
- [ ] 后端 `GET /oauth/payment/methods` 返回「余额 + 已配置在线厂商」，未配置厂商不出现
- [ ] 商品 checkout 支持 stripe/creem/epay/waffo_pancake（与订阅一致）
- [ ] 充值 checkout 支持 stripe/creem/epay/waffo_pancake
- [ ] 客户端移除硬编码 `balance | waffo_pancake`，渲染后端返回的厂商选择控件
- [ ] 选定厂商提交后跳转到对应厂商收银台

## 消费端订阅展示
- [ ] OAuth 用户订阅接口返回套餐名、套餐额度、到期时间
- [ ] 订阅查询仅返回当前 App 归属数据，跨 App 不泄露
- [ ] 消费端个人中心展示套餐名/额度/到期时间，未订阅显示空态

## 后台钱包按 App 筛选
- [ ] 钱包页接入 `useActiveApp`，查询传递 `app_id`
- [ ] 管理端可查看所选 App 的订阅与额度池（`user_app_wallets`）
- [ ] 「平台」视图行为与升级前一致
- [ ] `user-subscriptions-dialog` 的 `getAdminPlans()` 已补传 `appId`

## 自助钱包按 App 展示
- [ ] 自助钱包展示用户在各 App 的订阅与积分/额度
- [ ] 未破坏平台级余额展示

## 质量与安全
- [ ] 新增文案覆盖 en/zh/zh-TW/fr/ja/ru/vi 七语言包
- [ ] 后端构建与相关测试通过
- [ ] 前端类型检查、lint、构建与相关测试通过
- [ ] 管理端接口均要求管理员鉴权；App 身份只取自 access token，不信任请求体 `app_id`
- [ ] 若触及数据库模型/迁移，三库迁移矩阵验证完成并记录
- [ ] 完成基线 diff 代码审计，High/Medium 问题已修复或说明
