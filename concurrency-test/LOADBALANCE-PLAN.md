# 负载均衡测试方案 — EWMA 动态有效权重(新方案,2026-10-14)

> 独立于昨日队列/客户端参数方案(QUEUE-SCENARIO / REPORT-CODING-CLIENTS)。本方案专测 02-17 EWMA 选路在多渠道同 key 拓扑下的均衡效果。

## 目标

1. 同一模型配置 4 个同优先级同权重的编号渠道(lb-01..04,克隆自渠道 1 的配置:同上游 `upstream.example.internal:30000`、同 key(空)、免 key 内网推理集群)。
2. 用四档提示词长度的任务打流,**打印 EWMA 算法观察到的参数与每步决策结果**,验证:
   - 分布均衡:warm 后 4 渠道份额趋同(同质上游);
   - 参数可解释:samples / successEWMA / latencyEWMA / multiplier 时间线与真实延迟方向一致;
   - 反应可见:长任务在某渠道堆积 → 该渠道 latencyEWMA 升 → multiplier 降 → 后续请求迁移;
   - 不饿死:任何渠道 multiplier 不破 0.2 地板、份额不归零。

## 拓扑

| 项 | 值 |
|---|---|
| 渠道 | lb-01..lb-04(新建,编号命名;type=openai,base/key/models 逐字克隆渠道 1) |
| 期间处置 | 原渠道 1(upstream-190)、2(upstream-190_copy)status→0 暂停,测试后恢复;lb 渠道保留供复跑 |
| 模型 | Qwen3.6-35B(单模型,per-(channel,model) 键即 4 条记录) |
| 权重/优先级 | 全部 weight=0(→平滑各 100)、priority=0(同层 → EWMA 生效) |
| 渠道并发上限 | 全部 0(不限)——隔离 02-15 饱和换渠道路径,只测 EWMA |
| 网关 token | 新建 `lb-test-token`,concurrency_limit=0(不限),防网关闸干扰 |
| 网关 | http://127.0.0.1:3001(root) |

## 任务矩阵(长度单位≈token;输出约束为短回复,变量只在输入侧)

| 档位 | 提示词长度 | 数量 | 说明 |
|---|---|---|---|
| short | ~500 | 32 | 高频打底样本,快速堆 samples 过 warm 门(≥10/渠道) |
| medium | ~1000 | 20 | 中等 prefill |
| long | ~2000 | 10 | 长任务——制造渠道间延迟差与堆积的主力 |
| ultra | ~4000 | 5 | 超长,单发即数秒~数十秒级 prefill |
| **合计** | | **67** | 并发 4(与渠道数对齐);队列配置保持现状,token 无限制故网关闸不介入 |

提示词构造:确定性英文填充段(带编号便于日志核对)+ 结尾指令 `Reply with exactly: OK`——输入长度是唯一变量,输出恒短。

## 观测面

| 数据 | 来源 | 采样 |
|---|---|---|
| **EWMA 参数**(samples/successEWMA/latencyEWMA/multiplier/lastSampleAt) | `GET /api/concurrency/routing-health`(RootAuth;本轮新增观测端点,契约修订见提交说明) | 每 3s 全程 |
| channel 在飞 | `GET /api/concurrency/snapshot`(既有,channel 行) | 每 3s(与上合并节拍) |
| **每请求决策归因**(哪个渠道服务、耗时、token) | `GET /api/log`(type=0,模型过滤;channel_id/channel_name 字段) | 打流中每 5s 增量拉取 + 结束全量 |
| 打流侧 | 脚本本地记录:tier/长度/开始结束时间/HTTP 状态 | 逐请求 |

注:管理 API 采样合计 ≤2 次/s,贴合 GlobalAPIRateLimit(360/180s/IP)不触发。

## 指标与判读

1. **分布**:渠道×档位请求数矩阵;份额偏差(理想 25%±5pp)。
2. **参数时间线**:每渠道 multiplier 曲线;warm 门越过时刻(samples≥10)前后 multiplier=1.0→分化的转折。
3. **因果链样本**:至少 1 条"ultra/long 落在渠道 X → X 的 latencyEWMA 抬升 → multiplier 下行 → 下一批长任务改道"的时序证据。
4. **公平性**:Jain 指数(全量与分档各算一次)。
5. **副产品**:队列/snapshot 的 queued 值不应出现(网关闸不介入的预期确认)。

## 通过标准

- 67/67 请求 200(上游异常单独归因,不计入均衡判定);
- 4 渠道全部被选中且各 ≥5 请求;无渠道份额为 0;
- multiplier 出现与延迟方向一致的分化(不要求幅度,同质上游预期分化小,以参数可解释为准);
- 日志归因与脚本记录逐请求对齐(渠道名含 lb- 编号)。

## 执行步骤

1. 加观测端点(代码 + 重编 + 重启网关)——见提交 `feat(lb-test)`。
2. API 建 lb-test-token、lb-01..04,暂停渠道 1/2,探针单条验证链路。
3. 跑 `loadbalance-ewma-test.mjs`(打流+采样+归因+落盘 JSONL)。
4. 出报表 `REPORT-LOADBALANCE.md`;恢复渠道 1/2。

## 风险与边界

- 上游为真实推理集群,ultra 档 5 发成本可接受;总输入 ~28 万 token 量级。
- EWMA 为进程内存态,重启网关即清零——拓扑就绪后才重启/打流,顺序不可反。
- 4 渠道同质上游,延迟差主要来自服务端排队抖动——分化幅度小是**预期结果**,判读重在参数可解释与决策归因,而非强行制造不均。
