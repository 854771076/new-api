# App 产品接入交付与验收

更新：2026-11-19。开发分支 codex/app-products，worktree：C:/Users/Administrator/Desktop/projects/new-api-app-products。主体实现已本地 checkpoint 并 rebase 到 main 32ff3ba5a；本轮轮换兼容修复和有效期规格尚未提交。未推送、发布或部署。本文件替代此前“仅设计/首批开发”的状态记录。

规格见 [spec.md](spec.md)，验收见 [checklist.md](checklist.md)，13张本地ticket见 [issues/](issues/)。代码已覆盖01—12及13的资产入口迁移和证据整理；真实支付、AI上游及完整两用户/两App组合验收未完成，13不能标为全部通过。未创建外部tracker或发布软件包。

本轮新增 [配额规则有效期 spec](quota-rule-validity-spec.md)，目前只设计；不将其记为已实现。下文“实际验证”及原审计保留上轮证据，本轮重新验证见文末。

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
- 上轮验收时改动尚未提交；本轮本地提交/rebase 状态见文末。未发布tracker/npm/skill包、未建PR/合并/部署、未替换外部子App线上登录。

## 2026-11-19 main rebase 与有效期规格

### 分支与改动

- fetch 后 main/origin/main 为 **32ff3ba5a**（per-token periodic quota rules and weekday access controls），已完成 rebase，无 push。
- 原实现 checkpoint **a8b318b57**，恢复分支 **codex/app-products-pre-main-20261119**；rebase 后 HEAD **b57383a6b**，设计提交 **6956a920c**。
- 手动解决 Token 与七语言共 8 个文件冲突；同时保留不可变 AppId、QuotaRuleId/QuotaRule/QuotaRuleUsed 和双方文案。七语言解析及全部键值与两边来源逐项比较通过。两边原有的 No matches found 重复键未在本轮扩大或清理。
- `git range-diff 7407f95866b443b7fa040d6ec79f5797c5593219..codex/app-products-pre-main-20261119 32ff3ba5a..HEAD` 确认设计补丁相同，主体补丁仅 Token 上下文/对齐与语言尾部上下文变化；其余自动合并没有丢失 App 逻辑。
- 本轮兼容修复：绑定规则的无限额 Key 也按有限预算处理，拒绝阶段轮换；提交轮换时锁定原 Key 再检查，防止新 Token ID 获得新的周期预算。已有三 DB 集中矩阵补充创建拒绝、用量保持和阶段创建后绑定再提交拒绝的回归。未实现规则绑定管理的新 API，因此不把这项保护描述为完整代理会员管理。
- 本轮新增规格包括绝对 [开始,结束) 区间、指定代理用户规则 API、创建 Key 时原子绑定、未完成轮换与绑定互斥、时间状态 UI、在途账务及既有规则兼容。**上述有效期功能尚未开发**。测试边界已获用户明确确认。

### 本轮实际命令与结果

以下命令在本 worktree 执行，测试凭据只在进程环境和被忽略的本地夹具，不进入源码。App 矩阵使用 TEST_MYSQL_DSN/TEST_POSTGRES_DSN；计费矩阵使用自己声明的 TEST_FIXED_MYSQL_DSN/TEST_FIXED_POSTGRES_DSN 和独立 TEST_FIXED_MYSQL_LOG_DSN/TEST_FIXED_POSTGRES_LOG_DSN。

| 命令 | 结果与日志 |
| --- | --- |
| `go build ./...` | 通过；rebase-build.log |
| `go test ./controller -run '^TestApp(ProductSettingsDatabaseMatrix\|ProxyDatabaseMatrix)$' -count=1 -v` | 两矩阵共 12 个 fresh/upgrade 子用例通过，无跳过；rebase-app-matrix.log |
| `go test ./service -run '^TestFixedPriceBillingDatabaseMatrix$' -count=1 -v` | SQLite/MySQL/PostgreSQL 全部通过，无跳过；含 token quota rules、独立日志库；rebase-billing-matrix.log |
| `go test ./controller ./model ./middleware ./service -run '^(TestApp\|TestOAuth(Product\|Subscription\|Topup\|Purchas)\|TestUsersForApp\|TestPreConsumeUserSubscriptionAppIsolation\|TestSetupContextForToken\|TestTokenQuotaRule)' -count=1` | 四包通过，含规则授权/审计、日历/校验、App model 真实三 DB 及旧 OAuth 合同；rebase-focused.log |
| web：`bun run typecheck` | 1,458 文件、2 项目无类型错误；rebase-typecheck.log |
| web：`bun run test src/features/oauth-apps/__tests__ src/features/app-marketplace/__tests__ src/components/layout/components/__tests__/active-app-switcher.test.tsx src/features/wallet/__tests__/app-checkout.test.ts src/features/quota-rules/__tests__` | 6 文件 27 项通过；rebase-web-tests.log |
| web：`bun run build` | 通过；rebase-web-build.log |
| `git diff --check`；规格模板及 31 条 user stories 自检 | 通过 |

实际数据库版本：SQLite **3.50.4**、MySQL **8.0.45**、PostgreSQL **16.15**。App 矩阵复用发布版代表性升级夹具并连续迁移两次；本轮增加规则表测试夹具，生产规则 schema 沿用 main，没有引入有效期字段。未重复最低版本 MySQL5.7.8/PostgreSQL9.6 或独立 ClickHouse，不宣称全版本验收。

计费矩阵第一次误用 App 矩阵的环境变量，只有 SQLite 运行；已按其实际声明的 TEST_FIXED_* 重跑三引擎及独立日志库，上表只使用重跑结果。未以跳过结果声称三 DB 通过。

### 本轮审计与余项

使用 open-code-review 维度审计，比较基线为 `git merge-base origin/main HEAD` = **32ff3ba5a**。分支差异 **168 文件**，加本轮新规格为 **169 文件**。保留此前 168 文件完整审阅结果，并用 range-diff 核对补丁等价；本轮直接复审全部 **14 个 main/App 重叠文件**、规则模型/控制器/计费交互、轮换修复与回归以及新规格。清单和差异见被忽略的 rebase-baseline-review-files.json、rebase-range-diff.log、rebase-intersections.diff。不会把这项增量复审描述为重新逐行读过全部未变文件。

实际运行 `ocr review --audience agent --format json --background '<rebase and quota context>' --from 32ff3ba5a --to HEAD --output .local-tests/rebase-ocr.json`，因 **no valid LLM endpoint configured** 失败；按项目约定直接审阅，没有要求配置 provider。覆盖 bug/security/performance/maintainability/test/documentation。新增发现 **High 1**（轮换导致新 Key 周期预算重新开始）已修复；**Critical 0、其他已确认 Medium 0**。规格审阅另外补齐首次 Key 原子绑定、旧 PUT 缺省时间保护、未结束轮换与绑定互斥，属于后续功能验收合同。

本轮从 OWASP 官方 CheatSheetSeries 源文读取 Authentication、Session Management，服务端保留身份/范围检查、撤销和无秘密审计；ASVS 参考仍为 **5.0.0**。新增保护及测试针对凭据轮换与预算连续边界，没有新增会话协议，也不声称整体 ASVS 合规。完整认证要求和原 ASVS 条目验证见上轮审计记录。

本轮没有重新跑 Supabase 公网浏览器、支付沙箱、真实 AI、参考 App/SDK 全构建，或整个后端全套；原外部验收缺口不变。新有效期、代理规则 API/UI、多 Key 共享预算仍不属于已交付能力。当前代理自建会员只能作为未来集成方案，不能把 subscription_supported 改为 true。

未配置目标 issue tracker/triage 词表，spec 仅保存在本地；可运行 `/setup-matt-pocock-skills` 后发布到获授权目标，不向项目仓库提交禁止的 Coding Plan issue。临时 MySQL/PostgreSQL 测试容器在验证后停止；既有其他容器不变。

### 有效期规格审计修复（2026-11-19）

后续 open-code-review 直接审阅发现 **Medium 2**，本次只修订规格及本交付记录，未新增生产代码、数据库字段或接口实现：

- **在途预算快照**：明确扩展原预留的版本、限额开关和额度上限，复用原 UsageID；异步任务在提交上游前持久化。补充预扣按冻结上限原子更新原用量行，不再依赖已修改或删除的规则。补充降额/提额、删除后补扣、共享原行并发及重启恢复验收。历史无快照预留可继续结算/退款，新增正向预扣明确失败，不能猜上限或变成无限额。
- **轮换取消终态**：明确 Cancelled 与 Committed 的互斥状态、cancel-rotation 管理接口、替换 Key 禁用和状态原子提交；取消后允许原 Key 绑定。定义重复取消、提交/取消竞争、多个 pending 操作、失败回滚及重启恢复，禁止取消后幂等创建重放、提交或重新启用替换 Key；App 关闭后仍支持有权管理员收尾。

规格现有 **35 条 user stories**，测试边界仍采用已确认的管理 API/relay 与浏览器入口；未来三 DB 验收同时覆盖新增终态与任务快照兼容。上述验收是开发要求，没有以规格检查代替运行证据。

收尾继续使用 open-code-review 维度，基线仍为 main **32ff3ba5a**；本次直接审阅新增规格全文、README 增量及预留/轮换依赖合同，复用前述已核对的分支差异，没有重新逐行审阅 169 个文件。OCR 按基线实际运行，仍因 `no valid LLM endpoint configured` 失败，日志 spec-fix-ocr.log；改为直接审阅，两个已报告 Medium 的规格缺口已补齐，未发现本次修订新增的 Critical/High/Medium 合同冲突。校验七段模板、35 条故事连续编号、相关本地链接及 `git diff --check`。本次没有重跑应用测试/构建或数据库矩阵，没有提交或推送；预算快照扩展与轮换取消也仍待未来实施和真实验收。

## 2026-11-19 有效期功能开发交付

本节取代前面规格阶段“尚未开发”的当前状态说明；前文保留为历史记录。代码位于 `new-api-app-products` worktree 的 `codex/app-products`，基于 main **32ff3ba5a**；本轮变更尚未提交、推送或部署。

### 实现范围

- 规则新增 nullable `valid_from` / `valid_until`，UTC Unix 整数秒，执行 `[开始,结束)`。旧 PUT 省略日期时在事务锁内保留，显式 null 清空；免费、无限额、信任用户、同步/流式/任务入口均受启用规则约束。稳定拒绝码为 `rule_not_started` / `rule_expired`，不重试。
- 管理员 PAT 可在固定 App 下管理指定代理用户规则、绑定 Key，首次创建 Key 可原子绑定。规则、Key、App 与目标用户归属由服务端校验；只读 scope 不能修改。停用 App 后仍允许查询和清理。
- 预算预留冻结版本、上限与原 UsageID，在途补扣不读修改后的模板；真实结算不裁剪，退款归原用量行。无快照旧任务保留结算/退款，新增正向补扣返回 `reservation_snapshot_unavailable`。异步任务发送上游前持久化私有快照，发布任务和 App 已用额度同事务完成。
- 取消轮换具备持久化终态，替换 Key 禁用与取消标记同事务提交；失败回滚、重复取消、提交竞争、取消后创建重放/提交/启用拒绝、旧缓存拒绝均有回归。pending 轮换阻止原 Key 绑定，绑定规则的 Key 不支持阶段轮换。
- 既有规则编辑、列表、Key 摘要和代理用户 Key 面板接入日期、时间状态、影响数量与移除限制提示。复用 DateTimePicker、Dialog、ConfirmDialog、QuotaDetailsPopover 和规则页面；日期组件原先只处理浏览器本地时间，兼容扩展显式 UTC/Shanghai 编辑，未另造日期组件。七语言新增键放在真实 translation 命名空间。
- SDK、OpenAPI、参考 App 管理 BFF 与接入 skill 同步。BFF 由已验证原生用户映射固定网关用户/App，普通消费者不能修改会员门槛。默认推荐代理接入；双模式用户保持独立，代理 `subscription_supported=false`。

### 实际验证

命令均在该 worktree 执行，日志放在被忽略的 `.local-tests/`。数据库连接凭据仅进程环境使用，未写入源码。App 矩阵使用 `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN`；计费矩阵使用前文声明的四个 `TEST_FIXED_*` 变量。

| 实际命令或操作 | 观察结果与日志 |
| --- | --- |
| `go build ./...` | 通过；quota-validity-build-backend-final.log |
| `go test ./controller ./model ./middleware ./service ./relay -run '^(TestApp\|TestOAuth(Product\|Subscription\|Topup\|Purchas)\|TestUsersForApp\|TestPreConsumeUserSubscriptionAppIsolation\|TestSetupContextForToken\|TestTokenQuotaRule\|TestQuotaValidity\|TestFreeModelEnforces\|TestPaidZeroEstimate\|TestExecuteTaskSubmission\|TestRealtimeFailureRefunds)' -count=1` | 五包通过；quota-validity-focused-final.log |
| `go test ./controller -run '^(TestAppProxyDatabaseMatrix\|TestAppProxyQuotaManagementAuthorization)$' -count=1 -v` | SQLite/MySQL/PostgreSQL fresh/upgrade 六组合均通过，无跳过；含重复迁移、旧规则/用量/轮换数据、并发补扣、绑定竞争、取消竞争及失败回滚、旧缓存拒绝、真实 BillingSession 钱包补扣与任务发布不重复记账；quota-validity-accounting-final.log |
| `go test ./service -run '^TestFixedPriceBillingDatabaseMatrix$' -count=1 -v` | 三库及独立日志库通过，无跳过；quota-validity-billing-matrix.log |
| `go test ./controller -run '^TestExecuteTaskSubmissionPersistsQuotaContractBeforeSending$' -count=1 -v`，分别设置 `TEST_TASK_DB_DIALECT=mysql` / `postgres` | 两引擎均通过；提交前持久化及持久化失败不发送上游；quota-validity-task-mysql.log / quota-validity-task-postgres.log。SQLite 同项亦通过 |
| web：`bun run test src/features/oauth-apps/__tests__ src/features/app-marketplace/__tests__ src/components/layout/components/__tests__/active-app-switcher.test.tsx src/features/wallet/__tests__/app-checkout.test.ts src/features/quota-rules/__tests__ src/components/__tests__/datetime-picker-time.test.tsx src/features/users/components/dialogs/__tests__/app-proxy-keys-dialog.test.tsx` | **8 文件 46 项通过**；quota-validity-web-final.log。包括七语言真实资源、历史夏令时、最大日期、时区保持瞬间、分页绑定、取消终态 |
| web：`bun run typecheck`、`bun run build` | 1,460 文件/2 项目类型通过，构建通过；quota-validity-types-final.log / quota-validity-build-final.log |
| web：`bun x oxlint -c .oxlintrc.json <全部分支变更及新增 TS/TSX 文件>` | 通过，无输出；quota-validity-lint-changed.log。全项目 `bun run lint` 失败于既有未改文件，如 rankings、assets 和 sync-i18n；未将其描述为通过 |
| 参考 outfit-demo：`bun run test`、`bun run build` | **289 项 / 19 文件通过**，生产构建通过；quota-validity-reference-tests.log / quota-validity-reference-build.log |
| sdk/typescript：`bun run build`、`bun test` | 构建通过，**4 项通过**；OpenAPI 使用已安装 yaml 解析并校验 **36** 个本地引用全部可解析 |
| agent-browser + 本地 gateway3039 / web5189 | 实际创建并保存上海时间，切 UTC 后保持绝对时刻，重开保持；清空开始并保存过去结束后显示已过期；倒置区间显示中文错误且未保存，Escape 关闭。直接读取一次性 SQLite 核对开始为 NULL、结束为1767225600；quota-validity-browser-final.log |
| `git diff --check` | 通过 |

实际数据库版本：SQLite **3.50.4**、MySQL **8.0.45**、PostgreSQL **16.15**。新建与升级使用真实引擎，迁移各执行两次，校验原用量/取消默认与既有 committed/pending 数据；外部数据库并发用四连接。未使用版本专有 SQL，不宣称最低版本 MySQL5.7.8/PostgreSQL9.6 或 ClickHouse 全矩阵验收。未改 relaykit/tokenkit，独立模块构建不适用。

### 收尾审计

使用 open-code-review，基线 `git merge-base origin/main HEAD` = **32ff3ba5a**，包含 HEAD 加全部工作区变更和新增文件，共 **199 文件**。清单 quota-validity-review-manifest.json、完整已跟踪基线差异 quota-validity-final-baseline.diff；沿用此前168文件审阅及 rebase 等价核对，对本轮 **57 文件增量**和相关认证/支付/账务依赖直接复审，不把它声称为本轮重新逐行读过199文件。

实际 OCR 命令 `ocr review --audience agent --format json --background '<App products / quota validity / frozen reservations / rotation cancellation context>' --from 32ff3ba5a --to HEAD --output .local-tests/quota-validity-ocr.json` 因 **no valid LLM endpoint configured** 失败，按项目要求直接审阅，并额外覆盖未提交/新增内容。检查 bug/security/performance/maintainability/test/documentation，确认 **Critical0、High0、Medium6**，均已修复：翻译错误命名空间；日期最大值/历史DST转换；创建绑定下拉只列前100条；取消后UI幂等操作残留；MySQL相同快照重试被更新行数0误判；仅限时不设周期上限的规则快照丢失首次预留Amount。MySQL问题先在真实MySQL复现失败，再三库回归通过；仅限时快照补保留初始金额，并加入三库回归，便于SUBMITTING原资金对账。没有未解释的已知 High/Medium 代码缺陷。

适用 OWASP Authentication / Session Management Cheat Sheets 已读取，ASVS **5.0.0** 资源授权及撤销边界参照 **10.3.2、10.4.9**，既有 OAuth 协议控制参照上节条目。新增验证涵盖无认证/非管理员/只读/跨用户跨App/伪造归属、取消终态重放、服务端缓存拒绝、BFF Origin/管理员校验与审计无秘密。未改变登录协议；测试不是完整 ASVS 合规证明。

### 保留的验收缺口与运行限制

- 无实际支付沙箱商户或AI上游凭据，未验收“会员付款→权益发放→真实AI消耗”完整闭环。规则不发资金，App仍须独立维护会员订单并保证钱包余额；每Key独立预算，不能视为多Key共享会员额度。
- 提交上游后进程中断、结果不确定时，保留 **SUBMITTING** 私有快照供运维对账，不自动重发或退款。没有新增自动恢复或人工对账UI；应核对上游实际结果后处理原钱包/用量。
- 已做规则页面真实浏览器流程；代理用户/App切换的完整浏览器设备/主题矩阵、生产多节点Redis和新公网Supabase回调未在本轮重跑。参考BFF权限与范围以行为测试验证。
- 未跑整个后端全套（此前Windows临时audit.db清理问题仍存在），也未清理全项目既有lint错误；通过的是上述针对性回归与全部分支变更文件lint。
- 无提交、推送、PR、部署或外部tracker发布；本地临时浏览器、gateway/web服务和本轮MySQL/PostgreSQL测试容器收尾停止，既有其他容器保持。


## 2026-11-19 基线审计两项 P2 修复

- OAuth App 缓存补齐 nullable `quota_per_credit`：数据库读入、内存命中、Redis 序列化/反序列化均保留比例；Redis key 升至 v3，避免沿用缺字段旧缓存。商品、套餐与定价接口在冷/热缓存下保持相同积分契约，清空配置后仍返回 null。
- App 订阅复用现有 SubscriptionPurchaseDialog 与 Combobox，发送用户所选具体易支付类型。共享服务端只接受已配置类型，仍保留 `epay` 默认首个类型的旧合同；其他支付渠道分支及代理禁止订阅边界保留。重复请求返回原订单，不重复创建支付单。同步 skill 的 api-contract.md / billing.md。

验证（命令均在本 worktree；未提交、推送或部署）：

- 修复前 `go test ./controller -run '^TestApp(CreditCachePreservesRatio|SubscriptionSelectedEpayMethod)$' -count=1` 确认失败，日志 `.local-tests/fix-regression-before.log`；前端真实组件交互测试确认回调错误发送 epay，随后修复转绿。
- `go test ./controller -run '^TestApp(ProductSettingsDatabaseMatrix|CreditCachePreservesRatio|SubscriptionSelectedEpayMethod)$' -count=1 -v` 通过，日志 `.local-tests/fix-matrix.log`。三库为 SQLite 3.50.4 / MySQL 8.0.45 / PostgreSQL 16.15，fresh/upgrade 六组合与重复迁移均通过，无跳过。新缓存用例覆盖内存及 miniredis、配置值/清空、冷/热请求；支付用例覆盖 wxpay/alipay/旧 epay/未配置类型及幂等重试。这两项新增 API 回归使用 SQLite；未声称其在每个引擎均独立运行。
- `go test ./controller ./model ./middleware ./service ./relay -run '^(TestApp|TestOAuth(Product|Subscription|Topup|Purchas)|TestUsersForApp|TestPreConsumeUserSubscriptionAppIsolation|TestSetupContextForToken|TestTokenQuotaRule|TestQuotaValidity|TestFreeModelEnforces|TestPaidZeroEstimate|TestExecuteTaskSubmission|TestRealtimeFailureRefunds)' -count=1` 在两个外部 DB DSN 已设置环境中五包通过，日志 `.local-tests/fix-regression-after.log`。
- `go build ./...` 通过。
- web：`bun run test src/features/subscriptions/components/dialogs/__tests__/subscription-purchase-dialog.test.tsx src/features/wallet/__tests__/app-checkout.test.ts` 2 文件 5 项通过；`bun check -b` 通过（1,461 文件），涉及两文件 `bunx oxlint -c .oxlintrc.json ...` 通过；`bun run build` 通过，保留已有 OAuth 测试文件路由扫描警告。
- `git diff --check` 通过。

使用 open-code-review skill 收尾。基线仍为 `32ff3ba5acb90ca4a88a0e994879b20669c8a0ca`（origin/main 与 HEAD 的 merge base）；实际执行 `ocr review --audience agent --format json --background '<App baseline and credit cache / Epay fixes>' --from 32ff3ba5acb90ca4a88a0e994879b20669c8a0ca --to HEAD --output .local-tests/fix-baseline-review.json`，因 `no valid LLM endpoint configured` 失败。依项目规定直接审阅：复用前一轮全基线审计证据，本轮重点复核 service/oauth_app_policy.go、controller/oauth_app_purchase.go、controller/app_product_settings_test.go、订阅购买对话框及其新增测试、skill 两份合同文件，共 7 文件，并沿 scoped checkout、订单快照、易支付建单和钱包表单提交调用链核对。两项已报告 Medium/P2 均修复，未发现新增 Critical/High/Medium；不声称本轮重新逐行审过整个分支。

本轮未使用真实支付商户或完成付款回调，未重跑公网 Supabase/真实 AI 端到端、最低数据库版本或生产多节点 Redis。没有更改认证流程、数据库模型或 SQL；先前记录的外部验收缺口仍然适用。验证后停止本轮启动的 MySQL/PostgreSQL 测试容器。
