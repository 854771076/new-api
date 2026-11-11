# concurrency-test — 并发/负载均衡集成测试(自建)

本目录是并发控制(02-01..02-17)与 EWMA 负载均衡追加轮的**自建集成测试脚本**与实测报告,全部脚本针对**测试者自建的本地/内网测试网关**运行,不接触任何生产环境。

## 凭据声明(密钥扫描豁免)

脚本内出现的 token 与密码均为自建测试实例的凭据,泄露无影响:

| 凭据 | 性质 | 所在脚本 |
|------|------|----------|
| `h5LJ3…N9gYObt` | 本地网关(127.0.0.1:3001)名为 `concurrency-test` 的测试 token(limit 3/5),实例销毁即失效 | load-test / queue-test / queue-scenario-test / retry-load-test / test-local / hold-buckets |
| `sk-aFDp…qawqIXnmnJ` | 内网测试网关(upstream.example.internal:30000)测试 token | test.mjs |
| `root` / `<test-password>` | 本地测试实例 root 初始密码(自建实例,非任何共享/生产系统) | coding-client-sim / lb-setup / loadbalance-ewma-test / queue-scenario-test |

这些值是集成测试自建内容的一部分,密钥扫描(gitleaks、detect-secrets 等)应跳过本目录——仓库根 `.gitleaks.toml` 已对 `concurrency-test/` 做 allowlist,行内同时带 `gitleaks:allow` / `pragma: allowlist secret` 标注。若复制本目录到新环境,先在自建实例上重建同名 token 再替换。

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
