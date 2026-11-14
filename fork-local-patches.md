# fork-local-patches

本文件登记所有 `[fork-bridge]` 提交(per `.agents/rules/loc-rule.md` §6.1)。提交作者必须在打 `[fork-bridge]` 前缀时同步添加条目。

| 日期 | 分支 | 文件 | 行号 | 原因 | 上游 issue/PR | remove-when |
| --- | --- | --- | --- | --- | --- | --- |
| 2026-09-24 | newapi0924 | `model/main.go::addTokenConcurrencyColumn` | `~656-695` | 0924 镜像启动时 PostgreSQL 报 SQLSTATE 42601(双 DEFAULT 子句);同一函数在 MySQL 8 strict 模式下拒绝 TEXT DEFAULT ''。原 O-1 提交(`5d5db3494`)声称"三库验证通过"但实际未覆盖 addTokenConcurrencyColumn 这条代码路径(test 在 migration 包内联了自己的 SQL,从未调用本函数)。 | 待向 QuantumNous/new-api upstream 反馈 | upstream 修复 addTokenConcurrencyColumn 的 SQL 生成逻辑(检查 caller 已约束时不重复追加 DEFAULT,MySQL+TEXT 不带 DEFAULT),并合并 release |
| 2026-10-15 | newapi924-1015 | `web/src/components/ui/combobox-input.tsx` + `__tests__/combobox.test.tsx` | 整文件(3 提交 `d54165b12`/`683f31618`/`455b04d4b`) | fork UI 修复:对话框内 Combobox 弹层不可见(容器成为 fixed 包含块)→容器约束定位+向上翻转+偏移换算自纠偏;3 提交递进修复双值振荡。upstream 下拉定位实现不同,需评估后才能反馈。 | 待评估向 QuantumNous/new-api upstream 反馈 | upstream 统一 Combobox 弹层定位策略并合并 release,或该弹层实现被替换 |
| 2026-10-15 | newapi924-1015 | `controller/usedata.go`、`model/usedata.go`、`web/.../models-filter-dialog.tsx`、`web/src/features/dashboard/api.ts`(`6dd672c8c`) | 函数级(ListDistinctTokenKeys/GET /api/data/token-keys/筛选对话框) | fork 本地功能:看板令牌 Key 下拉按时间范围聚合调用次数与 tokens,window 参数透传+7 语言 i18n+契约测试。非上游已有需求。 | 待向 QuantumNous/new-api upstream 提需求(或保持 fork 功能) | 产品裁决为永久 fork 功能则不移除;上游实现同等能力后移除 |
| 2026-10-19 | newapi924-1015 | `controller/relay.go`、`relay/common/relay_info.go`、`relay/responses_request.go`、`relay/{alpha_search,audio,claude,compatible,embedding,gemini,image,rerank}_handler.go`、`controller/relay_model_mapping_retry_test.go`(新)、`relay/helper/model_mapped_test.go`(新)+ 已存在 commit `5e4f55f45` 与 `1ff8ed374`(均无 `[fork-bridge]` 前缀) | 函数级(`ModelMappedHelper` 重定位、`InitChannelMeta` 的 mapped-name 保留、`responses_request.go:40` 注释) | 用户反馈 channel #21 上 zhipu_4v / tyy_maas_kimi-k2.5 等 scenario:重试选到带不同 model_mapping 的 channel 后上游 body 仍带原始 client 虚拟名 → 上游 404 模型不存在。根因拆成两步:(1) `controller.Relay()` 把 `helper.ModelMappedHelper` 从函数顶部(在 retry loop 之前)挪到 loop 内 `getChannel` 之后;(2) `InitChannelMeta` 不再把 `info.ChannelMeta.UpstreamModelName` 重置为 `ContextKeyOriginalModel`,并且不再 `info.Request.SetModelName(info.OriginModelName)`——这两条 reset 在旧"每个 handler 各调一次 ModelMappedHelper"pattern 下是必要的前置清理,在 controller loop 集中调用 pattern 下变成对映射的破坏性回滚。补 e2e 回归测试 `TestRelayWireBodyCarriesRetryChannelModelMapping`(saturated 强制重试→断言上游 body 含 channel B 的映射名)。**既往 commit `5e4f55f45` 与 `1ff8ed374` 缺 `[fork-bridge]` 前缀与 # FORK-LOCAL-EXTENSION 注释——既已推送到分支不可 amend,作为本次新提交统一登记,后续审计 `scripts/fork-local-patches-audit.sh` 可一并扫描。** | 待向 QuantumNous/new-api upstream 反馈(根因解释 + 最小补丁) | upstream 接受任何一条:(a) 把 `ModelMappedHelper` 调整到 controller 重试 loop 内并修 `InitChannelMeta` 不再覆盖;(b) 或上游分支选择/重试逻辑完全重写,移除当前 `InfoChannelMeta` 的 reset 设计;(c) 或从设计上把 mapped-name 持久到 request 体时跳过 `InfoChannelMeta` 的 request 重置。三选其一合并 release |
| 2026-10-20 | newapi924-1015 | `common/body_storage.go`(新增 `RewriteBodyJSONModel`)、`middleware/task_plugin_model.go`(改为委托)、`relay/{compatible_handler,claude_handler,rerank_handler,image_handler}.go`、`relay/responses_request.go`、`controller/relay_passthrough_model_mapping_test.go`(新) | 函数级(`RewriteBodyJSONModel` + 5 处 pass-through 分支接入) | patch 4 修复的是「转换分支」上的映射丢失,但开了 `pass_through_body_enabled=true`(渠道级)或全局 `PassThroughRequestEnabled=true` 的渠道走的是另一条路:`common.GetBodyStorage(c)` 取客户端原始字节直接发出,`request.Model` 与 `info.UpstreamModelName` 写得再对也不落到 wire 上,上游同样返回 404「模型不存在」。语义澄清:pass-through 的承诺是「跳过协议转换、保留客户端 body 结构」,不等于「跳过模型寻址」——model_mapping 决定请求打到上游哪个模型,属路由寻址,必须落到 wire。实现上把 `middleware/task_plugin_model.go` 里已有的 `rewriteTaskPluginJSONModel`(sjson 改顶层 model + 替换 BodyStorage)提升为 `common.RewriteBodyJSONModel`,5 处 pass-through 分支共用同一实现,避免第二份重复。非 JSON body(multipart/form-data 图片上传、音频裸流)由 helper 内部按 Content-Type 判定后原样跳过。AWS 渠道刻意不改:`relay/channel/aws/relay-aws.go` 的 pass-through 分支本就 `delete(data, "model")`(AWS 用 URL 寻址),无需下发模型名。回归测试 `TestPassThroughChannelStillSendsConfiguredModelMapping` 已验证:修复前 FAIL(上游收到原始虚拟名),修复后 PASS。 | 待向 QuantumNous/new-api upstream 反馈(语义澄清 + 最小补丁) | upstream 明确定义 pass-through 语义并实现等价的模型寻址;若 upstream 选择「pass-through 就是字节级原样」,本 fork patch 需撤回并改为在渠道配置校验层拒绝「同时开启 pass_through 与 model_mapping」的组合 |

## patch 4: 2026-10-19 — model_mapping 集中化重试 (commits `5e4f55f45` + `1ff8ed374` + 本次合并 fix)

**问题现象**

用户报告 channel #21(zhipu_4v / 类 TYY MAAS provider)被重试或亲和到时,上游收到 404 "模型不存在",且 consume log 里的 `modelName` 是客户端原始虚拟名 `tyy_maas_kimi-k2.5`,不是 channel `model_mapping` 配置的映射名。同一现象也在其他类似场景复现:`上游 404, 模型不存在, modelName=<unmapped virtual name>`。

**根因(双层)**

1. **`helper.ModelMappedHelper` 调用位置错误(commit `5e4f55f45` 引入)**。之前 8 个 per-format handler 各自调用一次;`5e4f55f45` 集中到 `controller.Relay()` 函数顶部。此时 `model_mapping` 还**没有**进 gin context(`SetupContextForSelectedChannel` 在 retry loop **内部**的 `getChannel()` 才设),首次调用读到空字符串=no-op。重试 hop 选定新 channel 时 context 已经换成新 channel 的 mapping,但 `request.SetModelName` 与 `info.UpstreamModelName` 不会自动跟着重做 → 上游 body 仍是原始 client 虚拟名。
2. **`relay/common/relay_info.go` 的 `InitChannelMeta` 主动清空映射**。line 272 把新建 `ChannelMeta.UpstreamModelName` 硬编码填回 `ContextKeyOriginalModel`(即原始 client 名);line 322-323 强行 `info.Request.SetModelName(info.OriginModelName)`。这两条在"每个 handler 各自 map"的旧 design 下是为下一次 mapping 准备干净起点;在 controller-loop 集中调用 design 下变成破坏性回滚——每层 per-format handler 进来时都把映射清空。所以 `5e4f55f45` 修复后 wire body 仍然带原始名,只是多数 case 的 first hop 看到的是集中调用,概率性看起来"工作";只要 retry hop 一换 channel 就 100% 暴露。

**修复(本次合并 commit)**

1. **`controller/relay.go`**:把 `helper.ModelMappedHelper(c, relayInfo, request)` 从函数顶部(`GenRelayInfo` 之后)挪到 retry loop 内、`getChannel()` 返回之后。函数顶端不再调用。第一次 hop 走 `Distribute` 中间件已经走通的 `SetupContextForSelectedChannel` → `model_mapping` 已经在 context 里(只是中间件路径提前设好);retry hop 每次重新 `SetupContextForSelectedChannel` 后 context 也立刻是新 channel 的 mapping。每次 iteration 都把"当前 channel 的映射"应用到 `info.UpstreamModelName` 与 `request.Model`。
2. **`relay/common/relay_info.go`**:把 `InitChannelMeta` 里的两个 reset 改成条件保留:
   - 新建 `ChannelMeta.UpstreamModelName` 时,如果 `info.ChannelMeta` 已经存在(`info.UpstreamModelName` 非空)就沿用,否则才从 `ContextKeyOriginalModel` 初始化(防止 panic 在第一次调用 + 给非-controller-loop 入口留一条 fallback)。
   - `IsModelMapped` 同样沿用已有值。
   - 删除 line 322-323 的 `info.Request.SetModelName(info.OriginModelName)` —— 这条 reset 在集中调用 design 下完全不必要,只会侵蚀 controller-loop 步骤(1)刚写好的 `request.Model`。
3. **`relay/responses_request.go`**:保留 line 40 的 `helper.ModelMappedHelper` 调用,但加 doc 说明:"HTTP Responses 路径上 controller.Relay() 已经调过,这次是 idempotent 重复;WS Responses 路径(`buildResponsesWSCreatePayload`)不走 controller.Relay(),line 40 是唯一调用点,不能删。"
4. **`controller/relay_model_mapping_retry_test.go`(新)**:e2e regression。构造 channel A(limit=1,被 sibling 占位饱和)+ channel B(limit=1,空闲);同 `model_mapping`:A→A-映射名,B→B-映射名,但模型名不同。A 第一次 hop 因 saturate 被 429 拒绝(不发 upstream),retry hop 必落到 B(排除法唯一候选)。断言:B 上游收到的 body 含 B 的映射名、不含原始 client 名、不含 A 的映射名。这一条直接覆盖用户报告的"上游 404 模型不存在"路径,不再依赖 helper 单测。
5. **`relay/helper/model_mapped_test.go`(新,commit `1ff8ed374`)**:helper 单测——映射生效、二次调用换 context 映射名时重新翻译、空映射是 no-op。

**测试证据**

- `go test ./controller/... ./relay/... ./middleware/...` 全绿,含既有 `TestRelay*RetryLoop*` 系列(38 个 relay_slot_test.go 子测试)不受 regression。
- 新 `TestRelayWireBodyCarriesRetryChannelModelMapping` pass;消费日志显示 `response_model.upstream_model = "real-upstream-b"` 而非原始 `tyy_maas_kimi-k2.5`,符合预期。

**未修但已记录的相关路径**(刻意留给上游决策)

- **`pass_through_body_enabled=true` 与全局 `PassThroughRequestEnabled=true` 路径仍 raw-body 直发**:`relay/compatible_handler.go:99`、`relay/claude_handler.go:88`、`relay/responses_request.go:52` 取 `c.GetBodyStorage` 直接发原始字节。这条分支默认关闭、用户场景未启用,但映射仍然失效。本次不做——待上游裁决"pass-through 的语义到底是什么"(保持 raw 字节 vs 让映射生效)。
- **`relay/relay_task.go:227 / :244` 两处 ModelMappedHelper 调用**:task 路径不走 `controller.Relay`,内部有 `mappedBeforeValidate` 互斥旗标保证只跑一次。已确认合法,本次不动。

**remove-when**

upstream 任一即可移除:

- (a) 把"集中调用 + 不重置"实现合并到 main(直接合入本 patch 等价物)。
- (b) 上游对 `InitChannelMeta` reset 设计做根本性重写,使得 mapped-name 在 retry 之间天然持久。
- (c) 把现有的 `helper.ModelMappedHelper` 设计完全替换,集中调用与 per-attempt reset 二者择一,选其中一种作为上游主导写法。

## patch 1: 2026-09-24 — concurrency_limit 启动崩溃

**问题现象**

部署在 PostgreSQL 上的 0924 镜像启动时反复重启,日志:

```
2026/09/24 13:13:55 /build/model/main.go:666 postgres error SQLSTATE 42601
[FATAL] failed to initialize database: ERROR: multiple default values specified for column
       "concurrency_limit" of table "tokens" (SQLSTATE 42601)
[9.160ms] [rows:0] ALTER TABLE tokens ADD COLUMN concurrency_limit INTEGER NOT NULL
       DEFAULT 0 DEFAULT NULL
```

**根因**

`addTokenConcurrencyColumn` 在 `case DatabaseTypePostgreSQL` 分支无条件追加字符串 `" DEFAULT NULL"`,但调用方 (`migrateTokenConcurrencyLimits`) 已经传入 `"INTEGER NOT NULL DEFAULT 0"` —— 两者拼出两个 DEFAULT 子句,PostgreSQL 拒绝。

同一函数在 MySQL 8 strict 模式下还会拒绝 `TEXT NOT NULL DEFAULT ''`(Err 1101),但调用方没有看出来,因为：

- `migration/three_db_migrate_test.go::runMigrator` 复刻了一份自己写对了的 SQL,**从未调用 `addTokenConcurrencyColumn`**。
- 原 O-1 提交的"三库幂等性证据"(commit `5d5db3494` message 中的 schema-evidence.csv)实际上是在 test 用 inline SQL 跑出来的,**不是生产代码路径**。

**修复**

`model/main.go::addTokenConcurrencyColumn`:

1. 检测 caller 已提供 `NOT NULL` / `DEFAULT` 时不再追加 ` DEFAULT NULL`。
2. 当 MySQL + 列定义含 `TEXT` 时剥除尾部 `DEFAULT '...'` 子句(MySQL 8 strict mode Err 1101)。

**验证(三库实际 run)**

```
=== RUN   TestAddTokenConcurrencyColumn_GeneratesValidSQL/sqlite
=== RUN   TestAddTokenConcurrencyColumn_GeneratesValidSQL/postgres
=== RUN   TestAddTokenConcurrencyColumn_GeneratesValidSQL/mysql
--- PASS: TestAddTokenConcurrencyColumn_GeneratesValidSQL (0.23s)
    --- PASS: .../sqlite   (0.00s)
    --- PASS: .../postgres (0.07s)
    --- PASS: .../mysql    (0.16s)
PASS
ok  	github.com/QuantumNous/new-api/model
```

迁移套件 + 既有 PostgreSQL/MySQL 幂等回归:

```
ok  	github.com/QuantumNous/new-api/migration	1.806s
```

**新增 / 修改文件**

| 文件 | 行数 | 性质 |
| --- | --- | --- |
| `model/main.go` | +13 (1 个函数内联 + 注释头) | 桥接层:既保留函数签名,只在分支内调整 SQL 串 |
| `model/main_test.go` | +108 (新建) | 桥接层:补充真正调用 `addTokenConcurrencyColumn` 的集成测试(`//go:build integration`),修复原测试盲区 |

**前置 TDD 证据**

- RED: PostgreSQL 子测试首次运行报 `Received unexpected error: ERROR: multiple default values specified for column "concurrency_limit" of table "tokens" (SQLSTATE 42601)`,与生产日志字字对应。
- GREEN: 同样三行 schema(`CREATE TABLE tokens (id ...)`) + 同一调用,改为 PASS。

## patch 5: 2026-10-20 — 渠道全生命周期回归测试套件(纯测试,零生产代码改动)

**背景**

用户在生产上给 zhipu 渠道配了 `model_mapping`,上游持续 404,排查后确认是映射丢失(patch 4 + patch 6 已修生产代码)。修完用户提出一个更大的问题:「我们主要就是使用这个 AI 聚到供应商这一块,不应该这些基础功能还有问题」,要求梳理渠道从配置到被调用的完整生命周期,并补一套全流程回归测试。

本 patch 只新增/扩展**测试文件**,不改任何生产代码。

**盘点结论(决定了测试的靶子)**

既有资产:Go 测试 299 文件 / 2148 函数,`controller` 与 `relay` 是密集区;`concurrency-test/` 14 个黑盒脚本专测并发;前端 vitest 173 文件。既有覆盖的是**并发**,不是**渠道**——`relay/channel/` 下 30+ adaptor 零测试,且没有任何一条「创建→编辑→启停→删除→重建」的渠道生命周期串联测试。

**新增文件**

| 文件 | 用例 | 钉住的不变量 |
| --- | --- | --- |
| `relay/common/relay_info_channelmeta_test.go`(新) | 2 | `InitChannelMeta` 跨 attempt 保留 `UpstreamModelName`/`IsModelMapped`/`request.Model`;首跳 `ChannelMeta == nil` 时回落到原始名 |
| `controller/relay_model_addressing_matrix_test.go`(新) | 7 | 寻址载体矩阵:openai / zhipu_v4(body 寻址)与 zhipu v3(URL 路径寻址)× pass-through 开关;无 mapping 的渠道不得被改写 |
| `controller/channel_lifecycle_regression_test.go`(新) | 9 | abilities 笛卡尔展开、改 models 后重建、禁用后选路、删 ability 后选路、model_mapping 存储契约、setting JSON 往返 |
| `controller/relay_billing_model_test.go`(新) | 2 | **计费口径分界线**:`OriginModelName` 用于定价/日志,`UpstreamModelName` 只用于上游寻址;映射名有价而虚拟名无价时必须拒绝计费 |
| `web/src/features/channels/components/__tests__/model-mapping-editor.test.tsx`(扩) | +7 | 映射增删改的 payload、空上游名、非法 JSON 的可见反馈、重复虚拟名 |
| `web/src/features/channels/components/__tests__/channel-configuration.test.tsx`(扩) | +6 | 抽屉保存的 `model_mapping` payload、非法 JSON 阻断保存、映射键不在 models 时的确认、pass-through 开关往返 |
| `web/src/features/channels/components/__tests__/model-mapping-batch-dialog.test.tsx`(扩) | +3 | strip-prefix 规则、切换任务重置、空候选列表 |

**RED-GREEN 证据**

寻址矩阵与 `InitChannelMeta` 两组在回退生产修复后全红,报错信息逐字复现生产事故:

```
a path-addressed channel must route to the MAPPED model;
  got path=/api/paas/v3/model-api/virtual-model-alias/invoke
```

这正是 zhipu v3 用 URL 寻址时的 404 路由形态。恢复修复后全绿。

**测试发现的实现问题(未修,交上游/产品裁决)**

1. **崩溃级** — `model/channel_cache.go:65` `InitChannelCache` 在「某渠道 group 完全没有 ability 行」时 `assignment to entry in nil map` panic。`SyncChannelCache` 是周期 goroutine,触发即**整个网关进程崩溃**。已用 `TestInitChannelCachePanicsWhenAChannelGroupHasNoAbilityRows` 把该事实钉住。
2. **写入零校验** — `controller/channel.go:546 validateChannel` 从不解析 `model_mapping`。非法 JSON 落库后,每条走该渠道的请求在 `relay/helper/model_mapped.go:24` 报 `unmarshal_model_mapping_failed`——「保存时无感、运行时全挂」。
3. **读操作销毁数据** — `model/channel.go:1057 GetSetting()` 解析失败时只打 SysLog,然后 `channel.Setting = nil` 并 `Save()`,一次**读**把运营保存的设置永久抹成 NULL。
4. **双真相来源** — 删掉某模型的 ability 行后,内存缓存路径(生产默认 `MEMORY_CACHE_ENABLED=true`)仍按 `channels.models` 索引继续选中该渠道,DB 路径则拒绝。
5. **前端缺口** — `model-mapping-editor.tsx:267` `convertRowsToJson` 不校验上游名非空,`channel-form.ts:216` 的 zod refine 只要求「值是字符串」,`""` 通过。配了等于没配,运行时 `model_mapped.go:36` 静默 break,无任何反馈。
6. **前端死代码** — `channel-mutate-drawer.tsx:1663` 手写的细分 JSON 校验分支不可达(被 zod refine 抢先),提交后只剩笼统文案。

**remove-when**

upstream 具备等价的渠道寻址 / 生命周期 / 计费口径回归套件时移除。纯测试 patch,不产生上游行为分歧,回滚无风险。

## patch 6: 2026-10-20 — 渠道基础设施 5 处修复(patch 5 测试发现的问题)

**背景**

patch 5 只加测试不改代码,把渠道主线上 5 个缺陷钉了出来。用户裁决「都去修复」。本 patch 逐条修复并把钉住旧行为的用例改写为钉住修复后契约。

**1. 崩溃级 — `model/channel_cache.go` `InitChannelCache` panic**

索引外层 map 只为「有 ability 行」的 group 建内层 map,却遍历所有渠道行往里写,group 不在外层 key 时 `assignment to entry in nil map` panic。`SyncChannelCache` 是周期 goroutine,触发即整个网关进程崩溃。

同时修掉**路由双真相来源**:索引改为读 **enabled 的 ability 行**(与 DB 路径 `model.GetChannel` 同源),不再从渠道行重算 group × models。`AddAbilities`(`model/ability.go:277`)把 group/models/priority/weight 镜像到 ability 行,`FixAbility`(`model/ability.go:418`,`main.go:112` 启动时调用)会全量重建,所以 ability 行不会跨重启缺失——abilities 成为唯一索引无兼容风险。此前两条路径不一致:删掉某模型的 ability 行后 DB 路径拒绝、缓存路径仍继续服务。

**2. 读操作销毁数据 — `model/channel.go` `GetSetting` / `GetOtherSettings`**

解析失败时旧代码 `Setting = nil` + `Save()`。这两个方法在**每次读**(选路、转发、后台列表)都会调用,于是任何一条走该渠道的请求都可能把运营保存的设置永久抹成 NULL。改为只报 `SysError` 并回落到零值,不再写库。

**3. 写入零校验 — `controller/channel.go` `validateChannel`**

新增 `model.Channel.ValidateModelMapping()`,与 `ValidateSettings()` 并列,拒绝:不可解析的 JSON、非字符串值、**空的上游名**。空上游名过去能落库,运行时被 `relay/helper/model_mapped.go:36` 当作「无映射」静默 break——配了等于没配且零反馈。`{}` 与 nil 仍是合法的「不映射」。

**4. 前端空上游名无校验 — `web/src/features/channels/`**

`model-mapping-editor.tsx` 的 `convertRowsToJson` 不校验 `to` 非空,`channel-form.ts` 的 refine 只要求「值是字符串」。改为 zod `superRefine` 做权威拒绝(细分消息),编辑器内联 `Missing upstream model name for: {{models}}` 提示。新增 2 个 i18n key × 7 语言。

**5. 前端死代码 — `channel-mutate-drawer.tsx`**

`onSubmit` 里手写的 `validateModelMappingJson` 分支永远不可达(zod refine 抢先失败),细分消息用户看不到。删除该分支与 `lib/model-mapping-validation.ts` 里失去调用方的同名函数,细分消息改由 zod 产出、经 `FormMessage` 落到用户可见位。

**未改的自映射语义** — `{"a":"a"}` 继续放行:`model_mapped.go:41-46` 对自映射有显式分支(置 `IsModelMapped=false` 并正常返回),是被显式处理过的 no-op,不同于 `""` 的静默失效。

**验证**

| 命令 | 结果 |
| --- | --- |
| `go test` 根模块全部包 | ok=43,FAIL=0 |
| `go test ./...`(relaykit) | ok=9,FAIL=0 |
| `go test ./controller/` 全量 ×2 | 两次均 ok(101s) |
| `bun run test src/features/channels` | 21 files / 311 tests passed |
| `bun run typecheck` / `oxlint` / `oxfmt` | 通过(7 个 oxlint error 为既有问题,HEAD 版本单独落盘复现,与本次无关) |

`TestSecurityAccountDeletionConcurrentRequestsHaveOneWinner` 在一次全量并发跑中失败一次,隔离跑 3/3 通过、随后两次全量 controller 全绿;该用例不引用任何 channel/ability 状态,属既有负载型 flaky,非本 patch 引入。

**回归测试改写**

| 用例 | 改动 |
| --- | --- |
| `TestInitChannelCachePanicsWhenAChannelGroupHasNoAbilityRows` → `TestInitChannelCacheToleratesChannelGroupWithNoAbilityRows` | `require.Panics` → `NotPanics`,并断言 ability 被删的渠道不可路由、恢复后重新可路由 |
| `TestDeletingAbilityRowsKeepsCachePathRoutingIntact` → `TestDeletingAbilityRowsStopsRoutingOnBothPaths` | 断言两条路径一致不可路由,并验证同渠道其他模型不受影响 |
| 新增 `TestInitChannelCacheRetiresOnlyThePrunedModel` | 剪掉一个模型的 ability 不得连带废掉该渠道其他模型 |
| `TestChannelModelMappingStorageContract/invalid_..._is_accepted_at_write_time` → `..._is_rejected_at_write_time` | 断言拒绝非法 JSON 与空上游名,放行 `{}` 与 nil |
| `TestChannelSettingJSONRoundTrip/unparsable_..._is_silently_cleared_and_persisted` → `..._is_preserved` | 断言失败解析后 blob 原样保留,不再被读操作销毁 |

**remove-when**

upstream 任一即可移除:(a) `InitChannelCache` 改为以 ability 行为索引来源;(b) `GetSetting`/`GetOtherSettings` 移除读路径写回;(c) `validateChannel` 增加 model_mapping 校验;(d) 前端映射校验收敛到 zod 单一来源。

## patch 7: 2026-10-20 — 路由索引改数据源的复审修正(研究:CHANNEL-ROUTING-INDEX-RESEARCH)

**背景**

patch 6 把 `InitChannelCache` 的路由索引数据源从渠道行换成 ability 行,理由是修 panic + 消除双真相来源。部署前用 GSD 流程(`gsd-phase-researcher`,产出 `.planning/research/CHANNEL-ROUTING-INDEX-RESEARCH.md`)做了一次独立复审,结论是方向正确但**我漏掉了自己引入的一半风险**。

**我上一条消息里说错的地方**

我写过:「`FixAbility` 在启动时会全量重建,所以 ability 行不会跨重启缺失——重启后自愈」。**这是错的。** `main.go:106-119` 里 `FixAbility` 只在 `InitChannelCache` 的 `recover()` 分支内被调用(panic 时才跑),非测试调用点全仓只有两处:`main.go:112`(panic 恢复)和 `controller/channel.go:352`(管理员点「修复能力」按钮)。

也就是说:旧代码的 panic 是**唯一**的自动修复通道,而我的 nil map 修复让 panic 不再可能 ⇒ 该通道成为死路 ⇒ **一个丢了 ability 行的渠道会永久不可路由**,既不自愈、也不报错,只是「那个模型没有可用渠道」。我在 patch 6 的代码注释里把这个不存在的不变量写成了事实,会把后续维护者带沟里。

**修正**

1. **删除死代码** — `model/channel_cache.go` 里 `enabledPairs` 映射计算了却从未被读取(真正的门控在第二个循环)。12 行无用代码。
2. **改正注释** — 删掉「FixAbility called at startup … cannot stay ability-less across a restart」这个假断言,换成对真实后果的说明,并指明修复入口。
3. **启动期增量修复(替代自愈缺口)** — 新增 `model.EnsureChannelAbilities()`(`model/ability.go`),启动时(`main.go`,`InitChannelCache` 之后)补齐**缺失的** ability 行,修完再重建一次缓存。
   - 不用 `FixAbility`:后者是 TRUNCATE 全量重建,多节点部署下每个节点互相踩,重建窗口会把线上索引清空。
   - 只补缺失行,`AddAbilities` 本身用 `OnConflict{DoNothing}` upsert,多节点并发收敛到同一结果。
4. **孤儿渠道告警** — `InitChannelCache` 统计「enabled 但一条 ability 行都没有」的渠道并 `SysError` 输出渠道 ID 列表,让静默的路由空洞变成启动日志里看得见的事(仅统计 enabled 渠道:禁用渠道没有 ability 行是正常态,因为禁用本身就翻 `abilities.enabled`)。

**为什么这类渠道会出现(研究中查实,不是我猜的)**

- `model/channel.go:939` 的 `EditChannelByTag` 先写 `group`,`:943-952` 才重建 abilities,且**错误被吞**(`GetChannelsByTag` 出错整段跳过,`UpdateAbilities` 失败只 log 并返回成功)⇒ 改 tag 的 group 后,旧代码 60 秒内进程死亡,新代码静默不可路由。
- `model/channel.go:604-612` 的 `Insert` 是**非事务**的:`DB.Create` 成功 + `AddAbilities` 失败 ⇒ 渠道行在、ability 缺失。`Ability.Model` 是 `varchar(255)` 而 `Channel.Models` 无长度限制,MySQL 严格模式下超长模型名即可触发。

**方案 B 排除的补充理由**

我实现过「ability 门控 + 渠道行兜底」又推翻,当时理由是「把不一致换了个方向」。复审补了第二条硬理由:仓库里已有测试 `controller/channel_lifecycle_regression_test.go` 的 `TestDeletingAbilityRowsStopsRoutingOnBothPaths` 断言「ability 行被删后缓存路径不得继续服务该模型」,方案 B 会直接违反它。

**回滚**

**不可单独 revert**:panic 修复与数据源切换在 `model/channel_cache.go` 同一连续 hunk,`git revert 75cc8df4c` 会连带回滚另外 4 项修复。手工回退数据源必须同时保留 nil map 守卫,否则把进程崩溃带回来。失败模式是「响亮的」(模型无可用渠道 + 告警 + 一键修复),不是静默数据损坏,不设计回滚路径,改设计前滚修复路径(本次 patch 就是)。

**新增回归测试**

| 用例 | 钉住的不变量 |
| --- | --- |
| `TestEnsureChannelAbilitiesRepairsOnlyMissingRows` | 只补缺失行不动已有行(健康渠道不重建)、幂等、修完渠道重新可路由、无需手动点「修复能力」 |
| `TestInitChannelCacheSkipsDisabledOrphanChannels` | 禁用渠道无 ability 行是正常态,不触发孤儿告警、不被选中 |

**本次生产库体检(实测 `docker-test-data/one-api.db`)**

| 检查项 | 结果 |
| --- | --- |
| 渠道总数 | 15 |
| 带 `model_mapping` 的 | 1(合法) |
| **会被新 `ValidateModelMapping` 拒绝的** | **0** |
| `setting` 不可解析的 | 0 |
| 渠道行推导的 (group,model) 对 vs enabled ability 行 | 3 vs 3,**完全一致,本次改动对该库零路由差异** |
| 完全没有 ability 行的渠道 | 0(兜底风险不存在) |

**验证**

`go test` 根模块 44 包 + relaykit 9 包全绿;`gofmt` 干净;`go vet` 通过。

**remove-when**

(a) upstream 把路由索引改为以 ability 行为唯一来源并提供启动期自愈;或 (b) 渠道写入路径(`Insert` / `EditChannelByTag`)改为事务性维护 abilities,则本 patch 的 repair + 告警可退化为仅告警。
