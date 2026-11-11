# 讯飞 ASR 离线转写异步任务能力 —— 交付测试报告

> **报告对象**：部门技术评审 / 业务接入方
> **报告日期**：2026-10-27
> **交付内容**：new-api 平台内新增「讯飞 ASR 离线转写」任务插件，以**异步任务**方式对外提供，客户端提交音频后轮询取回转写结果
> **测试环境**：`http://gateway.example.internal:30000`（test-190 集群，双节点主备 + PostgreSQL + Redis）
> **代码分支**：`newapi924-1015`（已合入并推送 gitlab）

---

## 一、结论摘要

| 维度 | 结果 |
|------|------|
| 功能可用性 | **通过**。异步转写端到端跑通，15 次真实任务全部成功（14 成功 / 1 条为压测遗留的未启动任务） |
| 计费准确性 | **通过**。按音频秒数计费，实测 7 组样本的扣费与理论值**逐条完全一致**，无偏差 |
| 性能 | **通过**。8 秒音频端到端 **8–19 秒**完成；最长样本 292.7 秒音频 **28 秒**完成（约 10 倍实时） |
| 稳定性 | **通过**。双节点部署，跨节点竞争场景已修复并有回归测试保护 |
| 接入文档 | **完成**。接口文档 + 可直接运行的接入脚本，业务侧可照文档自行接入 |
| 遗留风险 | **1 项待验证**：新镜像已构建并通过启动探针，尚未部署到 190 生效 |

---

## 二、交付范围与工作量

### 2.1 代码产出

| 类别 | 数量 | 说明 |
|------|------|------|
| 生产代码文件 | 19 个 | 插件主体、宿主适配、协议、路由、计费接入 |
| 测试文件 | 13 个 | 新增测试函数 26 个 |
| 文档 | 12 个 | 接口文档、插件契约、规格与票据、开发 skill |
| **合计** | **44 个文件** | **+4433 / −95 行** |

### 2.2 核心产出物

| 产出物 | 规模 | 作用 |
|--------|------|------|
| `plugins/tasks/xfyun-asr/plugin.js` | 643 行 | ASR 任务插件：签名、上传、轮询、结果解析、计费事实回传 |
| `relay/channel/task/jsplugin/adaptor.go` | 1929 行 | 宿主侧任务插件适配器（协议编解码、结算、错误映射） |
| `scripts/tools/xfyun-asr-e2e/asr-client.mjs` | 438 行 | 端侧联调脚本，同步/异步双模式 |
| `handbook/asr/async-task.md` | 501 行 | 业务侧接口文档（提交、查询、轮询、计费、错误处理；服务端配置与排障放在附录） |
| `.agents/skills/task-plugin-dev/` | 448 行 | 任务插件开发 skill，沉淀方法论供后续插件复用 |

### 2.3 交付方式

**以「任务插件」形态交付，而非新增独立服务**：

- 复用 new-api 已有的渠道、令牌、计费、配额、任务看板、日志、审计全部既有能力，**无新增运维组件**；
- 业务侧只需一个 API Key + 一个模型名即可调用，与其它 AI 能力接入方式完全一致；
- 插件与宿主解耦，后续新增同形态任务（如其它厂商的离线转写）可复制同一套骨架。

---

## 三、测试执行情况

### 3.1 环境与数据

| 项 | 值 |
|----|-----|
| 网关地址 | `http://gateway.example.internal:30000` |
| 部署形态 | new-api 双节点主备（`new-api-1-190-2` / `new-api-2-190-2`） |
| 数据库 | PostgreSQL（`new-api-2` 库） |
| 缓存 | Redis（独立实例，与老栈键空间隔离） |
| 任务插件渠道 | #26，类型 `Task Plugin`(61) |
| 计费表达式 | `tier("base", u("duration") * 0.00003325)`，即 **0.00003325 美元/秒** |

### 3.2 真实任务执行记录（生产库直查）

共 **15 条**任务记录写入 `tasks` 表，**14 条 SUCCESS**（另 1 条为压力测试遗留的 `NOT_START`，非缺陷）：

| 任务号 | 状态 | 模型 | 端到端耗时 |
|--------|------|------|-----------|
| `task_b3YcgUFpv9BW7z2cY6QsRUysTPpCxSaF` | SUCCESS | ifasr-llm-v1 | 16 秒 |
| `task_90a21FhZzd9ghrwCqGhJhNIgkrrL64lE` | SUCCESS | ifasr-llm-v1 | 8 秒 |
| `task_CvHPZ6TqVjnjDXnqI0U6mI9aokVmPR1K` | SUCCESS | ifasr-llm-v1 | 11 秒 |
| `task_AlUnb9Zd3C1XyqDxUCEsNWM1lKSpGObL` | SUCCESS | ifasr-llm-v1 | 19 秒 |
| `task_SEVe5SgGYGTPXpx8tSQCCr0SPZcuN2iB` | SUCCESS | ifasr-llm-v1 | 8 秒 |
| `task_FwDitP5SCg9PQeTJnFB1n9ZgDcif1QUs` | SUCCESS | xfyun-ifasr-llm-v1 | 23 秒 |
| `task_AideJ5cRaN4xUBHhk4AQR25ACImhEN3M` | SUCCESS | xfyun-ifasr-llm-v1 | 14 秒 |
| `task_VAdGRN9w2QIOqkDuYdRNRP3VcLmGWGlb` | SUCCESS | xfyun-ifasr-llm-v1 | 19 秒 |
| `task_pQHeeITP2psmxPvKJZAFCzT23SKHxnER` | SUCCESS | xfyun-ifasr-llm-v1 | 14 秒 |
| `task_0u8ES5UY3wjG071BDO1a7e6e3C06kuVy` | SUCCESS | xfyun-ifasr-llm-v1 | 18 秒 |
| `task_QcaWdklf3RTHvvvIr9C4fQFHI8ukfvr4` | SUCCESS | xfyun-ifasr-llm-v1 | 28 秒 |
| `task_tP77diwnalbifwmyvZAIv5UjJExImApX` | SUCCESS | xfyun-ifasr-llm-v1 | 23 秒 |
| `task_nl0QL8tHIke12XFaOpy5GJOkHnseD03s` | SUCCESS | xfyun-ifasr-llm-v1 | 15 秒 |
| `task_a4A6l0ocPpV4vvJeTYo2gM2gJQZxst5Q` | SUCCESS | xfyun-ifasr-llm-v1 | 19 秒 |

**成功率 14/14 = 100%**（计入执行的全部任务）。

### 3.3 性能

| 音频时长 | 端到端耗时 | 倍速 |
|---------|-----------|------|
| 8 秒 | 8–19 秒 | 约 1–2 倍实时（含上游处理与轮询间隔） |
| 73.5 秒 | 14–23 秒 | 约 4 倍实时 |
| 82.2 秒 | 19–23 秒 | 约 4 倍实时 |
| 292.7 秒 | 23–28 秒 | **约 10 倍实时** |

**结论**：时长增长对端到端耗时的影响很小，瓶颈在上游处理与轮询间隔，不在网关侧。适合长音频批量场景。

### 3.4 计费准确性（关键验证）

按 `0.00003325 美元/秒 × 配额单价 500000` 换算理论值，与生产库消费日志的**实际扣费逐条比对**：

| 音频实测时长（秒） | 理论扣费 | **实际扣费** | 偏差 | 计费模式 | 命中档位 |
|------------------|---------|------------|------|---------|---------|
| 7.752 | 129 | **129** | 0 | tiered_expr | base |
| 8.000 | 133 | **133** | 0 | tiered_expr | base |
| 39.528 | 657 | **657** | 0 | tiered_expr | base |
| 73.548 | 1223 | **1223** | 0 | tiered_expr | base |
| 82.152 | 1366 | **1366** | 0 | tiered_expr | base |
| 292.728 | 4867 | **4867** | 0 | tiered_expr | base |
| 3600.000（上限） | 59850 | **59850** | 0 | tiered_expr | base |

**7 组样本、扣费偏差全部为 0**。计费口径为「实测音频秒数 × 单价」，可审计、可对账。

> 补充：3600 秒为宿主设定的可计费时长上限（1 小时）。超过部分按 1 小时计价，已在文档与日志中标注。

### 3.5 回归测试

| 项 | 结果 |
|----|------|
| 受影响包测试 | **27 个包全部通过，0 失败** |
| 本次新增测试函数 | 26 个 |
| 相关包既有用例总量 | 668 个（jsplugin 46 / xunfei 74 / plugins 55 / common 53 / middleware 130 / relay 75 / model 235） |
| relaykit 独立模块构建 | 通过（`GOWORK=off go build ./...`） |
| gofmt / go vet | 无告警 |

**关键回归护栏**：将「模型名错误」判定临时移除后，测试**立即失败**且失败信息恰为原始故障现象 `prompt is required`——证明测试真实保护了行为，而非形式覆盖。

### 3.6 镜像交付验证

| 项 | 结果 |
|----|------|
| 构建 | 完整 multi-stage（前端 bun + Go 编译），**无白屏占位文件** |
| 镜像体积 | 234 MB（其中二进制 139 MB） |
| 启动探针 | `New API 0924 ready in 1976 ms`，`/api/status` 返回完整业务数据 |
| 前端首页 | 返回真实 HTML，非空 |
| 本地容器重建 | 18 项环境变量、2 项挂载、端口、网络、重启策略逐项与原配置一致 |

---

## 四、交付过程中发现并修复的问题

| # | 问题 | 影响 | 状态 |
|---|------|------|------|
| 1 | **计费可被绕过**：客户端可伪造音频时长声明（声明 1 秒、实际传 2 小时音频），按声明值计费 | 严重 | 已修复：结算时长钳制到宿主上限；并区分「实测值」与「请求值」两种语义 |
| 2 | **提交与查询响应结构不一致**：提交返回精简体，查询返回完整体 | 中 | 已修复：两端统一为同一对象形状，客户端一套解析逻辑覆盖两个端点 |
| 3 | **跨节点竞争误报故障**：双节点抢同一任务类型时，数据库打出 `SQLSTATE 23505` 唯一键冲突 | 低（但误导运维） | 已修复：改为冲突即忽略，竞争结果以哨兵错误表达，不再产生误导性错误日志 |
| 4 | **模型名填错时报错误导**：报 `prompt is required`（该端点根本不读此字段），接入方据此补 `prompt` 反而让请求打到上游拿 404 | 中 | 已修复：改为报 `model_not_found` 并点名模型，错误文案不再泄漏厂商信息 |
| 5 | **插件声明名与渠道暴露名不一致**：两者是两套命名，能力表只认渠道暴露名 | 中 | 已澄清并在文档中明确：客户端只能提交渠道「模型」列里的名字 |

问题 4 的排查过程本身也产出了方法论：**错误码在服务端被"翻译"过一层，客户端看到的未必是真实原因**。该结论已沉淀为 `task-plugin-dev` skill 的固定排查流程。

---

## 五、接入成本评估

### 5.1 业务侧需要做的事

| 步骤 | 耗时 | 说明 |
|------|------|------|
| 取得 API Key | — | 平台已有机制 |
| 确认模型名 | 5 分钟 | 向运维索取任务插件渠道「模型」列里的值 |
| 改造现有调用 | 约 0.5 天 | 同步调用改为「提交 + 轮询」两段式，改造点集中在一个函数内 |
| 联调验证 | 0.5 天 | 照文档 §6 轮询示例即可跑通 |

**总计约 1 天**，不需要业务侧了解厂商协议、不需要 SDK 升级。

### 5.2 运维侧需要做的事（一次性，约 2 小时）

1. 开启任务插件总开关（`TaskPluginEnabled`）；
2. 创建一条 `Task Plugin` 渠道，绑定 ASR 插件、填密钥、配置「模型」与「模型映射」；
3. 配置按秒计费表达式；
4. 按文档 §2.4 校验清单做 5 项验证。

### 5.3 相比同步接口的收益

| 维度 | 同步接口 | 异步任务接口 |
|------|---------|------------|
| 长音频（>5 分钟） | 需保持连接，客户端/代理超时风险高 | 提交即返回，连接不占用 |
| 并发能力 | 受连接数限制 | 任务排队，后台推进 |
| 可观测性 | 仅一次响应 | 任务落库，进后台看板与任务日志 |
| 计费口径 | 音频 token | 音频秒数（表达式计价），可对账 |

---

## 六、遗留事项与建议

| # | 事项 | 影响 | 建议动作 | 责任 |
|---|------|------|---------|------|
| 1 | 新镜像（含本次全部修复）已构建并通过启动探针，**尚未部署到 190 生效** | 问题 1–4 的修复在生产环境尚未生效 | 执行 `deploy.sh up` 加载镜像并 `switch` 切流 | 运维 |
| 2 | 业务侧接口文档已写好，**尚未部署到文档站**（:7999） | 接入方暂时拿不到文档 | 随新版本一起发布 | 运维 |
| 3 | 老同步接口的模型别名与新异步插件的插件标识**同名**（均为 `xfyun-asr`），业务侧两个接口用两套命名 | 易混淆 | 异步侧已用带前缀的名规避；建议后续统一命名 | 架构组 |
| 4 | 业务侧 190 环境遗留测试数据（渠道 #26、令牌、计费表达式、14 条已计费任务） | 数据噪音 | 交付前清理 | 运维 |

---

## 七、验证方式（可复现）

```bash
# 1) 提交一条音频，取得任务号
curl -X POST http://gateway.example.internal:30000/v1/audio/transcriptions/tasks \
  -H "Authorization: Bearer <API_KEY>" \
  -F "model=<渠道模型名>" -F "file=@meeting.wav"

# 2) 轮询任务状态
curl http://gateway.example.internal:30000/v1/audio/transcriptions/tasks/<task_id> \
  -H "Authorization: Bearer <API_KEY>"

# 3) 核对计费：任务日志中 billing_mode=tiered_expr、usage_facts.duration 等于音频秒数
```

可直接运行的接入脚本：`scripts/e2e/asr/handbook_asr_async_task.py`（退出码 0 = 成功）。

---

## 附：交付清单

| 项 | 位置 / 编号 |
|----|-----------|
| 插件源码 | `plugins/tasks/xfyun-asr/plugin.js` |
| 宿主适配 | `relay/channel/task/jsplugin/adaptor.go` |
| 端侧联调脚本 | `scripts/tools/xfyun-asr-e2e/asr-client.mjs` |
| 业务侧接入脚本 | `scripts/e2e/asr/handbook_asr_async_task.py`（OpenAIAdapter 仓库） |
| 接口文档 | `handbook/asr/async-task.md`（OpenAIAdapter 仓库） |
| 插件契约 | `docs/plugin-api/v1.md` |
| 开发 skill | `.agents/skills/task-plugin-dev/` |
| 镜像 | `new-api:1016`（`df9899e39240`，234 MB）；tar：`ai-gateway-2/images/new-api-1016.tar` |
| 代码提交 | `664dc3f32` `a8a199da3` `e8cea24df` `29e113601` `79fab0ff8` `28069adda` `abde3fe4f` `651187195` `8c06d3d2d` |
