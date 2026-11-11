# 并发上限强制规格:Token 与用户并发控制(中文)

> **【实现状态说明 · 以 main 分支为准】**
>
> - **本规格描述的目标形态**:Token 轴与用户轴并发上限、Redis 共享计数、渠道并发轴、有界等待队列、429 契约、Token/用户配置入口,以及配套的验证要求与已知限制。
> - **main 上现有实现只有 O-1**:进程内 `MemoryLimiter`(`service/concurrency/limiter.go`);Token 轴 / 用户轴作用域桶与并发中间件(`middleware/concurrency.go`,已挂载于 relay 与 playground 路由);管理员并发快照(`controller/concurrency.go` + `GET /api/concurrency/snapshot`);`Token.ConcurrencyLimit` / `Token.ConcurrencyLimitByModel` 两列及其三库幂等迁移(`model/token.go`、`model/main.go`);429 响应体的 `error.type` / `error.code` = `concurrency_exceeded` 与 CNT-01 message 形态。
> - **O-1 尚未接线到运行期**:限流器未在启动流程装配(`SetConcurrencyLimiter` 无生产调用方),Token / 用户上限无来源(无 `User.ConcurrencyLimit` 字段、无管理员写入路径、无 `Retry-After` 头),§3.4 的同步 relay context 绑定亦未实现 —— 因此当前 main 的并发闸门尚不实际拦截。
> - **正文标注「已交付(02-08~02-19)」的分片不在 main 上**:Redis ZSET 租约多桶、渠道并发轴、编辑抽屉前端 UI、有界等待队列等只存在于本地并发功能线分支 `newapi0924bak` 及同源 `newapi924-1015` / `newapi924-1029`;这些「已交付」是分片交付史实,不表示 main 已具备该能力。
> - **main 引入这些能力时,以本文对应章节的行为要求、契约与字段定义为准重新实现**,不假定 main 已满足。

> 本文档描述 new-api 网关中"并发上限从配置到强制执行"的完整规格:行为要求、客户端契约、作用域模型、实现要点、验证要求与已知限制。读者无需了解任何内部流程背景,所有引用均为仓库内实际代码路径。

---

## 1. 功能概述

管理员可为 API Token 与用户配置并发上限,限制该主体同时进行中的请求数;达到上限的新请求先尝试有界排队等待(默认开启,见 3.5),排队超时、队列满或排队被关闭时收到规范的 HTTP 429,请求结束后槽位立即释放。

本规格覆盖两条轴:

- **Token 轴**:Token 全局并发上限(`Token.ConcurrencyLimit`)与按模型并发上限(`Token.ConcurrencyLimitByModel`),以及既有"字段存在但无任何生产路径推入 limiter、实际不限流"缺陷的修复。
- **用户轴**:用户全局并发上限(`User.ConcurrencyLimit`,0 = 不限),作用于该用户名下全部 Token 的聚合 in-flight。

## 2. 范围

> 本节"范围内/范围外"描述规格初版的切分。初版之后 02-08~02-16 分片已陆续交付 Redis 共享计数、渠道并发轴、编辑抽屉前端 UI 与有界排队——相关内容已在对应章节按"已交付"标注,不再属于范围外。(此处"已交付"为分片交付史实;这些分片尚未合入 main,详见开头实现状态说明。)

**范围内(本列表为规格目标范围,不代表各项均已在 main 实现):**

- 进程内 MemoryLimiter 上的强制执行(单节点语义)。
- Redis token 缓存命中路径上的上限生效与缓存完整性。
- `User.ConcurrencyLimit` 字段的 SQLite / MySQL / PostgreSQL 三库持久化与幂等迁移。
- 管理员配置入口:Token 写入 API、用户管理 API 与用户管理页前端。
- 429 客户端契约。
- 客户端断连后槽位即时释放(出站上游请求绑定客户端请求上下文)。

**范围外(初版明确不做;前四项已由后续分片交付,逐条标注;以下"已交付"分片均尚未合入 main):**

- Redis 多节点共享计数(跨节点一致性、崩溃回收、fail-open)——已交付(02-08:RedisLimiter Lua 租约、fail-open 回落进程内 limiter;尚未合入 main)。
- 渠道(Channel)并发上限与其 relay 强制——已交付(02-09 字段与三库迁移、02-14 relay 逐 attempt 取放、02-15 全饱和终态 429 归一;尚未合入 main)。
- 管理员在 API Key 编辑抽屉配置 Token 并发字段的前端 UI——已交付(02-09/02-11/02-13;尚未合入 main)。
- 有界等待队列(bounded wait queue)——已交付(02-16,默认开启,见 3.5;尚未合入 main)。
- 异步任务(Midjourney/视频等 task relay)并发控制:任务轮询生命周期与请求槽位模型不同。
- WebSocket/realtime 长连接占槽语义。
- 普通用户自设 Token 并发上限:并发上限仅管理员可配(与 quota 集权一致)。
- fail-closed 模式、per-channel-per-model 上限:仍为后续演进方向。

## 3. 行为要求

### 3.1 Token 轴

1. 管理员为某 Token 设置全局并发上限后,进行中请求数达到上限时,该 Token 的新请求先尝试有界排队等待(见 3.5);排队超时、队列满或排队被关闭时收到 HTTP 429。
2. 按模型并发上限对指定模型独立生效,与全局上限同时约束,生效值为更严格者(most-restrictive-wins):`min(模型帽, 全局帽)`。
3. 请求经任一路径退出——正常完成、上游错误、panic、客户端断连、SSE 流结束——后立即不再占用并发名额;压测后计数回落到实际 in-flight,无泄漏。
4. 上限配置为 0 或缺省表示不限,但请求仍被计数(可观测)。
5. 已设置的上限对 Redis token 缓存命中的请求同样生效;缓存哈希缺少并发字段或 schema 过期时必须回源数据库重载,不得解码为 0(= 不限流)。

### 3.2 用户轴

1. 管理员为某用户设置的全局并发上限作用于该用户全部 Token 的聚合 in-flight(`user:<id>:*` 桶);超限时该用户新请求先排队(见 3.5)、超时或队满后收到 429,即使单个 Token 自身未满。
2. 不做用户级按模型细分(按模型维度仅存在于 Token 轴)。
3. 不同用户之间互不影响:某用户满位时其他用户正常放行。
4. 字段持久化,三库迁移幂等:全新库与升级库各启动两次,第二次启动零 DDL;已有数据无损,新列默认 0。
5. 保存后新上限对后续请求立即生效,包括用户缓存命中路径(保存触发缓存失效并重写,不返回旧上限)。

### 3.3 权限与安全

1. `concurrency_limit` / `concurrency_limit_by_model` 仅管理员可写:服务端在写入路径按角色强制(角色缺失按非管理员处理,fail-closed)。
2. 非管理员的"不写"必须是结构性不拷贝字段,而非拷贝零值——更新接口按整列清单无条件落库,拷零值会抹掉管理员已配置的上限。
3. 自助路径(用户修改自身资料)不接受、不回显并发字段。
4. 前端仅管理员可见的界面门控只是呈现层,**不是安全边界**;安全边界永远在服务端。
5. 请求内测试覆盖头(如 `X-Test-*` 限额覆盖)仅在框架测试模式下可读,生产模式不可用作绕过。
6. 上限值校验已交付(02-13,该分片尚未合入 main):负数与畸形按模型 CSV 在保存前被拒绝、不落库(单一解析源 `ParseEntries`,Go/TS fixture 双端锁);读取侧仍保留防御性解码并忽略非法值作为纵深。

### 3.4 槽位释放(含客户端断连)

> 本节为目标形态。main 尚未实现:`relay/channel/api_request.go` 的 `DoApiRequest` / `DoFormRequest` 仍用 `http.NewRequest` 构造请求(未绑定 `c.Request.Context()`),该改动随同步 relay 路径分片,尚未合入 main。

1. 出站上游 HTTP 请求必须绑定客户端请求的 context:客户端断连时上游请求被取消,并发槽位即时释放,不被上游挟持到其自然结束。
2. 此绑定适用于同步 relay 路径(`DoApiRequest`、`DoFormRequest`)。任务轮询与 WebSocket 请求保持独立后台 context,不受客户端连接生命周期影响(任务语义要求其继续)。
3. 对外错误文案不得因错误链处理而改变,也不得泄漏上游内部细节;同时必须保留可判定的错误链(如 `errors.Is(err, context.Canceled)`)。

### 3.5 有界等待排队(02-16 已交付,该分片尚未合入 main)

Token 轴与用户轴(中间件路径)达到上限的新请求不再立即拒绝,而是先尝试进入有界等待队列:

1. 出厂默认开启(`concurrency.queue_enabled=true`),最多等待 `concurrency.queue_max_wait_seconds=10` 秒;排队深度 Q = min(ratio × C, cap),默认 ratio=4、cap=128(C 为该主体并发上限)。
2. 入队为 all-or-nothing:请求需一次性等待其全部作用域桶腾位,不做逐桶部分占用;每轮以退避节奏(50ms 起 ×1.5 封顶 500ms + jitter)重试,并逐轮响应客户端断连。
3. 排队超时、队列满、排队被关闭三种情形均返回 429(第 4 节契约),状态码与错误类型不变;排队超时与队列满的 message 各自独立(第 4 节,06),排队关闭保持并发超限的既有文案。
4. 队列为 per-node:多节点部署时各节点各自排队,跨节点总排队深为各节点之和——运维需知悉该语义。
5. 4 个 `concurrency.queue_*` 配置项经 RootAuth option API 动态生效(atomic 读,无需重启);排队深度计入 snapshot 顶层 `queued` 字段。
6. 渠道轴(relay 逐 attempt 取放)不经过此队列:渠道槽位获取失败即产生饱和拒绝信号,由 relay 重试循环换渠道消化(见第 4 节与 02-15 交付;02-15 尚未合入 main)。

## 4. 客户端契约(429)

> 本节为目标形态。main 现有实现只覆盖其中一部分:`middleware/concurrency.go` 的 `rejectConcurrency` 产出 429 + `concurrency_exceeded` + CNT-01 message,但**无 `Retry-After` 头**(常量 `middleware.ConcurrencyRetryAfterSeconds` 与该头随 02-15/02-16 分片,尚未合入 main);另外三种 message(排队超时 / 队满 / 渠道饱和)与 `controller/relay.go` 的 `renderRelayTerminalError` 出口在 main 上均不存在。

并发超限响应的确切格式:

- 状态码:**429**
- 响应头:**`Retry-After: 1`**(固定 1 秒代码常量,单一来源 `middleware.ConcurrencyRetryAfterSeconds`;该常量与响应头尚未合入 main)。边界:OpenAI Realtime 分支在任何终态写回前已将传输升级为 WebSocket,错误以 WS error frame 呈现,无 HTTP 头可写——该分支没有 `Retry-After` 头(02-15 裁定的契约边界)。
- 错误体:`error.type` 与 `error.code` 均为 **`concurrency_exceeded`**(状态码与类型归一,不新增错误码子类型——06 产品裁定),message 有四种逐字形态,按拒绝来源区分(调用方按 message 短语识别来源):
  - Token 轴/用户轴并发超限即时拒绝(排队关闭、深度系数为 0 或等待窗口为 0):`Concurrency limit exceeded for your account. Please retry after the current requests finish.`(CNT-01 逐字保持)
  - 排队等待超时(含等待中客户端断开退出等待):`Timed out waiting for an available concurrency slot. Please retry after the current requests finish.`(06)
  - 队列已满(未能入队):`The concurrency wait queue is full. Please retry later.`(06)
  - 渠道饱和(全部渠道槽位占满):`Concurrency limit exceeded for the selected channel. Please retry after the current requests finish.`(02-15 裁定的中性模板,不含渠道 ID 或内部路由码;relay 出口形态,不归中间件;02-15 尚未合入 main)
- 三种中间件形态的 message 两两互不相同、各含可识别短语("Concurrency limit exceeded for your account" / "Timed out waiting" / "wait queue is full");**仅 message 区分来源**,429 状态码、`error.type`、`error.code`、`Retry-After: 1` 头对三种来源完全一致(06 产品裁定:不新增错误码子类型、不引入 `error.scope` 之类的结构化字段)。
- 错误体**不得泄漏**内部作用域桶名(如 `key:11:*`)、数值 ID、队列深度或配置值。
- 客户端可见的 429 **仅由"超限"这一种结果驱动**(三种中间件来源均为超限的不同阶段/出口,渠道饱和为 relay 路径的同一归一结果),不与其他限流类别或基础设施错误混淆;并发基础设施故障永不以 429 呈现。
- 超限拒绝发生在配额预扣之后,被拒请求按现有退款机制退回配额。

该契约由两个出口产生,均逐字满足上述格式:relay 主路径的中间件拒绝(Token/用户轴,`middleware/concurrency.go` 的 `rejectConcurrency`,按来源选用四种 message 之中的前三种),以及 relay 终态渲染出口 `controller/relay.go` 的 `renderRelayTerminalError`(渠道饱和信号在此归一化为 `concurrency_exceeded` + Retry-After,内部错误链保持 `channel:` 前缀原样供重试/禁用判定,使用第四种 message)。两个出口共享同一 Retry-After 常量;所有 HTTP relay 格式(含 Gemini 路径)一致,Realtime 以 WS error frame 呈现(无 HTTP 头,见上)。

## 5. 作用域模型

> 本节为目标形态。main 现状与之有出入:表中的三桶与"用户轴只有聚合桶"尚未在 main 成立——`middleware/concurrency.go` 的 `buildScopes` 目前仍生成四个桶(多出 `user:<id>:<model>`),且表中三个桶的 limit 均无来源(见第 6 节),实际不构成有效限流。

每个 relay 请求在并发中间件中计算出一组"作用域桶"(scope + limit 配对),同时获取全部桶槽位,任一桶满即拒绝:

| 桶 | 含义 | limit 来源 |
|----|------|-----------|
| `key:<token_id>:<model>` | 该 Token 在指定模型上的在途数 | `Token.ConcurrencyLimitByModel` 按模型查表 |
| `key:<token_id>:*` | 该 Token 的总在途数 | `Token.ConcurrencyLimit` |
| `user:<user_id>:*` | 该用户名下全部 Token 的聚合在途数 | `User.ConcurrencyLimit` |

关键语义:

- **most-restrictive-wins**:三桶并取,最严的帽生效;无需显式合并逻辑。
- 旧的 `user:<id>:<model>` 桶已移除,用户轴只有聚合桶。
- `limit <= 0` 的桶恒通过但仍计数。
- 中间件在 token 鉴权与模型解析之后执行(链序:TokenAuth → ModelRequestRateLimit → Distribute → ConcurrencyMiddleware),保证 token/user 上下文已就绪。
- 后续分片新增的第四种桶 `channel:<id>`(渠道轴,02-14,该分片尚未合入 main)不经过本中间件批量取桶:它在 relay 重试循环内逐 attempt 获取/释放,limit 来源 `Channel.ConcurrencyLimit`,饱和拒绝信号交由重试循环换渠道消化(见第 4 节)。

## 6. 实现要点

> 本节为目标形态。main 上仅有 §6.1 的 `MemoryLimiter` 骨架与 §6.5 的快照端点(`/api/concurrency/snapshot` 路由与 authz 门已存在,但按 token/user 配置回填 limit 列的逻辑未实现);§6.2 的缓存并发字段与 schema 完整性门、§6.3 的 `User.ConcurrencyLimit` 字段与三库迁移、§6.4 的 Token 写入路径指针影子字段与管理员门、§6.6 的出站请求 context 绑定,均尚未合入 main。

### 6.1 限额计算采用 per-request 模式

`service/concurrency/limiter.go` 的 `Limiter` 接口:

```go
Acquire(ctx context.Context, scope ScopeKey, limit int) (release func(), err error)
Snapshot(ctx) ...
```

- **每次请求实时计算 limit 并传入 Acquire**,不采用"配置变更时推送 limit"的模式:后者需要跨层状态同步,且无法直接适配 Redis 计数后端——该判断已被 02-08 兑现:RedisLimiter 复用同一 per-request 契约,limit 每次随 Acquire 传入。(该形态在 main 上尚未落地:main 的 `service/concurrency/limiter.go` 仍是 `Acquire(ctx, scope)` + `SetLimit(ctx, scope, limit)` 的推送式接口,per-request 形态随 02-08 分片,尚未合入 main。)
- 接口不携带 `SetLimit`;进程内 limiter 不保存 limit 状态。
- `Snapshot` 输出结构保持稳定(JSON 形状不变),便于后续消费方。

上下文取数点:

- `middleware/auth.go` 鉴权时把解析好的 `token_concurrency_limits` 发布到请求上下文;
- 用户上限经类型化 context key(`ContextKeyUserConcurrencyLimit`)发布与读取——发布键与读取键必须逐字一致,键漂移会静默失效。

### 6.2 Redis token 缓存的并发字段与完整性门

`model/token_cache.go`:

- 缓存哈希新增 `ConcurrencyLimit`、`ConcurrencyLimitByModel`、`CacheSchema` 字段(既有参数序不动,尾插追加)。
- 缓存带 schema 版本号;读取门在版本不匹配时**整表回源数据库重灌**,绝不容忍缺字段哈希——否则并发上限会被解码为 0(= 永久不限流),这是本功能最危险的失败模式。
- 部分字段更新脚本遇到 schema 不匹配的旧哈希必须跳过写入,不得把新版本号盖到旧哈希上(否则旧哈希被"洗白"绕过读门)。
- 版本过期哈希的重建采用条件补写:不覆盖配额等既有活跃字段。

`model/user_cache.go` / `model/user_auth_cache.go` 同型:schema 版本升级、`UserBase.ConcurrencyLimit` 字段、写入时随上下文发布。

### 6.3 用户上限持久化

- `model/user.go`:`User.ConcurrencyLimit int`(`json:"concurrency_limit"`,列 `concurrency_limit INTEGER NOT NULL DEFAULT 0`)。
- 更新必须走显式列名 map(而非 struct Updates):struct 更新会跳过零值,"清 0 = 解除限制"将静默失败。
- 迁移助手在 `AutoMigrate(&User{})` 之前执行,按 SQLite / MySQL / PostgreSQL 三分支 `ALTER TABLE ... ADD COLUMN`,带 HasTable/HasColumn 守卫保证幂等。

### 6.4 配置写入路径

**Token 写入 API**(`controller/token.go`):

- 请求结构体用**指针影子字段**承载这两个可选值,绑定值只进指针——"请求缺省"与"显式 0"可区分;无条件拷贝会在前端表单不携带这两个字段时静默清零上限。
- 拷贝发生在管理员角色门内、按"非 nil 才拷贝"执行。
- 模型字段的 JSON tag 对外暴露为 `concurrency_limit_by_model`(否则绑定收不到、读回不回显);对非管理员的响应过滤已交付(02-11,该分片尚未合入 main):按请求者角色剥除两并发字段,非 admin 响应中整键缺席、admin 显式 0 保留。

**用户写入 API**(`model/user.go` `EditWithTx`):更新 map 白名单包含 `concurrency_limit`;该白名单同时是拒绝非管理员注入此字段的服务端防线。

### 6.5 快照页 limit 列补全

`controller/concurrency.go` 在生成并发快照时,于 limiter 快照之后、响应之前,从 token/user 配置数据实时回填每行的 limit,并按快照行公式仅在 limit > 0 时重算利用率。

- 快照显示的是**当前配置值**,而非"最近一次生效值";快照与 limiter 内部解耦,limit 不写入 limiter。
- 按模型行取 `LimitForModel`(该模型是否配置了上限)而非合并后的有效上限,避免 CSV 外模型显示误导值。
- 输出 JSON 形状不变,前端零改动。

### 6.6 出站请求上下文绑定

`relay/channel/api_request.go`:

- 统一的客户端请求构建函数使用 `http.NewRequestWithContext(c.Request.Context(), ...)`;`DoApiRequest` / `DoFormRequest` 均经由它构建。
- 构建函数对空 context 做守卫;派生请求的 `GetBody` 语义保留(重放载荷与原载荷一致)。
- 上游请求失败的对外文案固定且不含内部细节,同时保留 unwrappable 的真实 cause。

## 7. 验证要求

以下为该功能成立必须满足的验证条件:

1. **端到端走真实 router 栈**:429 强制必须通过与生产一致的路由挂载(含 Gemini 路径)验证,不能只有中间件/解码器单元测试——单测缺席正是历史缺陷漏网的原因。
2. **全出口释放**:正常完成、错误、panic、客户端断连、SSE 结束各路径均有断言,压测后计数回落;流式断连用例必须由 context 传播驱动释放(测试自身不得兜底),作为回归钉子。
3. **断连时效**:客户端断连后槽位在毫秒级释放,不等待上游自然结束。
4. **写入七态**:管理员创建(全部字段/单条 CSV)、更新改值读回、显式清零、缺省保留、非管理员创建落零、非管理员更新保留——均需覆盖。
5. **缓存三态**:并发字段 round-trip、过期 schema 哈希被拒、回源重灌。
6. **三库迁移矩阵**:真实 SQLite、MySQL ≥ 5.7.8、PostgreSQL ≥ 9.6;全新库 ×2 + 升级库 ×2;升级首次启动恰 1 条 `ADD COLUMN`,第二次启动零本迁移 DDL;数据无损、新列默认 0。
7. **跨租户隔离**:某用户满位不影响其他用户。
8. **契约逐字断言**:429 状态码、`Retry-After`、错误体字段与四种 message 形态(超限/排队超时/队满/selected channel)、三种中间件形态两两互不相同、无内部标识泄漏、两个出口(中间件拒绝与 `renderRelayTerminalError`)逐字一致;Realtime 分支确认无 HTTP 头、错误经 WS error frame 呈现。
9. **前端**:字段可见性角色门控五态断言、payload 值透传(含显式 0 不被省略)、7 语言(en/zh/zh-TW/fr/ru/ja/vi)翻译键齐全。
10. 构建与格式:`go build ./...` 通过、gofmt 无 diff;前端 typecheck/build 通过。

## 8. 已知限制与遗留项

| 项 | 说明 | 去向 |
|----|------|------|
| 管理端整字段替换语义 | 用户更新 API 缺省 `concurrency_limit` 会被当 0 清空——与同端点 username/group/remark 的 full-replace 语义一致,前端恒发送该键 | 采纳外部 API 兼容政策后再议 |
| 流式断连 end-reason 分类竞态 | 断连原因可能被归类为扫描错误而非客户端离开,影响消费日志状态等少数消费者;窗口窄、不泄漏 | 待裁决 |
| ~~非管理员响应字段过滤~~ | 已交付(02-11,该分片尚未合入 main):按请求者角色剥除 `concurrency_limit` / `concurrency_limit_by_model`,非 admin 响应中整键缺席 | 已关闭 |
| ~~负数/畸形 CSV 校验~~ | 已交付(02-13,该分片尚未合入 main):保存前拒绝负数与畸形条目、不落库(单一解析源 + fixture 双端锁);读取侧防御性解码保留为纵深 | 已关闭 |
| 非管理员写入静默忽略 | 返回 200 且审计无痕(既定缓解;从安全审计角度显式 403 或留痕更佳) | 后续安全加固 |
| 上游 adaptor 旁路 | 个别图像生成 adaptor 自建后台 context,未走统一客户端请求构建(延迟释放,非泄漏) | 建议小修 |
| PostgreSQL `users.access_token` 重复 DDL | 既有迁移每次启动重复发出自我 cast 的列类型变更(GORM 类型 tag 与 PG 类型上报差异),与本功能无关、良性 | 单独排查 |
| 基线红测试 | Windows TempDir 家族、个别既有前端测试红、既有格式漂移,均与本功能无关 | 分别单独分诊 |

## 9. 演进方向(交付状态)

> 本节"已交付"指分片交付史实。下列 02-08~02-16 能力均**尚未合入 main**,只存在于本地并发功能线分支(`newapi0924bak` 及其同源分支);main 引入时按本文各章的行为要求与契约实现。

原规划的三项依赖能力均已交付(下列分片均尚未合入 main),Acquire 携带 limit 的契约按预期承接了它们:

1. **Redis 共享计数(已交付,02-08;尚未合入 main)**:启用 Redis 时并发计数跨节点一致(Lua all-or-nothing 租约、lease TTL 崩溃回收),Redis 不可达时 fail-open 回落进程内 limiter(基础设施故障不以 429 呈现);未启用 Redis 时保持进程内 limiter。
2. **Token 并发配置 UI(已交付,02-09/02-11/02-13;尚未合入 main)**:API Key 编辑抽屉配置全局/按模型上限,0/留空 = 不限的明确语义,仅管理员可见,保存即生效(含缓存失效);含非管理员响应字段剥除与负数/畸形 CSV 保存前校验。
3. **渠道并发轴(已交付,02-09/02-14/02-15;尚未合入 main)**:渠道字段与三库迁移、relay 逐 attempt 获取/释放渠道槽位、饱和拒绝驱动换渠道重试、全部饱和才终态 429(复用第 4 节的 429 契约,经 `renderRelayTerminalError` 归一化)。
4. **有界等待排队(已交付,02-16;尚未合入 main)**:Token/用户轴超限先入有界队列等待,超时/队满/关闭统一 429,详见 3.5。

仍未交付的方向:fail-closed 严格模式、per-channel-per-model 上限,见需求文档 Future Requirements。原「429 来源区分(`error.scope`)」已由 06 落地为 **仅 message 文本区分**(三种中间件形态各含可识别短语,见第 4 节;产品裁定不新增结构化字段)。
