/**
 * capacity-run.mjs — 多渠道复制 × 阶梯目标速率的容量边界测试。
 *
 * 用法: node capacity-run.mjs            = 跑 config.capacity.channelCounts 全部 N
 *      node capacity-run.mjs 4 8       = 只跑指定 N(前缀/精确都可)
 *
 * 思路: 以渠道 3(lb-01)为模板克隆 cap-01..cap-N(每渠道 concurrency_limit=3),
 *      对每个 N 按 config.capacity.offeredRps 阶梯打恒定到达率,找:
 *      - 边界 rps = 最后一个 PASS(完成率≥99% 且 429 率≤1%)的目标档
 *      - 实测天花板达成 rps 与理论 min(key 侧 10×3/时延, 渠道侧 3N/时延) 对比
 *
 * 约定(与 integration-run.mjs 一致):
 * - 一切拓扑变更走 HTTP API;数据落 results/capacity-N<N>-<ts>/ 与 results/CAPACITY-<ts>.md。
 * - 打流模型固定 cfg.upstream.realModel;非流;重试=0(容量轮测原始拒绝,首击口径)。
 * - 请求体短句 + max_tokens=32:目标是吞吐,时延由 mock(fixed sleep)决定。
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const DIR = path.dirname(fileURLToPath(import.meta.url));
const CFG = JSON.parse(fs.readFileSync(path.join(DIR, 'config.json'), 'utf8'));
const BASE = CFG.target.baseUrl;
const MOCK_HOST_PORT = CFG.upstream.mockPort;
const MOCK = `http://127.0.0.1:${MOCK_HOST_PORT}`;
const RESULTS = path.join(DIR, 'results');
const MODEL = CFG.upstream.realModel;
const URL_CHAT = `${BASE}/v1/chat/completions`;
const CLIENTS = CFG.load.clients; // pool-key 数量(与主矩阵一致 = 10)
const CAP = CFG.capacity;
const RUNG_MS = Math.round(CAP.rungSeconds * 1000);
const IN_FLIGHT_CAP = CAP.inFlightCap; // 全局在飞上限,达到时该次发送记 dropped_client(不计入 429)
const TICK_MS = 200; // 速率控制 tick:每 tick 发 rps/5,误差累计补偿
const PASS_COMPLETION = 0.99; // 档位判定:完成率≥99%
const PASS_429_RATE = 0.01; // 且 429 率≤1%
const DRAIN_MAX_MS = 30000; // 每档结束后等在飞清零的上限
const TEMPLATE_ID = 3; // 克隆模板:渠道 3(lb-01,指向 mock)
// 容量轮固定每渠道 concurrency_limit=3(不读 config.limits.channelLimit —— 那是主矩阵的闸;
// 3.75N rps 的理论天花板就建立在"每渠道 3"上)。
const CHANNEL_CONCURRENCY = 3;
// GET /api/channel/:id 走 model.GetChannelById(id,false)=Omit("key"),详情永远拿不到真 key;
// POST /api/channel/:id/key 需要 RootAuth+SecureVerification 脚本不便走。克隆渠道只指向 mock
// 上游(mock-upstream.mjs 不校验 Authorization),占位 key 即可通过 validateChannel(isAdd)。
const CLONE_KEY_PLACEHOLDER = 'sk-mock-capacity-clone';
// 容量轮上游模式:mock(本机打桩)或 real(内网真实集群,凭据取 config.upstream.realApiKey,
// 只落在 git-exclude 的 config.json 里,绝不进代码/提交)。
const CAP_MODE = CAP.mode || 'mock';
const CAP_BASE = CAP_MODE === 'real' ? CFG.upstream.realBaseUrl : `http://host.docker.internal:${MOCK_HOST_PORT}`;
const CAP_KEY = CAP_MODE === 'real' ? (CFG.upstream.realApiKey || '') : CLONE_KEY_PLACEHOLDER;
const LAT_TXT = CAP_MODE === 'real'
  ? 'real 模式:理论按 0.8s 假设,真实时延以实测 p50 为准'
  : `时延 ${CFG.upstream.fakeDelayMs}ms±${CFG.upstream.fakeJitterMs}ms`;

let ADMIN = ''; // admin bearer,ready() 里登录获取
let KEYS = {}; // pool-key-01..N 真 key(ensureKeys 取回)

// ---------------- 基础工具 ----------------
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const now = () => Date.now();
const p2 = (n) => String(n).padStart(2, '0');
const clock = (t = Date.now()) => {
  const d = new Date(t);
  return `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
};
const stamp = (t = Date.now()) => {
  const d = new Date(t);
  return `${d.getFullYear()}${p2(d.getMonth() + 1)}${p2(d.getDate())}-${p2(d.getHours())}${p2(d.getMinutes())}${p2(d.getSeconds())}`;
};
const log = (...a) => console.log(`[${clock()}]`, ...a);
const pct = (x) => (x == null ? 'N/A' : `${(x * 100).toFixed(1)}%`);
const ms = (x) => (x == null ? 'N/A' : `${Math.round(x)}ms`);
const rps1 = (x) => (x == null ? 'N/A' : x.toFixed(1));
const fail = (msg) => {
  console.error(`[fatal] ${msg}`);
  process.exit(1);
};

function quantile(sorted, q) {
  if (!sorted.length) return null;
  const pos = (sorted.length - 1) * q;
  const lo = Math.floor(pos);
  const hi = Math.ceil(pos);
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (pos - lo);
}

/** 重新登录(会话约 15 分钟过期;401 时由 api() 自动调用)。 */
let loggingIn = false;
async function ensureLogin() {
  if (loggingIn) return;
  loggingIn = true;
  const saved = ADMIN;
  ADMIN = null; // 登录不携带过期 token
  try {
    const lg = await api('POST', '/api/user/login', { username: CFG.target.adminUser, password: CFG.target.adminPassword });
    ADMIN = lg.data?.access_token;
    if (!ADMIN) fail('admin 登录未取得 access_token');
    log(`admin 会话已刷新 (${CFG.target.adminUser})`);
  } catch (e) {
    ADMIN = saved;
    throw e;
  } finally {
    loggingIn = false;
  }
}

/** 管理端 API:15s 超时;HTTP 非 2xx 或 success=false 都抛错并打印响应体;401 自动重登一次。 */
async function api(method, p, body, retried = false) {
  const opt = {
    method,
    headers: { 'Content-Type': 'application/json', 'New-API-User': '1' },
    signal: AbortSignal.timeout(15000),
  };
  if (ADMIN) opt.headers.Authorization = `Bearer ${ADMIN}`;
  if (body !== undefined) opt.body = JSON.stringify(body);
  let res;
  let text;
  try {
    res = await fetch(BASE + p, opt);
    text = await res.text();
  } catch (e) {
    log(`! ${method} ${p} 网络/超时: ${e?.name || e}`);
    throw new Error(`${method} ${p}: ${e?.name || e}`);
  }
  if (res.status === 401 && !retried && p !== '/api/user/login') {
    log(`! ${method} ${p} 会话过期(401),自动重新登录后重试一次`);
    await ensureLogin();
    return api(method, p, body, true);
  }
  let j = null;
  try {
    j = JSON.parse(text);
  } catch {
    /* 非 JSON(如 429 空 body) */
  }
  if (!res.ok || (j && j.success === false)) {
    log(`! ${method} ${p} -> HTTP ${res.status} body=${text.slice(0, 400)}`);
    const err = new Error(`${method} ${p} -> ${res.status}`);
    err.body = text;
    throw err;
  }
  return j ?? {};
}

// ---------------- N 选择 ----------------
/** 参数前缀/精确匹配 channelCounts;无参 = 全部。 */
function selectCounts(args) {
  const all = CAP.channelCounts;
  if (!args.length) return [...all];
  const picked = all.filter((c) => args.some((a) => String(c) === a || String(c).startsWith(a)));
  return picked;
}

// ---------------- A. 就绪检查 ----------------
async function ready() {
  const host = new URL(BASE).hostname;
  if (!['127.0.0.1', 'localhost'].includes(host)) {
    fail(`target.baseUrl 主机 ${host} 不在白名单(仅 127.0.0.1/localhost),拒绝继续`);
  }
  let st;
  try {
    st = await (await fetch(`${BASE}/api/status`, { signal: AbortSignal.timeout(15000) })).json();
  } catch (e) {
    fail(`网关 /api/status 不可达: ${e?.name || e}`);
  }
  if (!st?.success) fail(`网关 /api/status 返回失败: ${JSON.stringify(st).slice(0, 200)}`);
  log(`网关就绪 ${BASE} version=${st.data?.version || '?'} docker=${CFG.target.dockerContainer} capacity.mode=${CAP_MODE}`);
  if (CAP_MODE === 'real') {
    if (!CAP_KEY) fail('capacity.mode=real 但 upstream.realApiKey 为空(config.json)');
    let rs;
    try {
      rs = await fetch(`${CFG.upstream.realBaseUrl}/api/status`, { signal: AbortSignal.timeout(8000) });
    } catch (e) {
      fail(`真实上游不可达(${CFG.upstream.realBaseUrl}): ${e?.name || e}`);
    }
    if (!rs.ok) fail(`真实上游 /api/status -> ${rs.status}`);
    log(`真实上游就绪 ${CFG.upstream.realBaseUrl} model=${MODEL}`);
  } else {
    let h;
    try {
      h = await fetch(`${MOCK}/health`, { signal: AbortSignal.timeout(5000) });
    } catch (e) {
      fail(`mock 上游不可达(${MOCK}/health): ${e?.name || e} — 先起 mock-upstream.mjs`);
    }
    if (!h.ok) fail(`mock 上游 /health -> ${h.status}`);
    log(`mock 上游就绪 ${MOCK}(时延 ${CFG.upstream.fakeDelayMs}ms±${CFG.upstream.fakeJitterMs}ms)`);
  }
  await ensureLogin();
  log(`admin 登录成功 (${CFG.target.adminUser})`);
  log(`配置快照: capacity=${JSON.stringify({ channelCounts: CAP.channelCounts, offeredRps: CAP.offeredRps, rungSeconds: CAP.rungSeconds, inFlightCap: CAP.inFlightCap, queue: CAP.queue })} 每渠道concurrency_limit=${CHANNEL_CONCURRENCY}(固定常量)`);
}

// ---------------- B. 拓扑助手(全部 HTTP API) ----------------
/** 建/修 CLIENTS 个 pool key(limit=3)并按 id 取回未掩码真 key(同 integration-run)。 */
async function ensureKeys() {
  const list = await api('GET', '/api/token/?p=1&size=100');
  const items = list.data?.items || [];
  const want = [];
  for (let i = 1; i <= CLIENTS; i++) want.push(`pool-key-${p2(i)}`);
  const idOf = {};
  for (const name of want) {
    let t = items.find((x) => x.name === name);
    if (!t) {
      log(`创建 token ${name}(concurrency_limit=3)`);
      await api('POST', '/api/token/', {
        name,
        remain_quota: 0,
        unlimited_quota: true,
        expired_time: 0,
        model_limits_enabled: false,
        model_limits: '',
        allow_ips: '',
        group: '',
        cross_group_retry: false,
        status: 1,
        concurrency_limit: 3,
        concurrency_limit_by_model: '',
      });
      const after = await api('GET', '/api/token/?p=1&size=100');
      t = (after.data?.items || []).find((x) => x.name === name);
      if (!t) throw new Error(`token ${name} 创建后列表里找不到`);
    } else if (t.concurrency_limit !== 3) {
      log(`修正 token ${name} concurrency_limit ${t.concurrency_limit} -> 3(PUT 原对象+id)`);
      await api('PUT', '/api/token/', { ...t, concurrency_limit: 3 });
    }
    idOf[t.id] = name;
  }
  // 列表/详情都掩码 key:批量取真 key(走 CriticalRateLimit)
  const kb = await api('POST', '/api/token/batch/keys', { ids: Object.keys(idOf).map(Number) });
  const raw = kb.data?.keys || {};
  const byName = {};
  for (const [id, name] of Object.entries(idOf)) {
    const k = raw[id];
    if (!k || k.includes('*')) throw new Error(`取真 key 失败: ${name}(${id})`);
    byName[name] = k;
  }
  log(`token 就绪: pool×${CLIENTS}(全部 concurrency_limit=3)`);
  return byName;
}

/** 写 4 个队列键并回读校验(同 integration-run)。 */
async function setQueue(q) {
  const want = {
    'concurrency.queue_enabled': String(!!q.enabled),
    'concurrency.queue_depth_ratio': String(q.ratio),
    'concurrency.queue_depth_cap': String(q.cap),
    'concurrency.queue_max_wait_seconds': String(q.maxWait),
  };
  for (const [k, v] of Object.entries(want)) await api('PUT', '/api/option/', { key: k, value: v });
  const all = await api('GET', '/api/option/');
  const got = Object.fromEntries((all.data || []).filter((o) => o.key.startsWith('concurrency.')).map((o) => [o.key, o.value]));
  const ok = Object.entries(want).every(([k, v]) => got[k] === v);
  log(`队列(${q.name}): ${JSON.stringify(want)} 回读 ${ok ? 'OK' : 'MISMATCH ' + JSON.stringify(got)}`);
  if (!ok) throw new Error('队列参数回读不一致');
}

async function listChannels() {
  const l = await api('GET', '/api/channel/?p=1&size=100');
  return l.data?.items || [];
}

/** 克隆模板渠道为 cap-XX(POST /api/channel/,mode=single)。失败补字段重试一次,仍失败抛错(由调用方降级)。 */
async function cloneChannel(template, name, base) {
  const ch = { ...template };
  // 详情对象里服务端生成/只读的字段剔除;status 走 batch 端点控制
  for (const k of ['id', 'status', 'created_time', 'test_time', 'response_time', 'balance', 'balance_updated_time', 'used_quota', 'channel_info', 'other_info']) delete ch[k];
  ch.key = CAP_KEY || CLONE_KEY_PLACEHOLDER; // 详情 Omit(key);mock 不校验,real 用真实集群 key
  ch.name = name;
  ch.base_url = base;
  ch.concurrency_limit = CHANNEL_CONCURRENCY;
  ch.weight = 100;
  ch.status = 1;
  const body = { mode: 'single', channel: ch };
  try {
    await api('POST', '/api/channel/', body);
    return;
  } catch (e) {
    log(`创建 ${name} 失败 body=${(e.body || '').slice(0, 400)},补字段重试一次`);
    // 常见必填缺失:key 非空(validateChannel isAdd)、base_url、models
    if (!body.channel.key) body.channel.key = CAP_KEY || CLONE_KEY_PLACEHOLDER;
    if (!body.channel.base_url) body.channel.base_url = base;
    if (!body.channel.models) body.channel.models = '*';
    await api('POST', '/api/channel/', body); // 仍失败直接抛,进入降级
  }
}

/**
 * 拓扑准备:克隆 cap-01..cap-maxN(已存在按名 PUT 保证同样字段)。
 * 返回 {capIds: [id...] 按 cap 序号有序, allIds: 现有全部渠道 id}。
 * 创建彻底失败时抛错,由 main 降级为现有渠道组合。
 */
async function ensureCapChannels(maxN) {
  const tplRes = await api('GET', `/api/channel/${TEMPLATE_ID}`);
  const template = tplRes.data;
  if (!template) throw new Error(`模板渠道 id=${TEMPLATE_ID} 不存在`);
  const base = CAP_BASE;
  let items = await listChannels();
  for (let i = 1; i <= maxN; i++) {
    const name = `cap-${p2(i)}`;
    let c = items.find((x) => x.name === name);
    if (!c) {
      log(`克隆渠道 ${name}(模板 id=${TEMPLATE_ID} ${template.name}, limit=${CHANNEL_CONCURRENCY})`);
      await cloneChannel(template, name, base);
      items = await listChannels();
      c = items.find((x) => x.name === name);
      if (!c) throw new Error(`渠道 ${name} 创建后列表里找不到`);
    } else {
      log(`渠道 ${name} 已存在(id=${c.id}),PUT 保证字段一致`);
    }
    // 统一 PUT 保证字段一致。关键副作用:UpdateChannel 成功后会调用 InitChannelCache ——
    // 容器配了 Redis(main.go: RedisEnabled → MemoryCacheEnabled 强制 true),新建渠道在
    // channelsIDM 缓存就绪前,batch status 改它是静默 no-op(data=0)、relay 也选不到它,
    // 必须靠一次管理写操作把它刷进内存缓存。key 为空被 GORM 跳过,凭据不被清空。
    const body = { ...c };
    delete body.status; // PUT /api/channel/ 明确拒绝带 status 的 body(MsgInvalidParams)
    body.base_url = base;
    body.concurrency_limit = CHANNEL_CONCURRENCY;
    body.weight = 100;
    if (CAP_MODE === 'real') body.key = CAP_KEY; // 占位 key 换成真实集群 key(root 敏感写放行)
    await api('PUT', '/api/channel/', body);
  }
  items = await listChannels();
  const capIds = [];
  for (let i = 1; i <= maxN; i++) {
    const c = items.find((x) => x.name === `cap-${p2(i)}`);
    if (!c) throw new Error(`渠道 cap-${p2(i)} 仍不在列表`);
    capIds.push(c.id);
  }
  const allIds = items.map((x) => x.id);
  log(`渠道克隆就绪: cap-01..cap-${maxN} -> ids=[${capIds}]`);
  return { capIds, allIds };
}

/** 当前档只激活 on 集,其余(含原 1..6 与多余 cap-XX)全部禁用。 */
async function activateChannels(on, allIds) {
  const off = allIds.filter((id) => !on.includes(id));
  if (off.length) await api('POST', '/api/channel/status/batch', { ids: off, status: 2 }); // 2=手动禁用
  await api('POST', '/api/channel/status/batch', { ids: on, status: 1 }); // 1=启用
  log(`渠道: 激活 [${on}] 禁用 [${off}]`);
}

/** 快照一次(scopes current 之和 / queued / rejections 进程起算单调计数)。 */
async function snapshot() {
  const s = await api('GET', '/api/concurrency/snapshot');
  return {
    inFlight: (s.scopes || []).reduce((a, x) => a + (x.current || 0), 0),
    queued: s.queued || 0,
    rejections: s.rejections || 0,
  };
}

/** 探针:1 个小请求必须 200,否则抛错中止。 */
async function probe() {
  const t0 = now();
  let res;
  try {
    res = await fetch(URL_CHAT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEYS['pool-key-01']}`, 'New-API-User': '1' },
      body: JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'Reply with exactly: OK' }], stream: false, max_tokens: 16 }),
      signal: AbortSignal.timeout(60000),
    });
  } catch (e) {
    throw new Error(`探针请求异常: ${e?.name || e}`);
  }
  const txt = await res.text().catch(() => '');
  if (res.status !== 200) {
    log(`探针失败 ${res.status}: ${txt.slice(0, 400)}`);
    throw new Error(`探针单发失败: HTTP ${res.status}`);
  }
  log(`探针 ok ${now() - t0}ms`);
}

// ---------------- C. 负载驱动(恒速阶梯,重试=0) ----------------
/** 单发一次请求,返回 {status, durMs, retryAfter}。永不抛错(网络错误记 status=0)。 */
async function fireOne() {
  const key = KEYS[`pool-key-${p2((fireOne.idx++ % CLIENTS) + 1)}`]; // 10 key 轮转,避免单 key limit=3 抢跑
  const body = JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'Reply with exactly: OK' }], stream: false, max_tokens: 32 });
  const t0 = now();
  let status = 0;
  let retryAfter = null;
  try {
    const res = await fetch(URL_CHAT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${key}`, 'New-API-User': '1' },
      body,
      signal: AbortSignal.timeout(60000),
    });
    status = res.status;
    retryAfter = res.headers.get('retry-after'); // 429 只有 status+Retry-After 头
    await res.text().catch(() => '');
  } catch {
    status = 0; // 网络错误/超时
  }
  return { status, durMs: now() - t0, retryAfter };
}
fireOne.idx = 0;

/**
 * 单档:目标 rps 恒速发 rungSeconds 秒(200ms tick × rps/5,分数误差累计补偿),
 * 全局在飞上限 inFlightCap(达到记 dropped_client);等在飞清零(≤30s)后对账落盘。
 */
async function runRung(N, targetRps, jsonl) {
  const st = { scheduled: 0, sent: 0, ok: 0, r429: 0, err: 0, dropped: 0, inflight: 0, lat: [] };
  const pending = new Set();
  const rejStart = (await snapshot()).rejections; // Δrejections 起点
  log(`N=${N} rps目标=${targetRps} 档开始(起点 rejections=${rejStart})`);
  const t0 = now();
  let credit = 0;
  const perTick = targetRps / (1000 / TICK_MS);
  let lastProgress = t0;
  while (now() - t0 < RUNG_MS) {
    await sleep(TICK_MS);
    credit += perTick;
    let n = Math.floor(credit);
    credit -= n;
    while (n-- > 0) {
      st.scheduled++;
      if (st.inflight >= IN_FLIGHT_CAP) {
        st.dropped++; // 客户端侧丢弃,不计入 429 口径
        continue;
      }
      st.sent++;
      st.inflight++;
      const p = fireOne()
        .then((rec) => {
          if (rec.status === 200) {
            st.ok++;
            st.lat.push(rec.durMs);
          } else if (rec.status === 429) st.r429++;
          else st.err++;
        })
        .finally(() => {
          st.inflight--;
          pending.delete(p);
        });
      pending.add(p);
    }
    if (now() - lastProgress >= 10000) {
      log(`N=${N} rps目标=${targetRps} 已发${st.sent} 成功${st.ok} 429=${st.r429} 在飞${st.inflight} (丢弃${st.dropped} err${st.err})`);
      lastProgress = now();
    }
  }
  // drain:等在飞清零,最多 30s,避免污染下一档
  const drainStart = now();
  while (st.inflight > 0 && now() - drainStart < DRAIN_MAX_MS) await sleep(200);
  const unsettled = st.inflight; // drain 超时仍未回的(实际 10s maxWait+0.8s 时延下不应出现)
  if (unsettled > 0) log(`[warn] N=${N} rps目标=${targetRps} drain 超时,${unsettled} 个请求仍未回,计入 unsettled`);
  const rejEnd = (await snapshot()).rejections;
  const wallMs = now() - t0;
  const dRej = rejEnd - rejStart;
  const lat = st.lat.slice().sort((a, b) => a - b);
  const completionRate = st.sent ? st.ok / st.sent : 0;
  const r429Rate = st.sent ? st.r429 / st.sent : 0;
  const achieved = st.ok / (RUNG_MS / 1000);
  const pass = completionRate >= PASS_COMPLETION && r429Rate <= PASS_429_RATE;
  const rec = {
    t: t0,
    clock: clock(t0),
    N,
    targetRps,
    scheduled: st.scheduled,
    sent: st.sent,
    ok: st.ok,
    r429: st.r429,
    err: st.err,
    dropped_client: st.dropped,
    unsettled,
    achievedRps: achieved,
    p50: quantile(lat, 0.5),
    p95: quantile(lat, 0.95),
    completionRate,
    r429Rate,
    deltaRejections: dRej,
    reconcile: st.r429 === dRej,
    wallMs,
    pass,
  };
  fs.appendFileSync(jsonl, JSON.stringify(rec) + '\n');
  log(
    `N=${N} rps目标=${targetRps} 完成: sent=${st.sent} ok=${st.ok} 429=${st.r429} err=${st.err} drop=${st.dropped} ` +
      `达成=${rps1(achieved)}rps p50=${ms(rec.p50)} p95=${ms(rec.p95)} 完成率=${pct(completionRate)} 429率=${pct(r429Rate)} Δrej=${dRej} → ${pass ? 'PASS' : 'FAIL'}`,
  );
  if (!rec.reconcile) log(`[warn] 429 对账不相等: 客户端 r429=${st.r429} vs Δrejections=${dRej}`);
  return rec;
}

// ---------------- D. 每 N 汇总与总表 ----------------
function theoryFor(N) {
  const delayS = Math.max(0.001, CFG.upstream.fakeDelayMs / 1000);
  const keySide = (CLIENTS * 3) / delayS; // 10 pool key × limit 3 / 时延
  const channelSide = (N * CHANNEL_CONCURRENCY) / delayS; // 3N / 时延
  return { delayS, keySide, channelSide, min: Math.min(keySide, channelSide) };
}

function summarizeN(N, rungs, dirName, degraded) {
  const th = theoryFor(N);
  let boundary = null;
  for (const r of rungs) if (r.pass) boundary = r.targetRps; // 边界 = 最后一个 PASS 的目标 rps
  const passRungs = rungs.filter((r) => r.pass);
  const ceiling = passRungs.length ? Math.max(...passRungs.map((r) => r.achievedRps)) : null;
  const m = {
    N,
    dir: dirName,
    degraded,
    queue: CFG.queueSweep.find((x) => x.name === CAP.queue) || CFG.queueSweep[0],
    rungs,
    boundaryRps: boundary,
    ceilingAchievedRps: ceiling,
    theory: th,
    theoryNote: `理论天花板≈min(key侧${rps1(th.keySide)}, 渠道侧${rps1(th.channelSide)})=${rps1(th.min)} rps(${LAT_TXT})`,
    verdict: boundary != null ? `边界 ${boundary} rps(其后档位 FAIL)` : '全部档位 FAIL(无 PASS 档)',
    dockerImage: CFG.target.dockerContainer,
    config: JSON.parse(JSON.stringify(CFG)),
  };
  const dir = path.join(RESULTS, dirName);
  fs.writeFileSync(path.join(dir, 'summary.json'), JSON.stringify(m, null, 2));
  const L = [];
  L.push(`# 容量边界 — N=${N} 渠道`);
  L.push('');
  if (degraded) L.push(`> ⚠ ${degraded}`);
  L.push(`- 队列: ${m.queue.name}(enabled=${m.queue.enabled} ratio=${m.queue.ratio} cap=${m.queue.cap} maxWait=${m.queue.maxWait}s)`);
  L.push(`- 每渠道 concurrency_limit=${CHANNEL_CONCURRENCY};理论天花板: ${m.theoryNote}`);
  L.push('');
  L.push('## N×rps 矩阵');
  L.push('');
  L.push('| 目标rps | sent | ok | 429 | err | drop | 达成rps | 完成率 | 429率 | p50 | p95 | Δrej | 判定 |');
  L.push('| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |');
  for (const r of rungs) {
    L.push(`| ${r.targetRps} | ${r.sent} | ${r.ok} | ${r.r429} | ${r.err} | ${r.dropped_client} | ${rps1(r.achievedRps)} | ${pct(r.completionRate)} | ${pct(r.r429Rate)} | ${ms(r.p50)} | ${ms(r.p95)} | ${r.deltaRejections} | ${r.pass ? 'PASS' : 'FAIL'} |`);
  }
  L.push('');
  L.push('## 边界与天花板');
  L.push('');
  L.push(`- 边界 rps(最后 PASS 档): **${boundary ?? 'N/A'}**`);
  L.push(`- 实测天花板达成 rps(PASS 档 max): **${rps1(ceiling)}**`);
  L.push(`- 理论: ${m.theoryNote} → 实测/理论 = ${ceiling != null ? rps1(ceiling / th.min) : 'N/A'}×`);
  L.push(`- 判定: ${m.verdict}`);
  L.push('');
  L.push('## config 快照(执行时深拷贝)');
  L.push('');
  L.push('```json');
  L.push(JSON.stringify(m.config, null, 2));
  L.push('```');
  fs.writeFileSync(path.join(dir, 'summary.md'), L.join('\n') + '\n');
  log(`N=${N} 汇总已写: ${dir}`);
  return m;
}

function writeFinalReport(summaries, runStamp, degraded) {
  const L = [`# 容量边界汇总 RUN-${runStamp}`, '', `目标: ${BASE}(${CFG.target.dockerContainer})  上游: ${CAP_MODE === 'real' ? CFG.upstream.realBaseUrl + ' (real, ' + MODEL + ')' : MOCK + ' (' + CFG.upstream.fakeDelayMs + 'ms±' + CFG.upstream.fakeJitterMs + 'ms)'}`, ''];
  if (degraded) L.push(`> ⚠ ${degraded}`);
  L.push('| N | 边界 rps | 天花板达成 rps | 理论 | p95@边界 |');
  L.push('| --- | --- | --- | --- | --- |');
  for (const m of summaries) {
    const bRung = m.boundaryRps != null ? m.rungs.find((r) => r.targetRps === m.boundaryRps) : null;
    L.push(`| ${m.N} | ${m.boundaryRps ?? 'N/A'} | ${rps1(m.ceilingAchievedRps)} | ${rps1(m.theory.min)}(${rps1(m.theory.keySide)}/${rps1(m.theory.channelSide)}) | ${ms(bRung?.p95)} |`);
  }
  L.push('');
  L.push(`理论 = min(key 侧 ${CLIENTS}×3/时延, 渠道侧 3N/时延);${LAT_TXT};每档明细见各 N 目录 rungs.jsonl 与 summary.md。`);
  const file = path.join(RESULTS, `CAPACITY-${runStamp}.md`);
  fs.writeFileSync(file, L.join('\n') + '\n');
  console.log('\n' + L.join('\n'));
  log(`容量汇总已写: ${file}`);
}

// ---------------- main ----------------
async function main() {
  let counts = selectCounts(process.argv.slice(2));
  if (!counts.length) fail(`没有匹配的 N(参数: ${process.argv.slice(2).join(' ') || '默认'};可选: ${CAP.channelCounts.join(',')})`);
  log(`执行渠道数 N: ${counts.join(' → ')}`);
  await ready();
  const queue = CFG.queueSweep.find((x) => x.name === CAP.queue) || CFG.queueSweep[0];
  await setQueue(queue);
  KEYS = await ensureKeys();
  fs.mkdirSync(RESULTS, { recursive: true });
  const runStamp = stamp();

  // A. 拓扑:克隆 cap-01..cap-maxN;失败降级为现有渠道组合 N∈{1,3,6}
  let degraded = null;
  let capIds = [];
  let allIds = [];
  let activeFor; // (N) => [id...]
  const maxN = Math.max(...counts);
  try {
    ({ capIds, allIds } = await ensureCapChannels(maxN));
    activeFor = (N) => capIds.slice(0, N);
  } catch (e) {
    log(`渠道创建失败,降级为现有渠道: ${e?.message || e}`);
    degraded = `渠道创建失败,降级为现有渠道(原因: ${String(e?.message || e).slice(0, 200)})`;
    counts = [1, 3, 6];
    let items = await listChannels();
    allIds = items.map((x) => x.id);
    const sets = { 1: [3], 3: [3, 4, 5], 6: [1, 2, 3, 4, 5, 6] };
    const used = [...new Set(Object.values(sets).flat())];
    // 降级集也要满足容量前提:每渠道 limit=3 + 指向 mock(主矩阵跑完后它们是 limit=0)
    for (const id of used) {
      const c = items.find((x) => x.id === id);
      if (!c) fail(`降级失败: 现有渠道 id=${id}(降级集需要)不存在`);
      const body = { ...c };
      delete body.status; // PUT 拒绝带 status 的 body
      body.base_url = CAP_BASE;
      body.concurrency_limit = CHANNEL_CONCURRENCY;
      body.weight = 100;
      if (CAP_MODE === 'real') body.key = CAP_KEY;
      await api('PUT', '/api/channel/', body);
    }
    activeFor = (N) => sets[N];
    log(`降级激活集: ${JSON.stringify(sets)}(已统一 limit=${CHANNEL_CONCURRENCY})`);
  }

  // B/C. 每 N × 每 rps 阶梯
  const summaries = [];
  let broken = null;
  for (const N of counts) {
    const dirName = `capacity-N${N}-${runStamp}`;
    const dir = path.join(RESULTS, dirName);
    fs.mkdirSync(dir, { recursive: true });
    const jsonl = path.join(dir, 'rungs.jsonl');
    fs.writeFileSync(jsonl, '');
    log(`=== N=${N} 开始 dir=${dirName} 队列=${queue.name} 目标档=${CAP.offeredRps.join(',')} rps 每档 ${CAP.rungSeconds}s`);
    try {
      await activateChannels(activeFor(N), allIds);
      await probe();
      const rungs = [];
      for (const targetRps of CAP.offeredRps) {
        rungs.push(await runRung(N, targetRps, jsonl));
        await sleep(1000); // 档间歇
      }
      summaries.push(summarizeN(N, rungs, dirName, degraded));
    } catch (e) {
      broken = e;
      log(`N=${N} 失败,停止后续(已写数据保留): ${e?.message || e}`);
      break;
    }
  }
  if (summaries.length) writeFinalReport(summaries, runStamp, degraded);
  else log('没有任何 N 完成,不写容量汇总');
  if (broken) process.exitCode = 1;
  await sleep(300); // 等 stdout 落盘(Windows 管道异步)
  process.exit(process.exitCode || 0);
}

main().catch((e) => {
  console.error('Fatal:', e);
  process.exit(1);
});
