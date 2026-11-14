# 集成测试策划案 — 并发上限 × 有界等待队列 × 429 降级

> 本文件是**执行入口**:测什么、怎么排、产什么、怎么扩展。
> 决策依据见 `INTEGRATION-TEST-PLAN.md`(spec,11 问定案);参数见 `config.json`(唯一配置源,已 git-exclude);脚本索引见 `README.md`。

## 1. 目标与成功标准

**主目标**:拿到可用于生产与真实测试环境的配置经验 —— 一张参数查表、一个折算公式、一份生产 checklist。

成功标准(全部达成即方案闭环):

1. M5 扫出拐点:「到达率 ↔ 队列长 ↔ 1min 吞吐 ↔ 429 率 ↔ p95」五元组能拟合,且能回答你最初的经验假设「1min 处理量 ≈ 队列长度时体验良好」成立与否。
2. 429 对账等式成立:客户端 429 计数 ≡ 服务端 rejections 轮间差值(逐档)。
3. 四项体验指标(完成率/p95/首击 429 率/终失败率)对每档给出合格判定。
4. 容灾四场景(D1 杀 Redis、D2 杀节点、D3 热调配置、D4 auto-ban 可选)行为符合设计预期并留证。

## 2. 被测对象与环境

| 角色 | 拓扑 | 说明 |
|------|------|------|
| 主矩阵 | 单节点:`new-api:20261015` 容器 → 3001 | SQLite 用 `docker-test-data/` **副本**,Redis 连宿主 6379 |
| 容灾专项 | 双节点:测试 compose(待建)→ 3001/3002 | 两节点各持 SQLite 副本、**共享**宿主 Redis;lease TTL=30s(soakenv 覆盖) |
| 上游(fake) | 本机 mock 上游(`mock-upstream.mjs`,sleep 模拟延迟) | 固定延迟 800ms ± 抖动,零成本、无上游压力,主矩阵全程 |
| 上游(real) | upstream.example.internal:30000 | 仅 M6 收尾轮,受 token 预算约束(0=跳过) |

**执行期修正(2026-10-15,方案实施时确认)**:
1. 假上游由 delay-proxy 改为**独立 mock 上游**——delay-proxy 仍会把真实 LLM 流量打到上游,高并发扫参轮不可接受;mock 直接返回固定补全,延迟可控、并发无上限。
2. 主矩阵 `channelLimit` 置 **0(不限)**——渠道闸无排队、会先于 key 队列饱和,渠道终端 429 会掩盖 Q 扫参梯度;渠道轴 limit=3 行为已有 02-14 专项覆盖,要复现组合行为把 config 改回 3。
3. 新增 `load.clientConcurrency=20`——每客户端同时在飞 20 任务,单 key 在飞(20)> 3+Q(默认档 15)才能让各队列档位产生 429/等待梯度;客户端仍按各自 50 任务总量计。

**环境铁律**:一切读写只打本机/内网测试实例与 db 副本;真实凭据只填 `config.json`(不入 git);杀 Redis/节点前按容器名+compose project 核对归属。

## 3. 指标与口径

| 指标 | 定义 | 权威来源 | 备注 |
|------|------|----------|------|
| 完成率 | 终态 200 / 总任务 | 客户端 | |
| 端到端 p50/p95 | send → 完成 | 客户端 | 含排队+上游 |
| 首击 429 率 | 首发即 429 / 首发 | 客户端 | |
| 终失败率 | 重试耗尽仍失败 / 总任务 | 客户端(robust 画像) | |
| 429 对账 | Σ客户端429 ≟ Δrejections | 客户端 + 快照(轮间差值) | rejections 进程起算,重启清零 |
| 1min 处理量 | COUNT(成功日志, 分钟窗) | 日志账本 | 验证吞吐≈队列长假设 |
| queued 均值/峰值、inFlight | 每 3s 快照轮询 | 快照端点 | history 10s 采样仅留证 |
| 排队成本 | p95(有队列档) − p95(M1 基线) | 推导 | **不加服务端埋点**的替代口径 |
| 合格线 | 完成率 ≥99% 且 p95 ≤ 3×基线 | config | 可用绝对 p95 覆盖 |

429 归因不看响应体(key/channel 对外同形是契约),全靠脚本侧记账 + 内部日志。

## 4. 测试项总表

| 编号 | 目的 | 队列 | 拓扑 | 负载 | 预期/产出 |
|------|------|------|------|------|-----------|
| M1 | 无队列基线 | ratio=0 | 单渠道,10key | 突发20%+阶梯 | p95 基线、无排队 429 形态 |
| M2 | 单渠道+默认队列 | 4/128/10s | 单渠道,10key | 同上 | Q=min(4×3,128)=12 档行为 |
| M3 | 3 渠道对照 | 同上 | 3 渠道(同质),10key | 同上 | 渠道轴不排队,归因收敛 |
| M4 | 共享 key 对照 | 同上 | 3 渠道,共享1key | 同上 | 全局抢位公平性 vs M3 |
| **M5** | **队列 5 档 × 阶梯(核心)** | noqueue/tiny/default/deep/aggressive | 3 渠道,10key | 每档 1→2→4→6→8→12 rps×60s | **查表 + 拐点 + 公式拟合** |
| M6 | 真实上游收尾 | default | 3 渠道,10key | 阶梯,1-2 轮 | 真实延迟下结论复核(看预算) |
| D1 | 杀 Redis | default | 单节点打流中 | 持续 | fail-open、Degraded 三态、合并计数、恢复回溯 |
| D2 | 杀节点 | default,TTL=30s | **双节点**打流中 | 持续 | 租约接管、在途归零、N×Q 语义 |
| D3 | 热调队列参数 | 打流中改 4 项 | 双节点 | 持续 | 跨节点传播 ≤60s 生效 |
| D4 | auto-ban 恢复 | 默认**跳过** | config 开关 | — | 可选,时间富余才跑 |

每轮固定收尾断言:压后 `queued=0` 且 `inFlight=0`(无泄漏,复用 S4 口径)。

## 5. 执行编排

```
阶段0 准备   config _todo 检查 → 起容器(副本库)→ 配额 top-up → ERROR_LOG 确认
             → 起 delay-proxy → 探针单发 → 拓扑布置(key/channel/queue)
阶段1 主矩阵 M1 → M2 → M3 → M4 → M5(基线先行,核心最后,保证基线未被污染)
阶段2 容灾   D1 → D2 → D3(每轮独立重置环境;D4 视预算)
阶段3 真实轮 M6(仅当 realBudgetTokens>0)
阶段4 合成   逐轮 summary → 综合报告(查表/公式/checklist 三节)→ 提交留档
```

- **每轮固定步骤**:设拓扑/队列 → 快照轮询起 → 打负载 → 落盘 → 即时断言(对账+无泄漏)→ 不过则暂停进报告,不带病跑下一轮。
- **预算熔断**:超过 `maxWallHours` 立即停,已得数据照常出报告。

## 6. 数据落盘约定

```
concurrency-test/results/<轮号>-<YYYYMMDD-HHmm>/
  clients.jsonl    # 每任务一行: client, profile, task, tier, sendTs, doneTs, status, attempts, firstByteMs
  snapshot.jsonl   # 每 3s 一行: t, inFlight, queued, rejections, degraded
  summary.md       # 该轮指标表 + 合格判定;必带镜像tag、config 快照、起止时间指纹
```

综合报告:`concurrency-test/REPORT-INTEGRATION.md`(与既有 REPORT 同惯例)。

## 7. 风险与回滚

| 风险 | 防线 |
|------|------|
| 流量误伤生产 | baseUrl 白名单仅 127.0.0.1/localhost;real 轮必须 config 显式开启 |
| 真实上游成本 | 跑前校验 `realBudgetTokens`,不填=跳过 M6 |
| db 损坏 | 永远操作 `docker-test-data/` 副本,原 `one-api.db` 只读 |
| 凭据泄露 | `config.json` 已 git-exclude;杀容器按名核对 |
| 时间超预算 | 熔断规则见 §5,报告不受影响 |

## 8. 扩展指南(后续怎么加)

- **加一轮测试**:在 `config.json` 的 `rounds` 加一个条目(指定 queue/channels/keys/负载)→ 执行器自动识别;无需改代码。
- **加一个参数**:config 加键并给默认值,执行器读不到就用默认——所有默认都放 config,不散在脚本里。
- **加一项客户端指标**:只在 `clients.jsonl` 加字段+报告器加一列(客户端侧自由);**服务端要出新字段 = 改产品代码**,须另起 spec,不塞进本方案。
- **加一种客户端画像**:`clientProfiles` 加类型(如 `aggressive-retry`),分配规则沿用对半模式。
- **快速回归**:既有 S1-S5 队列场景套件保留为冒烟,大矩阵前先跑它确认环境没坏。

## 9. 待办

- [ ] 负责人填 `config.json` 4 个 `_todo`(真实轮预算/时间墙/绝对 p95 可选/生产规模)
- [ ] harness 三件:参数化 delay-proxy、双节点测试 compose、执行器+报告器
- [ ] (可选)发布 spec 到 issue tracker — 需先 `/setup-matt-pocock-skills`
