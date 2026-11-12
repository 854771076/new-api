# concurrency-test — 并发/负载均衡集成测试(自建)

本目录是并发控制(02-01..02-17)与 EWMA 负载均衡追加轮的**自建集成测试脚本**与实测报告,全部脚本针对**测试者自建的本地/内网测试网关**运行,不接触任何生产环境。

## 凭据声明(脚本内不含真实凭据)

脚本**不硬编码任何真实 token / 密码 / 内网地址**。所有凭据与目标地址从环境变量读取,缺省值是占位符或本地默认:

| 变量 | 用途 | 缺省 | 用到它的脚本 |
|------|------|------|----------|
| `ADMIN_USER` / `ADMIN_PASSWORD` | 本地测试实例 root 登录 | `root` / 空 | coding-client-sim / lb-setup / loadbalance-ewma-test / queue-scenario-test |
| `TEST_BASE_URL` / `TEST_API_KEY` | 被测网关地址与 token | `http://upstream.example.internal:30000` / 空 | test.mjs |
| `UPSTREAM_HOST` / `UPSTREAM_PORT` | 延迟代理的上游地址 | `upstream.example.internal` / `30000` | delay-proxy.mjs |
| `SIM_KEY` / `SIM_CONFIG` / `SIM_CLIENTS` / `SIM_REQUESTS` | 接入方画像模拟参数 | 见脚本头注释 | coding-client-sim.mjs |
| `LOAD_DURATION_MS` / `LOAD_BURST` / `LOAD_INTERVAL_MS` | 持续打流参数 | 见脚本头注释 | load-test / queue-test / retry-load-test 等 |

`config.json` 是**唯一配置源**(已 git-exclude,填凭据不入库),`integration-run.mjs` / `capacity-run.mjs` 读它;其余脚本走环境变量。

使用前在自建实例上重建 token 并设好环境变量即可:

```bash
export ADMIN_USER=root ADMIN_PASSWORD='<你的实例密码>'
export TEST_BASE_URL='http://127.0.0.1:3001' TEST_API_KEY='<你的测试 token>'
```

仓库根 `.gitleaks.toml` 仍对 `concurrency-test/` 做 allowlist(该目录历史上含过自建测试凭据,扫描豁免以免误报),但目录内已不再保留真实值。

## 文档索引

- `TEST-PLAN.md` — **测试策划案(执行入口)**:测什么/怎么排/产什么/怎么扩展
- `INTEGRATION-TEST-PLAN.md` — spec:方案决策记录(11 问定案)
- `config.json` — 唯一配置源(已 git-exclude,填凭据不入库)
- `REPORT-*.md` / `LOADBALANCE-PLAN.md` — 既有专项报告

## 脚本索引

- `test-local.mjs` / `test.mjs` — 并发上限基础行为(顺序/突发/恢复/流式)
- `load-test.mjs` — 持续打流,为 /concurrency 快照页制造非零桶与 429
- `queue-test.mjs` / `queue-scenario-test.mjs` — 有界等待队列 S1..S5 场景套件
- `retry-load-test.mjs` — 150 任务 429 重试 + 退避阶梯
- `hold-buckets.mjs` — UI 截图用:并发长生成保持桶非空
- `coding-client-sim.mjs` / `multi-key-test.mjs` — 客户端形态/多 key 池模拟
- `lb-setup.mjs` / `loadbalance-ewma-test.mjs` — EWMA 负载均衡拓扑搭建与打流
- `delay-proxy.mjs` — 本地延迟注入代理
- `check-i18n.mjs` / `fix-i18n.mjs` — i18n key 巡检/修复
- `REPORT*.md` / `sim-report.jsonl` — 实测报告与结果数据
