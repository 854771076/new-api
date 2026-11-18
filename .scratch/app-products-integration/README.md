# App 产品接入交付与验收

更新：2026-11-19。开发分支 codex/app-products，worktree：C:/Users/Administrator/Desktop/projects/new-api-app-products。改动尚未提交、发布或部署。本文件替代此前“仅设计/首批开发”的状态记录。

规格见 [spec.md](spec.md)，验收见 [checklist.md](checklist.md)，13张本地ticket见 [issues/](issues/)。代码已覆盖01—12及13的资产入口迁移和证据整理；真实支付、AI上游及完整两用户/两App组合验收未完成，13不能标为全部通过。未创建外部tracker或发布软件包。

## 实现范围

| Ticket | 本地交付 | 边界 |
| --- | --- | --- |
| 01 | 独立模式/整体/广场开关，默认代理；issuer+subject+App原子开户、唯一映射与稳定恢复 | 双模式用户独立；混合Supabase identities拒绝，不合并邮箱 |
| 02 | scoped管理员PAT指定用户充值/商品、持久化幂等请求、稳定订单ID与冻结快照、授权查单 | 代理仅Pancake；无真实商户支付验收 |
| 03 | App个人Key的创建/更新/撤销/阶段轮换，relay固定App钱包 | 有限额轮换明确拒绝，不复制预算；未知额度规则字段拒绝 |
| 04 | nullable安全正整数比例、十进制字符串、六位向零截断、欠费显示 | 不改变quota账本、模型价格或历史订单 |
| 05 | 会话/PAT/OAuth统一钱包DTO、来源与能力、余额不重复减已用 | 无可靠请求统计时为null；旧userinfo语义保留 |
| 06 | 定价携带比例，quote增加estimated_quota/estimated_credits，服务端摘要绑定用户/App/输入/价格 | 旧credits仍为quota；复杂表达式明确不支持；实际账务无证明时为null |
| 07 | 管理展示配置、公共安全DTO、导航广场、稳定排序和HTTPS外链 | 无运营推荐或SSO |
| 08 | 普通用户App选择、按账户隔离、主钱包统计切换、关闭OAuth隐藏、历史只读入口 | 无权范围拒绝，不回退平台 |
| 09 | 当前App订阅/商品主区域、真实货币价格与能力控制、既有支付核心 | 代理无订阅；线上渠道未全矩阵付款验收 |
| 10 | 同范围预览/充值/历史/精确订单确认；OAuth固定App订单接口与参考BFF关联 | 只按订单终态确认；旧快照/货币缺失不伪造 |
| 11 | 后台独立App筛选、来源与账户归属、零钱包用户、搜索/分页/排序/total和额度操作 | OAuth/平台旧账户保留资金视角 |
| 12 | 原生Supabase登录→BFF→加密个人App Key；私有RLS、活session、owner初始化；SDK/OpenAPI/skill/bootstrap同步 | 实际原生登录/余额/admin/退出与真实RLS通过；未真实AI或支付 |
| 13 | 旧底部汇总迁为主区域与独立历史入口；汇总证据与审计 | 完整外部组合验收待凭据，不宣称77项全通过 |

## 实际验证

命令从本worktree根运行；前端/SDK/参考命令从对应目录运行。`.local-tests/`含实际日志与本机隔离夹具，已gitignore；凭据不能提交。

### 网关与真实三数据库

设置自己的测试 `TEST_MYSQL_DSN`、`TEST_POSTGRES_DSN` 后运行：

```text
go test ./controller ./model ./middleware ./service -run '^(TestApp|TestOAuth(Product|Subscription|Topup|Purchas)|TestUsersForApp|TestPreConsumeUserSubscriptionAppIsolation|TestSetupContextForToken)' -count=1
go test ./controller -run '^TestApp(ProductSettingsDatabaseMatrix|ProxyDatabaseMatrix)$' -count=1 -v
go test ./model ./controller -run '^(TestTaskAccessorsScopeByApp|TestOAuthUserInfoReturnsAppQuota|TestAdjustUserAppQuota|TestManageUserQuotaAcceptsAppId|TestPlatformUserListRemainsUnchanged|TestPurchaseSubscriptionWithBalanceAppScope|TestUserAppWalletQuotaOperations)$' -count=1
go build ./...
```

全部通过，日志分别为 `focused-final.log`、`database-matrix-final.log`、`compatibility-final.log`、`backend-build-final.log`。集中新增后端测试在 `controller/app_product_settings_test.go`，其余扩展已有测试。

实际版本：SQLite **3.50.4**、MySQL **8.0.45**（本机13316）、PostgreSQL **16.15**（本机15436）。两个controller矩阵的 **12个fresh/upgrade子用例均运行，无跳过**；focused命令也包含原model App矩阵。升级使用 **v2026.11.12.4** 发布结构与代表性App/用户/Key数据，连续迁移两次，验证旧归属默认、唯一约束、配置与资产保留。新增模式/身份/Key操作/checkout表、原子恢复、余额与订单快照、非法金额及历史货币读取覆盖三引擎。未引入依赖最低版本的新SQL特性，未在MySQL5.7.8/PostgreSQL9.6重复矩阵，不外推为全版本实测。

日志schema未变，规范化App请求统计为null，没有新增ClickHouse统计SQL；未在独立ClickHouse验收既有日志读取。relaykit/tokenkit未改动，独立模块构建不适用。更宽后端全套曾遇Windows audit.db句柄导致TempDir清理失败；未宣称全套通过，上述针对性合同回归及根构建通过。

### 前端、参考与SDK

| 目录 | 实际命令及结果 |
| --- | --- |
| web | `bun check -b`、36个改动TS/TSX的oxlint、保留原版权头的oxfmt检查、`bun run build`通过 |
| web | `bun run test src/features/oauth-apps/__tests__ src/features/app-marketplace/__tests__ src/components/layout/components/__tests__/active-app-switcher.test.tsx src/features/wallet/__tests__/app-checkout.test.ts`：**5文件24项通过** |
| 参考outfit-demo | `bun run test`：**19文件287项1061断言通过**；`bunx tsc --noEmit`、`bun run build`通过；lint 0error/6warning。必须用serial mocks的test script，不用裸bun test替代 |
| sdk/typescript | `bun test`：**3项9断言通过**；`bun run build`通过，验证固定App/PAT隔离、请求键、路径与完整定价元数据 |
| 合同/skill | OpenAPI **24paths/26local refs**全部解析；两份PS bootstrap AST与Git bash `-n`检查通过；skill quick_validate通过 |
| i18n/秘密 | 七语言**69个新增英文键**及占位符一致；**1886个客户端产物**未匹配测试PAT/service secret/加密密钥/个人AI Key。仅证明本轮夹具无匹配，不是通用秘密检测认证 |

前端复用原选择器、React Query、钱包/订阅卡、用户DataTable、ConfirmDialog、公共状态和布局；新增App业务组合及必要个人Key面板，不引入第二套基础UI。构建保留既有consent测试路由导出提示。

### 原生Supabase和浏览器

独立本机栈：Supabase CLI **3.1.0**，project_id **newapi-app-products-native**，实际PostgreSQL **17.11**。无远程project ref，没有操作既有Toonflow栈。实际命令：

```text
supabase start --workdir .local-tests/supabase-native
supabase db reset --workdir .local-tests/supabase-native
supabase db push --local --workdir .local-tests/supabase-native
supabase db push --local --workdir .local-tests/supabase-native
python scripts/verify-native-rls.py
cloudflared tunnel --url http://localhost:56321
cloudflared tunnel --url http://localhost:3019
cloudflared tunnel --url http://localhost:3029
```

reset仅作用于本轮独立本地栈。原生迁移、重复push无待迁移；真实Auth/RLS脚本验证私有表权限、session归属、跨session拒绝、native owner初始化、立即logout撤销。浏览器实际登录studio、读取profile、owner访问admin、退出后admin跳回login，全程无OAuth跳转。测试余额夹具quota1000/used200/ratio100显示余额10积分、已用2积分，无订阅区域；**夹具不是支付证据**。

本次临时HTTPS主机（只记录当次证据，不可用于部署）：

- Supabase：`movement-speaker-carried-wonder.trycloudflare.com`
- 网关：`generating-chance-problems-legacy.trycloudflare.com`
- 参考App：`floppy-pictures-sense-cooling.trycloudflare.com`

代理模式未创建OAuth provider/redirect，APP_ORIGIN和Supabase site配置均为隔离夹具。浏览器session已关闭、native临时用户已删除；三个tunnel与3019/3029服务已停止。执行 `supabase stop --workdir .local-tests/supabase-native` 并还原原本地config，执行 `docker stop newapi-app-products-mysql newapi-app-products-postgres`；保留隔离数据供复现。没有修改线上来源、redirect或生产用户。日志 `native-rls-final.log`、`native-cleanup.log`。

## 认证与分支审计

按open-code-review的bug/security/performance/maintainability/test/documentation维度直接审阅。基线 `git merge-base codex/quota-management HEAD` = **7407f95866b443b7fa040d6ec79f5797c5593219**；覆盖设计提交、未提交修改及未跟踪新增文件，共 **168文件**，清单 `.local-tests/baseline-review-files.json`。范围含网关认证/账户/Key/钱包/支付/迁移、根前端、参考App/RLS、SDK/OpenAPI、七语言、设计/skill。OCR实际执行后因 `no valid LLM endpoint configured` 失败，按项目规则改为完整差异直接审阅，没有要求另配provider。

已读OWASP Authentication、Session Management、OAuth2、CSRF Cheat Sheets，采用 **ASVS5.0.0** 适用资源授权与撤销要求（10.3.2、10.4.9），保留精确回调/PKCE/client验证（10.4.1、10.4.6、10.4.10）。增加服务端角色/scope、不可变归属、verified user+live session、私有AES-GCM凭据、可信Origin严格比较、模式关闭/撤销检查。证据是HTTP回归、真实三DB、Auth/RLS和原生浏览器流程；不声称整体ASVS合规。

收尾修复 **High2、Medium5，Critical0**：

- High：Epay充值回调未核对冻结金额；签名成功后核对金额/CNY，不符进入reconciliation_required，不入账。
- High：Stripe充值把购买单位当实际金额；校验真实Price并冻结单价×数量，限定整数量、USD单次单价，禁用改变冻结应付金额的促销码。
- Medium：反向代理公共Origin与Next内部localhost不一致导致原生登录失败；仅比较可信APP_ORIGIN，恶意Host/Origin回归拒绝。
- Medium：未知Key规则字段被静默丢弃；改严格allowlist，不支持的额度规则明确失败。
- Medium：历史订单错误假定USD；仅使用可证明的原货币，未知显示暂无数据，不套当前比例；NaN/Inf/负金额写快照前拒绝，三DB回归。
- Medium：SDK pricing丢失比例等元数据且订单路径允许特殊片段；保留完整合同并严格订单ID校验。
- Medium：参考UI没有付款方式仍可确认且误称余额支付；明确空态并禁用提交。

此前还修复共享App缓存被选择器污染、模式保存/refresh撤销非原子、重新开启自动换secret、估算冒充实际账务等问题，不重复计入上述7项。当前未留已知未解释Critical/High/Medium代码缺陷；下面外部验收缺口仍有效。

## 未完成验收与限制

- 缺实际支付沙箱商户和AI上游凭据，不能演示付款→精确订单成功→同一钱包→真实同步/流式/异步AI消耗完整组合。模拟provider、测试余额、构建通过均不能替代。
- 未做七语言所有页面视觉/深浅主题全矩阵、生产多节点真实Redis、所有在线渠道、公网OAuth重定向；已做文案键/占位符与局部浏览器检查。
- 有限额Key轮换未实现共享预算，按设计明确拒绝；未重写周期/窗口引擎；未知规则字段拒绝。代理支付仅Pancake，不支持订阅。
- 未运行独立ClickHouse集成，未改其schema。后端全套Windows清理问题未修复；针对性回归不能替代全套绿灯。
- 改动本地未提交，未发布tracker/npm/skill包、未建PR/合并/部署、未替换外部子App线上登录。
