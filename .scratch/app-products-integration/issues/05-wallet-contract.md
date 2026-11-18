# 05: 按用户与 App 返回统一余额、积分及可信统计

**What to build:** 普通用户能读取自己选定范围的钱包，可信 App 后端可通过管理员 PAT 读取指定用户的同一合同。返回剩余 quota、积分、累计消耗与有明确统计口径的请求数据，客户端直接展示。

**Blocked by:** 01 — 创建 App 选择双接入模式，并按开关显示配置与切换选项；04 — 管理员配置每个 App 的积分比例，并保留旧 App 显示

**Status:** local implementation — 已实现；本地验证与剩余验收见下文，未发布tracker

- [x] 自助读取始终从会话获取本人 ID；管理员指定用户读取验证 user:read 等匹配的现有管理权限，不能让普通 PAT 通过 user_id 参数越权。
- [x] 响应明确 user_id、app_id、quota、used_quota、quota_per_credit、credits、used_credits；未配置比例时积分值为 null。采用独立的规范化钱包读取合同，不改写旧 /oauth/userinfo：新响应 quota/used_quota 只表示响应 app_id 对应范围，积分只由该范围余额与当前 App 比例计算；平台 app_id=0 的比例/积分为 null，保持原货币显示。旧 OAuth quota/used_quota 继续是平台数据，app_quota/app_used_quota 继续是 token 所属 App 数据，不向旧接口混加含义不明的积分字段。
- [x] 响应返回可信身份来源及 capabilities.subscription_supported；代理恒为 false，OAuth 按已有能力。代理订阅接口明确 unsupported，不假装空列表；读取不混入同 App 的 OAuth 用户资产。代理资产读取仅限绑定 App，后台范围筛选独立保留。
- [x] quota 直接代表剩余额度；quota=1000、used_quota=200 时余额仍为 1000，禁止再次相减。
- [ ] 保留真实负 quota 并按 04 返回有符号积分；极小负值换算为 "0" 时 UI 按原始 quota 显示微量欠费，不与无钱包/零余额混淆。不为展示修改预扣/结算语义，覆盖预扣后补扣致负及充值部分抵债。
- [x] 无钱包返回零余额；查询故障返回明确错误，不伪造零；无订阅、无限额与普通余额区分。
- [x] App 请求统计只覆盖本人和该 App，携带可追溯的统计窗口；历史缺失/无法可靠计算时返回 null，不用平台 request_count 填补。
- [x] 新规范化钱包分别通过控制台本人会话、指定用户管理员 PAT、待开发 OAuth api:read 入口提供同一 DTO；OAuth 范围固定为 token App，不携带 dashboard PAT、不允许跨 App。具体路由待实施确定；代理只读其不可变绑定 App，任何失败不回落到平台。
- [x] 平台 quota=9000、App quota=1000、App used_quota=200、比例=100 时，新 App credits="10"、used_credits="2"，不显示 90 或 8；旧 OAuth quota=9000/app_quota=1000 不变，平台新 DTO 比例与积分为 null。两模式、新旧客户端分别回归。
- [ ] 通过 HTTP 请求独立演示平台/A/B 和两用户的不同余额、相同比例结果、未配置比例与无钱包状态，不暴露其他用户信息。
- [ ] 复用用户 App quota / wallet 测试边界；对日志统计、查询及相关日志库执行真实 SQLite/MySQL/PostgreSQL 验证，涉及 ClickHouse 的共用日志读取也验证；给出实际命令、结果与基线审计。

验收证据对应 checklist D04、D06、D12、F02；无需等待新 App Key 即可用现有 App 钱包夹具验证。

## 实现与证据（2026-11-19）

实现位置：model/user_app_wallet.go、controller/app_proxy.go、controller/user.go、router/oauth-app-router.go。

实际结果：TestAppProxyDatabaseMatrix、TestAppProxyHTTPAndFunding、TestOAuthUserInfoReturnsAppQuota通过；原生浏览器profile实际显示10积分/已用2。App请求统计为null，未增加ClickHouse统计查询。

完整命令、SQLite3.50.4/MySQL8.0.45/PostgreSQL16.15、Supabase PostgreSQL17.11、临时tunnel清理、OWASP/ASVS5.0.0、审计基线与限制统一见 [交付报告](../README.md)。相关实际命令为报告列出的后端focused/matrix/compatibility、根前端24项、参考287项及SDK3项，未运行的外部验收不因实现完成勾选。基线7407f95866b443b7fa040d6ec79f5797c5593219，包含未提交与新增文件；未发布issue/包或部署。
