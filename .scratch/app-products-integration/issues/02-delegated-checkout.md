# 02: 管理员 PAT 为指定用户打开收银台并查询支付结果

**What to build:** App 后端使用管理员访问令牌，为指定 new-api 用户的平台或 App 钱包创建 Waffo Pancake 充值订单，打开现有收银台，并通过订单查询确认是否到账；不要求该用户另行 OAuth 登录。复用既有 checkout 与 webhook 核心。

**Blocked by:** 01 — 创建 App 选择双接入模式，并按开关显示配置与切换选项；04 — 管理员配置每个 App 的积分比例，并保留旧 App 显示

**Status:** local implementation — 已实现；本地验证与剩余验收见下文，未发布tracker

- [x] 新管理员接口要求明确 user_id、app_id、充值参数及幂等键；app_id=0 为平台，正数为指定 App，缺失/非法范围拒绝。
- [x] 下单与查询分别校验管理员角色、billing:write / billing:read 及目标归属；停用 App/模式拒绝新下单，但不拒绝有权限查询已有订单和对账。普通 PAT、错误 scope、撤销/过期令牌不可调用，原 self 路由仍只能操作本人。
- [x] 按目标用户实际价格组计算支付金额和到账 quota，遵守限额及现有支付合规门槛；客户端传入积分、价格或折扣不能改写结果。
- [ ] 返回订单标识、收银台链接、有效期和目标范围；订单创建时固定目标与金额，可通过服务端查询恢复网络超时结果。
- [x] 原子记录身份来源、user_id、app_id、购买类型、货币、金额、到账 quota 和 quota_per_credit 快照（未配置显式 null）；历史无快照订单按原 quota/货币展示。比例改变后，同幂等键仍返回原订单与原快照。
- [x] 在充值核心上复用已支持的一次性额度商品下单/查询，校验商品 App、限购、金额和重复入账；代理身份只支持自身 App 充值及一次性商品，拒绝订阅、向平台或其他 App 购买，不新增代理订阅接口。平台旧充值行为保留。
- [x] 同幂等键同参数返回原订单，同键改目标/金额返回冲突；无配置与不支持渠道明确失败。
- [x] 回调验证金额、货币、商户、签名与订单状态，重复通知至多入账一次；平台与其他 App 钱包不变。浏览器返回和余额变化均不代替订单成功状态。
- [x] App/模式停用后，停用前创建的有效订单仍按原快照处理真实付款并可查询，不恢复新调用权限；过期订单不复用，过期后真实付款进入可审计异常处理，不静默丢款。覆盖下单后停用、重复回调和查询恢复。
- [x] 操作审计区分操作者和目标，不记录 PAT、支付密钥或可用支付凭证。
- [ ] 在公开 HTTP 边界以两用户、两 App 验证成功、越权、重放、幂等冲突和失败不入账；优先扩展已有支付回归文件。
- [x] 数据库变更/行为在真实 SQLite、MySQL、PostgreSQL 验证；若新增幂等约束或快照字段，覆盖空库、发布版升级及两次启动，记录实际版本/命令/结果。按项目规则记录适用 OWASP 资料与分支基线审计。

验收证据对应 checklist C01—C08；本切片用接口客户端和支付沙箱独立演示，不依赖钱包 UI。

## 实现与证据（2026-11-19）

实现位置：controller/app_checkout.go、model/app_checkout.go、controller/app_proxy.go、各topup/payment回调。

实际结果：TestAppCheckoutRecoveryAndOrderOwnership、TestAppProviderPaymentSnapshots、TestAppProxyDatabaseMatrix通过；真实三DB通过。缺真实商户收银台/付款验收。

完整命令、SQLite3.50.4/MySQL8.0.45/PostgreSQL16.15、Supabase PostgreSQL17.11、临时tunnel清理、OWASP/ASVS5.0.0、审计基线与限制统一见 [交付报告](../README.md)。相关实际命令为报告列出的后端focused/matrix/compatibility、根前端24项、参考287项及SDK3项，未运行的外部验收不因实现完成勾选。基线7407f95866b443b7fa040d6ec79f5797c5593219，包含未提交与新增文件；未发布issue/包或部署。
