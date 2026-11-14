# ADR-0001: New-API Redis Sentinel 高可用支持

> **Status**: ✅ Implemented (2026-08-19 完成,2026-09 文档化)
> **Date**: 2026-08-19(原始 plan) / 2026-09(本文档化)
> **Deciders**: 主代理 + 用户
> **Source plan**: `.trae/documents/redis-sentinel-support.md`(已迁移)

---

## Context

让 new-api-monster 支持 Redis Sentinel 集群模式,可通过环境变量切换;docker 编排可切换到 sentinel 拓扑;本地以 30000 端口启动并自验。

### 原始状态(2026-08-19 蒸馏时点)

- `common/env.go` 当前唯一读取的 Redis 配置是 `REDIS_CONN_STRING`,没有 sentinel 相关变量。
- `common/redis.go` 在启动时调用 `redis.NewClient(&redis.Options{Addr: ..., Password: ..., DB: ...})` 解析 URL。
- `docker-compose.yml` 当前只有一个 `redis` 单点服务(密码 `123456`,无 sentinel)。
- `.env.example` 注释里只有 `REDIS_CONN_STRING`。
- 容器名 `redis`、`postgres`、`new-api`,new-api 容器 depends_on `redis`。
- 上一轮已用 30000 端口拉起。

### 实施结果(2026-09 验证)

通过文件系统扫描确认以下交付物已落地:

| 交付物 | 路径 | 状态 |
| --- | --- | --- |
| 多阶段 Dockerfile | `Dockerfile.sentinel` | ✅ 存在 |
| Sentinel overlay compose | `docker-compose.sentinel.yml` | ✅ 存在 |
| 单点 fallback compose | `docker-compose.single.yml` | ✅ 存在 |
| Sentinel 启动脚本 | `scripts/start-sentinel.ps1` | ✅ 存在 |
| 单点启动脚本 | `scripts/start-single.ps1` | ✅ 存在 |
| `common/redis.go` 双分支 | `common/redis.go`(含 `sentinel` 字样) | ⚠️ 待 Phase 0 codegraph 复核(需  codegraph analyze) |
| `.env.example` sentinel 块 | `.env.example` | ⚠️ 待复核 |
| `docker/sentinel/conf/sentinel-{1,2,3}.conf` | `docker/sentinel/conf/` | ⚠️ 待复核 |

---

## Decision

### 1. 目标行为

1. 当 `REDIS_SENTINEL_MODE=true` 且 `REDIS_SENTINEL_ADDRS` 非空 → 使用 `redis.NewFailoverClient` 连接 sentinel,master 由 sentinel 自动选主。
2. 当 `REDIS_SENTINEL_MODE` 未设置或 `false` → 维持现状,使用 `REDIS_CONN_STRING` 单点连接。
3. `.env.example` 增加 sentinel 配置示例块。
4. `docker-compose.yml` 增加可选 sentinel 拓扑(默认注释,用户可通过复制到独立 `docker-compose.sentinel.yml` 启用)。
5. 本地构建镜像 → 30000 端口启动 → `/api/status` 健康检查通过 → 容器内 redis-cli 验证 sentinel master 指向正确数据节点。

### 2. 假设与决策

| 决策 | 取值 | 依据 |
| --- | --- | --- |
| Redis 客户端 | go-redis v9(升级) | 用户回答「同步升级到最新版」 |
| Sentinel 模式开关 | 环境变量 `REDIS_SENTINEL_MODE=true/false` | 用户给定的配置风格 |
| Master 名 | `mymaster`(可通过 `REDIS_SENTINEL_MASTER_NAME` 覆盖) | go-redis 文档默认 |
| 哨兵端口 | 26379/26380/26381 | 用户给定 |
| 启动端口 | 30000 | 用户在上一轮已使用 |
| docker 拓扑 | 3 sentinel + 1 master + 2 replica | 用户要求「打通功能」 |
| 现有单点 redis 服务 | 保留,向后兼容 | 不破坏现有部署 |

### 3. 实施步骤(历史 plan,已完成)

#### Phase 0 — codegraph 索引与代码定位(已跑)


#### Phase 1 — Go 代码改动(已落地)

文件 `common/redis.go`:
1. 顶部新增 sentinel 环境变量读取:`redisSentinelMode` / `redisSentinelAddrs` / `redisSentinelPassword` / `redisSentinelMasterName` / `redisPassword`。
2. 改造 `InitRedis` 分支:`if redisSentinelMode && redisSentinelAddrs != ""` 走 `redis.NewFailoverClient(&redis.FailoverOptions{...})`;否则维持原 `ParseURL + NewClient` 路径。
3. 哨兵模式时跳过 URL 解析但保留 `REDIS_CONN_STRING` 作为注释说明(不抛错,仅打 info log)。
4. 启动日志区分两种模式(`logger.SysLog`)。

文件 `go.mod` / `go.sum`:`go-redis` 升级至 v9.x;`go mod tidy`。

文件 `.env.example`:Redis 块下方追加 sentinel 示例。

#### Phase 2 — docker 编排(已落地)

- `docker-compose.yml`:保留原 `redis` 单点服务(向后兼容)。
- `docker-compose.sentinel.yml`:sentinel 拓扑(3 sentinel + 1 master + 2 replica),`new-api` 服务端口 `30000:3000`,`depends_on` 增加 sentinel-1/2/3,environment 追加 `REDIS_SENTINEL_MODE=true` / `REDIS_SENTINEL_ADDRS=sentinel-1:26379,...` / `REDIS_PASSWORD=123456`。
- `docker-compose.single.yml`:单点模式回退。

#### Phase 3 — 构建与启动(脚本已落地)

```powershell
docker compose -f docker-compose.yml -f docker-compose.sentinel.yml build new-api
docker compose -f docker-compose.yml -f docker-compose.sentinel.yml up -d
Start-Sleep -Seconds 20
Invoke-WebRequest http://localhost:30000/api/status
docker exec new-api sh -c 'redis-cli -h sentinel-1 -p 26379 sentinel masters'
```

入口:`scripts/start-sentinel.ps1` / `scripts/start-single.ps1`。

---

## Verification(已部分完成,后续回归清单)

- [x] `Dockerfile.sentinel` 存在
- [x] `docker-compose.sentinel.yml` / `docker-compose.single.yml` 存在
- [x] `scripts/start-{sentinel,single}.ps1` 存在
- [x] `go build ./...` 通过 — 2026-09-23, exit 0, go1.25.1 windows/amd64
- [ ] `go mod tidy` 无 diff(待用户侧回归)
- [x] `docker compose -f docker-compose.yml -f docker-compose.sentinel.yml config` 合法 — 2026-09-23, exit 0 (single-node config also valid: exit 0)
- [x] `docker ps` 中 new-api `STATUS=healthy` — 2026-09-23, compose cycle 后 25s 转 healthy, RestartCount=0
- [x] `curl http://localhost:30000/api/status` 返回 `success: true` — 2026-09-23, exit 0, body `"success":true`
- [x] `docker exec new-api redis-cli -h sentinel-1 -p 26379 sentinel masters` 输出 `mymaster` 且 `flags=master` — 2026-09-23, verified via `docker exec sentinel-1 redis-cli -p 26379 sentinel master mymaster` (ip 172.28.0.10, runid non-empty)
- [ ] codegraph 分析与本次改动一致(待 Phase 4 复核)（Phase 1 范围外——ROADMAP SC-5 仅含前六项，本项属历史 Phase 4 范围）
- [x] **Automated failover validated** — 2026-09-23, T+6s after `docker stop redis-master` sentinel promoted `redis-replica2` (172.28.0.12) to master, `/api/status` HTTP 200 throughout, fix commit `451b2519e`. Pre-fix evidence: `evidence/failover-injection.log`; post-fix evidence: `evidence/failover-fix.log`.

---

## Risks & Rollback

- **风险**:现有调用方若有依赖 `*redis.Client` 具体类型(而非 `redis.UniversalClient`)且在哨兵模式下 `MasterAddr()` 行为变化,可能引发 panic。Phase 0.4 impact 会量化此风险。
- **回滚**:保留 `docker-compose.yml` 原 redis 单点服务,仅切换 `REDIS_SENTINEL_MODE=false` 即可回退;代码保留双分支,零侵入回退。

### Out of Scope(明确划线,避免范围蔓延)

- 不实现 Redis Cluster(用户只要 sentinel)
- 不动 billing/relay/auth 等业务模块
- 不引入新中间件,不改前端
- 不删除原 docker compose 服务

---

> **本 ADR 来源**:`.trae/documents/redis-sentinel-support.md`(2026-09 迁移至 `docs/adr/0001-redis-sentinel-support.md`)。
> **遵循**:AGENTS.md「重大架构决策 → 落 `docs/adr/NNNN-{slug}.md`」约定。

---

## Verification History

- **2026-08-19** — Implementation merged (per ADR § source). Conf files initially assumed `172.21.0.0/24` subnet; actual docker-compose sentinel network is `172.28.0.0/16`.
- **2026-09-23 (Phase 1, before fix)**: failover injection via `docker stop redis-master` revealed sentinel-2/3 monitored `172.21.0.10` (out of subnet), sentinel-1 announced `172.21.0.21`, `num-other-sentinels=0` made quorum=2 unreachable, no replica promoted in 90s window, `/api/status` HTTP 500 throughout. Evidence: `.planning/phases/01-baseline-verification/evidence/failover-injection.log`. ADR HA claim **partially false** until fixed.
- **2026-09-23 (fix commit `451b2519e`)** — corrected `docker/sentinel/conf/sentinel-{1,2,3}.conf`: sentinel-1 `announce-ip` → `172.28.0.21`, sentinel-2/3 `monitor mymaster` → `172.28.0.10`. Restart brought quorum=2 online (`num-other-sentinels=2`).
- **2026-09-23 (re-test, after fix)**: `docker stop redis-master` → T+6s sentinel promoted `redis-replica2` (172.28.0.12) → master, `/api/status` HTTP 200 throughout (single 41s reconnect on first post-failover request, subsequent 4-25 ms). Evidence: `.planning/phases/01-baseline-verification/evidence/failover-fix.log`. Clean cycle (down→up→healthy) re-run also passes (~57s to healthy, post-cycle sentinel consensus `num-other-sentinels=2, quorum=2, flags=master`). ADR HA claim now **substantiated by live test**.
