# 06: 子 App 定价与 quote 返回积分，并处理提交前价格变化

**What to build:** App 后端可以用 PAT 获取目标用户在目标 App 的可见定价与积分比例，现有子 App 报价向客户端同时返回预估 quota 和积分，用户在提交前清楚看到预计消耗。

**Blocked by:** 01 — 创建 App 选择双接入模式，并按开关显示配置与切换选项；04 — 管理员配置每个 App 的积分比例，并保留旧 App 显示

**Status:** local implementation — 已实现；本地验证与剩余验收见下文，未发布tracker

- [x] 提供受管理员权限保护的目标用户/App 定价读取，保留原 OAuth pricing 路径及分组、模型可见性规则。
- [x] 扩展现有参考子 App 的 quote 输出，保留已有 quota 字段并增加 estimated_credits 与实际使用的 quota_per_credit；报价调用不要求浏览器持有 PAT。
- [x] 现有顶层 credits: number 实际表示 quota，保持旧语义并标记弃用；新增 estimated_quota 与 estimated_credits: string | null，迁移 StudioClient、类型与相关调用到新字段。旧客户端与新客户端分别验证，不能同名改单位。
- [x] 使用现有服务端业务估算和统一积分换算，不在浏览器重算，不增加通用计价引擎，也不把 quote 作为实际扣费账本。
- [x] 未配置比例时保留旧显示；小于精度的正预估不得呈现为免费。
- [x] 提交时重新验证目标、价格和比例；发生变化返回最新报价供用户确认，不默默按另一份报价解释消耗。
- [x] 确认绑定服务端报价版本/摘要、用户、App、身份来源及输入，不信任客户端自报价格；代理估算仅使用余额资金语义，不混入 OAuth 用户或订阅权益。
- [x] quote、生成结果、历史记录明确区分 estimated、actual、refund 状态；实际扣费与退款只来自网关结算，不复用旧估算值冒充结算结果。
- [x] 报价 UI 复用现有组件，显示积分并保留需要的货币说明，错误保留真实网关状态和原因。
- [x] 在现有 quote 测试边界验证比例差异、变价、非整除、未知实际消耗、无比例与越权；API、示例类型及公开 SDK 契约同步。
- [x] 运行受影响模块的测试、前端检查和构建，完成分支基线审计；若实现产生数据库变更，执行对应三数据库与迁移验证，不因本票主要是报价而豁免。

验收证据对应 checklist D07—D09。此票不依赖钱包 UI 或充值完成。

## 实现与证据（2026-11-19）

实现位置：controller/oauth_pricing.go、controller/app_proxy.go、参考src/lib/tryon/{pricing,quote}.ts、参考generate与StudioClient。

实际结果：参考bun run test：287项通过（包含pricing/quote/生成409重新确认）；参考typecheck/build通过。复杂表达式明确失败，actual/refund保持null直到真实证明。

完整命令、SQLite3.50.4/MySQL8.0.45/PostgreSQL16.15、Supabase PostgreSQL17.11、临时tunnel清理、OWASP/ASVS5.0.0、审计基线与限制统一见 [交付报告](../README.md)。相关实际命令为报告列出的后端focused/matrix/compatibility、根前端24项、参考287项及SDK3项，未运行的外部验收不因实现完成勾选。基线7407f95866b443b7fa040d6ec79f5797c5593219，包含未提交与新增文件；未发布issue/包或部署。
