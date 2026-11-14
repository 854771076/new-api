/**
 * integration-run.mjs — 主矩阵执行器(并发上限 × 有界等待队列 × 429 降级)。
 *
 * 用法: node integration-run.mjs [轮次名...]   例:无参 = M1 M2 M3 M4 M5(M6 仅 realBudgetTokens>0)
 *      轮次名前缀匹配:M1 / M3 / M5 / M5-tiny(队列档)。
 *
 * 约定:
 * - 一切拓扑变更走 HTTP API(禁止 node:sqlite);数据落 results/<轮>-<YYYYMMDD-HHmmss>/。
 * - 指标口径见 TEST-PLAN.md §3;落盘见 §6;编排见 §5。
 * - 打流模型固定 cfg.upstream.realModel;非流(stream:false)。
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const DIR = path.dirname(fileURLToPath(import.meta.url));
const CFG = JSON.parse(fs.readFileSync(path.join(DIR, 'config.json'), 'utf8'));
const BASE = CFG.target.baseUrl;
const MOCK = `http://127.0.0.1:${CFG.upstream.mockPort}`;
const RESULTS = path.join(DIR, 'results');
const MODEL = CFG.upstream.realModel;
const URL_CHAT = `${BASE}/v1/chat/completions`;
const CLIENTS = CFG.load.clients;
const PER = CFG.load.tasksPerClient;
const MAX_TOKENS_BY_TIER = [150, 200, 300, 400]; // 四档 short/medium/long/ultra
const DRAIN_BUDGET_MS = Math.round((CFG.budget.maxWallHours || 6) * 3600000) / 3; // 单轮 drain 上限 = 全矩阵 1/3

let ADMIN = ''; // admin bearer,ready() 里登录获取

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

/** 重新登录(会话约 15 分钟过期,矩阵跑一半必撞 401;401 时由 api() 自动调用)。 */
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

// ---------------- 轮次展开 ----------------
/** config.rounds → 可执行单元(M5 按 queueSweep 展开成 M5-noqueue…)。 */
function expandUnits() {
  const out = [];
  for (const [key, r] of Object.entries(CFG.rounds)) {
    const base = {
      channels: r.channels,
      keys: r.keys,
      mode: r.mode || CFG.upstream.mode || 'fake',
      // 渠道拓扑扩展(M7 priority 退档用):每 channel 的 priority / limit / weight
      // 不传时退化为原有行为(priority=0, limit=CFG.limits.channelLimit, weight=100)
      priorities: r.priorities || null,
      channelLimit: r.channelLimit != null ? r.channelLimit : CFG.limits.channelLimit,
      channelWeight: r.channelWeight != null ? r.channelWeight : 100,
      // M7: per-round 覆盖 token concurrency_limit(0 = 关闭 key 闸,让 channel 闸为唯一瓶颈)
      // 不传时 ensureKeys 用默认 3
      keyLimit: r.keyLimit != null ? r.keyLimit : null,
      // M7: per-round 覆盖 gateway RetryTimes(common.RetryTimes)。默认 0 让 channel error
      // 立即终结,不进 retry 退档路径。设 3 让 channel 闸 429 触发 Fix A 退档。
      // 不传时为 undefined,runRound 入口不动 RetryTimes。
      retryTimes: r.retryTimes,
      default: true,
    };
    if (r.sweep) {
      for (const q of CFG.queueSweep) out.push({ ...base, key: `M5-${q.name}`, queue: q, sweepOf: key });
    } else {
      const q = CFG.queueSweep.find((x) => x.name === r.queue) || CFG.queueSweep[0];
      out.push({ ...base, key, queue: q });
    }
  }
  return out;
}

function selectUnits(args) {
  const units = expandUnits();
  const picked = args.length
    ? units.filter((u) => args.some((a) => u.key.startsWith(a) || (u.sweepOf || '').startsWith(a)))
    : units.filter((u) => u.default);
  // real 轮受 token 预算闸:realBudgetTokens<=0 一律不进执行清单(main 会打印跳过原因)
  return picked.filter((u) => u.mode !== 'real' || CFG.upstream.realBudgetTokens > 0);
}

// ---------------- A. 就绪检查 ----------------
async function ready(sel) {
  for (const t of CFG._todo || []) log(`[todo] ${t}`); // 警告,不阻断
  const host = new URL(BASE).hostname;
  if (sel.some((u) => u.mode !== 'real') && !['127.0.0.1', 'localhost'].includes(host)) {
    fail(`target.baseUrl 主机 ${host} 不在白名单(仅 127.0.0.1/localhost),拒绝继续`);
  }
  let st;
  try {
    st = await (await fetch(`${BASE}/api/status`, { signal: AbortSignal.timeout(15000) })).json();
  } catch (e) {
    fail(`网关 /api/status 不可达: ${e?.name || e}`);
  }
  if (!st?.success) fail(`网关 /api/status 返回失败: ${JSON.stringify(st).slice(0, 200)}`);
  log(`网关就绪 ${BASE} version=${st.data?.version || '?'} docker=${CFG.target.dockerContainer}`);
  if (sel.some((u) => u.mode !== 'real')) {
    let h;
    try {
      h = await fetch(`${MOCK}/health`, { signal: AbortSignal.timeout(5000) });
    } catch (e) {
      fail(`mock 上游不可达(${MOCK}/health): ${e?.name || e} — 先起 mock-upstream.mjs`);
    }
    if (!h.ok) fail(`mock 上游 /health -> ${h.status}`);
    log(`mock 上游就绪 ${MOCK}`);
  }
  await ensureLogin();
  log(`admin 登录成功 (${CFG.target.adminUser})`);
  if ((CFG.production?.nodeCount || 1) > 1) log(`提示: production.nodeCount=${CFG.production.nodeCount},本执行器只跑单节点轮`);
}

// ---------------- B. 拓扑助手(全部 HTTP API) ----------------
/** 一次性建/修 10 个 pool key + 1 个共享 key,并按 id 取回未掩码真 key。unit.keyLimit 覆盖默认 3(M7 关闭 key 闸专测 channel 闸)。 */
async function ensureKeys(unit) {
  const list = await api('GET', '/api/token/?p=1&size=100');
  const items = list.data?.items || [];
  const want = [];
  for (let i = 1; i <= CLIENTS; i++) want.push(`pool-key-${p2(i)}`);
  want.push('concurrency-test');
  // unit.keyLimit 不传时默认 3;M7 传 0 关闭 key 闸
  const keyLimit = unit && unit.keyLimit != null ? unit.keyLimit : 3;
  const idOf = {};
  for (const name of want) {
    let t = items.find((x) => x.name === name);
    if (!t) {
      log(`创建 token ${name}(concurrency_limit=${keyLimit})`);
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
        concurrency_limit: keyLimit,
        concurrency_limit_by_model: '',
      });
      const after = await api('GET', '/api/token/?p=1&size=100');
      t = (after.data?.items || []).find((x) => x.name === name);
      if (!t) throw new Error(`token ${name} 创建后列表里找不到`);
    } else if (t.concurrency_limit !== keyLimit) {
      log(`修正 token ${name} concurrency_limit ${t.concurrency_limit} -> ${keyLimit}(PUT 原对象+id)`);
      await api('PUT', '/api/token/', { ...t, concurrency_limit: keyLimit });
    }
    idOf[t.id] = name;
  }
  // 列表/详情都掩码 key:批量取真 key(走 CriticalRateLimit,整场只调一次)
  const kb = await api('POST', '/api/token/batch/keys', { ids: Object.keys(idOf).map(Number) });
  const raw = kb.data?.keys || {};
  const byName = {};
  for (const [id, name] of Object.entries(idOf)) {
    const k = raw[id];
    if (!k || k.includes('*')) throw new Error(`取真 key 失败: ${name}(${id})`);
    byName[name] = k;
  }
  log(`token 就绪: pool×${CLIENTS} + 共享×1(全部 concurrency_limit=${keyLimit})`);
  return byName;
}

/** 每轮布置 6 个渠道:PUT 完整对象改 base_url/concurrency_limit/weight,状态走 status 端点。
 * M7 priority 退档扩展:unit.priorities 数组(默认 null)按 [id=3,4,5] 顺序应用
 * 到 channel 3/4/5 的 priority 字段;unit.channelLimit / channelWeight 单独覆盖。
 */
async function ensureChannels(unit) {
  const list = await api('GET', '/api/channel/?p=1&size=100');
  const items = list.data?.items || [];
  const on = unit.channels === 1 ? [3] : [3, 4, 5];
  const off = [1, 2, 3, 4, 5, 6].filter((id) => !on.includes(id));
  const base = unit.mode === 'real' ? CFG.upstream.realBaseUrl : `http://host.docker.internal:${CFG.upstream.mockPort}`;
  const limit = unit.channelLimit; // 0 = 不限;M7 显式传 3 触发 saturation
  const weight = unit.channelWeight;
  // unit.priorities 为 null 时不设 priority(保留 DB 已有值),数组时按 [id=3,4,5] 应用
  const priorityByIndex = (idx) => (Array.isArray(unit.priorities) ? unit.priorities[idx] : undefined);
  for (const id of [1, 2, 3, 4, 5, 6]) {
    const c = items.find((x) => x.id === id);
    if (!c) throw new Error(`渠道 id=${id} 不存在(期望 6 个渠道)`);
    const body = { ...c };
    delete body.status; // PUT /api/channel/ 明确拒绝带 status 的 body(MsgInvalidParams)
    body.base_url = base;
    body.concurrency_limit = limit;
    body.weight = weight;
    if (Array.isArray(unit.priorities)) {
      const idx = on.indexOf(id);
      if (idx >= 0) {
        // priority 字段是 *int64,API 接受数字(后端 handler 转换)
        body.priority = priorityByIndex(idx);
      }
    }
    try {
      await api('PUT', '/api/channel/', body); // body = 列表完整对象 + 改动项;key 为空被 GORM 跳过,凭据不被清空
    } catch (e) {
      log(`渠道 ${id} PUT 失败 body=${(e.body || '').slice(0, 400)}`);
      throw e;
    }
  }
  await api('POST', '/api/channel/status/batch', { ids: on, status: 1 }); // 1=启用
  await api('POST', '/api/channel/status/batch', { ids: off, status: 2 }); // 2=手动禁用(API 只接受 1/2,等价于策划里的"置 0")
  const prioDesc = Array.isArray(unit.priorities) ? `priorities=[${unit.priorities.join(',')}]` : 'priority=DB-keep';
  log(`渠道: 激活 [${on}] 禁用 [${off}] base=${base} limit=${limit} weight=${weight} ${prioDesc}`);
  return base;
}

/** 写 4 个队列键并回读校验。 */
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

/** 写 RetryTimes 选项并回读校验。M7 设为 3,让 channel error 触发 retry 退档。 */
async function setRetryTimes(retryTimes) {
  if (retryTimes == null) return; // undefined / null: 不动
  const want = { 'RetryTimes': String(retryTimes) };
  await api('PUT', '/api/option/', { key: 'RetryTimes', value: String(retryTimes) });
  const all = await api('GET', '/api/option/');
  const got = (all.data || []).find((o) => o.key === 'RetryTimes');
  const ok = got && got.value === String(retryTimes);
  log(`RetryTimes=${retryTimes} 回读 ${ok ? 'OK' : 'MISMATCH ' + JSON.stringify(got)}`);
  if (!ok) throw new Error('RetryTimes 回读不一致');
}

// ---------------- 任务生成(固定种子,确定性) ----------------
const WORD_POOL = 'alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee zulu amber beryl coral denim ember fjord'.split(' ');
const FILL = new Map();
function fillText(words) {
  if (!FILL.has(words)) {
    const a = new Array(words);
    for (let i = 0; i < words; i++) a[i] = WORD_POOL[i % WORD_POOL.length];
    FILL.set(words, a.join(' ')); // 确定性填充文本
  }
  return FILL.get(words);
}

function lcgShuffle(arr, seed = 42) {
  let s = seed >>> 0;
  const rnd = () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 4294967296;
  };
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(rnd() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

/** 每 client 一列任务(四档长度 × 固定种子洗牌),再全局交错成打流序列。 */
function genTasks() {
  const fragileN = CFG.load.clientProfiles.fragile;
  const byClient = [];
  let per = 0;
  for (let c = 0; c < CLIENTS; c++) {
    const raw = [];
    CFG.load.lengthTiers.forEach((tier, i) => {
      for (let k = 0; k < tier.count; k++) raw.push({ tier: tier.name, words: tier.words, maxTokens: MAX_TOKENS_BY_TIER[i] ?? 200 });
    });
    per = raw.length;
    lcgShuffle(raw, 42);
    raw.forEach((t, i) => {
      t.client = c;
      t.task = i;
      t.profile = c < fragileN ? 'fragile' : 'robust';
    });
    byClient.push(raw);
  }
  if (per !== PER) log(`[warn] lengthTiers 合计 ${per} ≠ tasksPerClient ${PER},以 lengthTiers 为准`);
  const all = [];
  for (let i = 0; i < per; i++) for (let c = 0; c < CLIENTS; c++) all.push(byClient[c][i]); // 交错,burst 覆盖每个 client
  return all;
}

// ---------------- 客户端工作池 ----------------
class Fifo {
  constructor() {
    this.q = [];
    this.waiters = [];
    this.closed = false;
  }
  push(x) {
    const w = this.waiters.shift();
    if (w) w(x);
    else this.q.push(x);
  }
  async pop() {
    if (this.q.length) return this.q.shift();
    if (this.closed) return null;
    return new Promise((r) => this.waiters.push(r));
  }
  close() {
    this.closed = true;
    while (this.waiters.length) this.waiters.shift()(null);
  }
}

/** 单任务:首次 429 / 非 429 失败的重试策略见 TEST-PLAN 与 D 节约定。 */
async function runTask(t, ctx) {
  const key = ctx.keyFor(t.client);
  const body = JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: fillText(t.words) }], stream: false, max_tokens: t.maxTokens });
  const headers = { 'Content-Type': 'application/json', Authorization: `Bearer ${key}`, 'New-API-User': '1' };
  const sendTs = now();
  let attempts = 0;
  let firstStatus = 0;
  let term = null; // {status, doneTs, firstByteTs|null}
  for (;;) {
    attempts++;
    const attStart = now();
    let status = 0;
    let retryAfter = null;
    let firstByteTs = null;
    try {
      const res = await fetch(ctx.url, { method: 'POST', headers, body, signal: AbortSignal.timeout(ctx.attemptTimeout) });
      firstByteTs = now(); // 响应头到达 = 首字节
      status = res.status;
      retryAfter = res.headers.get('retry-after');
      await res.text().catch(() => '');
    } catch (e) {
      status = 0; // 网络错误/超时
    }
    if (attempts === 1) firstStatus = status;
    if (status === 429) ctx.count429++; // 每收到一次 429 就 +1(对账口径:middleware 每拒一次 rejections+1)
    term = { status, doneTs: now(), firstByteTs, attStart };
    if (status === 200) break;
    if (status === 429) {
      if (t.profile === 'fragile' || attempts >= 6) break; // fragile 首击即终态;robust 最多 6 次尝试
      const raSec = Number(retryAfter);
      const expMs = Math.min(1000 * 2 ** attempts, 30000); // 指数退避,30s 封顶
      const wait = Math.min(Math.max(expMs, (Number.isFinite(raSec) ? raSec : 1) * 1000), 30000) + Math.floor(Math.random() * 500); // Retry-After(缺省1s)+ 0-500ms 抖动
      await sleep(wait);
      continue;
    }
    if (t.profile === 'fragile' || attempts >= 4) break; // robust 最多重试 3 次(共 4 次尝试)
    await sleep(1000 * attempts); // 间隔 1s 递增
  }
  const rec = {
    client: t.client,
    profile: t.profile,
    task: t.task,
    tier: t.tier,
    words: t.words,
    sendTs,
    doneTs: term.doneTs,
    status: term.status,
    attempts,
    firstStatus, // 客户端侧扩展字段:算"首发即 429 率"必需
    firstByteMs: term.firstByteTs ? term.firstByteTs - term.attStart : null, // 终态那次尝试的首字节
    keyMode: ctx.keyMode,
  };
  ctx.records.push(rec);
  fs.appendFileSync(ctx.jsonl, JSON.stringify(rec) + '\n');
}

async function worker(fifo, ctx) {
  for (;;) {
    const t = await fifo.pop();
    if (!t) return;
    try {
      await runTask(t, ctx);
    } catch (e) {
      log(`client=${t?.client} task=${t?.task} 执行异常(跳过): ${e?.message || e}`); // 单任务异常不拖垮工作池
    }
  }
}

// ---------------- C. 快照轮询 ----------------
async function sampleOnce(st) {
  try {
    const s = await api('GET', '/api/concurrency/snapshot');
    const rec = {
      t: now(),
      inFlight: (s.scopes || []).reduce((a, x) => a + (x.current || 0), 0), // scopes current 之和
      queued: s.queued || 0,
      rejections: s.rejections || 0, // 进程起算单调计数
      degraded: !!(s.degraded && s.degraded.active),
    };
    fs.appendFileSync(path.join(st.dir, 'snapshot.jsonl'), JSON.stringify(rec) + '\n');
    st.samples.push(rec);
    if (rec.queued > st.maxQueued) st.maxQueued = rec.queued;
    st.sumQueued += rec.queued;
  } catch (e) {
    log(`快照采样失败(忽略): ${e?.message || e}`);
  }
}

async function pollLoop(st) {
  while (!st.stop) {
    await sleep(CFG.budget.snapshotPollMs);
    if (st.stop) break;
    await sampleOnce(st);
  }
}

// ---------------- D. 负载驱动 ----------------
/** 阶段 A 突发 + 阶梯恒速;只决定"何时把任务交给客户端"。 */
async function dispatch(all, fifos, unit) {
  const burst = Math.round(CFG.load.burstFrac * all.length);
  for (let i = 0; i < burst; i++) fifos[all[i].client].push(all[i]);
  let i = burst;
  log(`${unit.key} 打流 ${i}/${all.length} dispatched (burst=${burst} @t=0)`);
  for (const rps of CFG.load.staircase.offeredRps) {
    if (i >= all.length) break;
    const rungEnd = now() + CFG.load.staircase.rungSeconds * 1000;
    const gap = 1000 / rps;
    let next = now();
    log(`${unit.key} 档位 ${rps} rps 开始(已发 ${i}/${all.length})`);
    while (i < all.length && now() < rungEnd) {
      const wait = next - now();
      if (wait > 0) await sleep(wait);
      fifos[all[i].client].push(all[i]);
      i++;
      next += gap;
      if (i % 25 === 0) log(`${unit.key} 打流 ${i}/${all.length} dispatched`);
    }
    if (i >= all.length) break; // 档内发完即止
  }
  if (i < all.length) {
    log(`[warn] 阶梯时间墙用尽,剩余 ${all.length - i} 个任务立即下发`);
    for (; i < all.length; i++) fifos[all[i].client].push(all[i]);
  }
  log(`${unit.key} 打流 ${all.length}/${all.length} dispatched`);
}

async function withTimeout(promise, msBudget, onTimeout) {
  let timer;
  const guard = new Promise((r) => {
    timer = setTimeout(r, msBudget);
  });
  const done = await Promise.race([promise.then(() => true), guard.then(() => false)]);
  clearTimeout(timer);
  if (!done) onTimeout();
  return done;
}

// ---------------- E. 指标 ----------------
function loadBaselineP95() {
  if (!fs.existsSync(RESULTS)) return null;
  let best = null;
  for (const d of fs.readdirSync(RESULTS)) {
    if (!d.startsWith('M1')) continue;
    const f = path.join(RESULTS, d, 'summary.json');
    if (!fs.existsSync(f)) continue;
    try {
      const j = JSON.parse(fs.readFileSync(f, 'utf8'));
      if (typeof j.p95 === 'number' && (!best || (j.startTs || 0) > (best.startTs || 0))) best = j;
    } catch {
      /* 损坏的旧汇总忽略 */
    }
  }
  return best ? best.p95 : null;
}

function metrics(recs, st, unit, total, t0, dir, base) {
  const ok = recs.filter((r) => r.status === 200);
  const durs = ok.map((r) => r.doneTs - r.sendTs).sort((a, b) => a - b);
  const first429 = recs.filter((r) => r.firstStatus === 429).length;
  const finalFail = recs.filter((r) => r.status !== 200).length;
  const hist = {};
  for (const r of recs) hist[r.attempts] = (hist[r.attempts] || 0) + 1;
  const perClient = [];
  for (let c = 0; c < CLIENTS; c++) {
    const rs = recs.filter((r) => r.client === c);
    const oks = rs.filter((r) => r.status === 200).map((r) => r.doneTs - r.sendTs).sort((a, b) => a - b);
    perClient.push({ client: c, profile: rs[0]?.profile || '?', done: rs.length, rate: rs.length ? oks.length / rs.length : 0, p95: quantile(oks, 0.95) });
  }
  const perMinute = {};
  for (const r of recs) {
    const m = Math.floor((r.doneTs - t0) / 60000);
    perMinute[m] = (perMinute[m] || 0) + 1;
  }
  const start = st.samples[0];
  const end = st.samples[st.samples.length - 1];
  const completionRate = total ? ok.length / total : 0;
  const p50 = quantile(durs, 0.5);
  const p95 = quantile(durs, 0.95);
  const dRej = (end?.rejections || 0) - (start?.rejections || 0);
  // 合格线:完成率 ≥ 阈值 且 p95 ≤ K×基线(或绝对阈值);无基线只判完成率
  let threshold = null;
  let src = 'N/A(无基线)';
  if (CFG.pass.absoluteP95Ms != null) {
    threshold = CFG.pass.absoluteP95Ms;
    src = `absoluteP95Ms=${threshold}ms`;
  } else {
    const b = loadBaselineP95();
    if (b != null) {
      threshold = CFG.pass.kVsBaseline * b;
      src = `${CFG.pass.kVsBaseline}×baseline(${Math.round(b)}ms)=${Math.round(threshold)}ms`;
    }
  }
  const ratePass = completionRate >= CFG.pass.completionRate;
  const p95Pass = threshold == null ? null : p95 != null && p95 <= threshold;
  return {
    round: unit.key,
    dir,
    base,
    startTs: start?.t || t0,
    endTs: end?.t || now(),
    startStr: clock(start?.t || t0),
    endStr: clock(end?.t || now()),
    wallSeconds: Math.round(((end?.t || now()) - t0) / 1000),
    total,
    completed200: ok.length,
    completionRate,
    p50,
    p95,
    first429Rate: total ? first429 / total : 0,
    finalFailRate: total ? finalFail / total : 0,
    attemptsHistogram: hist,
    client429Total: st.count429,
    deltaRejections: dRej,
    reconcile: st.count429 === dRej,
    maxQueued: st.maxQueued,
    meanQueued: st.samples.length ? st.sumQueued / st.samples.length : 0,
    queuedEnd: end?.queued ?? null,
    inFlightEnd: end?.inFlight ?? null,
    degradedObserved: st.samples.some((s) => s.degraded),
    sampleCount: st.samples.length,
    perClient,
    perMinute: Object.entries(perMinute).map(([m, n]) => ({ minute: Number(m), done: n })),
    pass: { verdict: ratePass && (p95Pass === null || p95Pass), ratePass, p95Pass, thresholdMs: threshold, source: src },
    dockerImage: CFG.target.dockerContainer,
    queue: unit.queue,
    channels: unit.channels === 1 ? [3] : [3, 4, 5],
    keys: unit.keys,
    mode: unit.mode,
    config: JSON.parse(JSON.stringify(CFG)), // 整个 config 深拷贝
  };
}

// ---------------- 落盘 ----------------
function writeSummary(dir, m) {
  fs.writeFileSync(path.join(dir, 'summary.json'), JSON.stringify(m, null, 2));
  const L = [];
  L.push(`# 轮次汇总 — ${m.round}`);
  L.push('');
  L.push(`- 起止: ${m.startStr} → ${m.endStr}(墙钟 ${m.wallSeconds}s,采样 ${m.sampleCount} 次)`);
  L.push(`- 镜像 tag: \`${m.dockerImage}\``);
  L.push(`- 拓扑: channels=[${m.channels}] keys=${m.keys} mode=${m.mode} base_url=${m.base}`);
  L.push(`- 队列: ${m.queue.name}(enabled=${m.queue.enabled} ratio=${m.queue.ratio} cap=${m.queue.cap} maxWait=${m.queue.maxWait}s)`);
  L.push('');
  L.push('## 指标(口径见 TEST-PLAN.md §3)');
  L.push('');
  L.push('| 指标 | 值 |');
  L.push('| --- | --- |');
  L.push(`| 任务总数 | ${m.total} |`);
  L.push(`| 完成率(终态200) | ${pct(m.completionRate)}(${m.completed200}/${m.total}) |`);
  L.push(`| p50 / p95(端到端) | ${ms(m.p50)} / ${ms(m.p95)} |`);
  L.push(`| 首击 429 率 | ${pct(m.first429Rate)} |`);
  L.push(`| 终失败率 | ${pct(m.finalFailRate)} |`);
  L.push(`| attempts 直方图 | ${JSON.stringify(m.attemptsHistogram)} |`);
  L.push(`| Σclient429 / Δrejections | ${m.client429Total} / ${m.deltaRejections}(相等=${m.reconcile ? '是' : '否'}) |`);
  L.push(`| maxQueued / meanQueued | ${m.maxQueued} / ${m.meanQueued.toFixed(2)} |`);
  L.push(`| 轮终 queued / inFlight | ${m.queuedEnd} / ${m.inFlightEnd}(无泄漏要求双 0) |`);
  L.push(`| degraded 观测 | ${m.degradedObserved ? '是' : '否'} |`);
  L.push('');
  L.push('## 合格判定');
  L.push('');
  L.push(`- 完成率 ≥ ${pct(CFG.pass.completionRate)}:${m.pass.ratePass ? 'PASS' : 'FAIL'}`);
  L.push(`- p95 阈值(${m.pass.source}):${m.pass.p95Pass == null ? 'N/A(只判完成率)' : m.pass.p95Pass ? 'PASS' : 'FAIL'}`);
  L.push(`- **结论: ${m.pass.verdict ? 'PASS' : 'FAIL'}**`);
  L.push('');
  L.push('## per-client 公平性');
  L.push('');
  L.push('| client | profile | 完成 | 完成率 | p95 |');
  L.push('| --- | --- | --- | --- | --- |');
  for (const c of m.perClient) L.push(`| ${c.client} | ${c.profile} | ${c.done}/${m.total / CLIENTS} | ${pct(c.rate)} | ${ms(c.p95)} |`);
  L.push('');
  L.push('## 客户端侧每分钟完成数');
  L.push('');
  L.push('```');
  L.push(m.perMinute.map((x) => `m${x.minute}:${x.done}`).join(' ') || '(无)');
  L.push('```');
  L.push('');
  L.push('## config 快照(执行时深拷贝)');
  L.push('');
  L.push('```json');
  L.push(JSON.stringify(m.config, null, 2));
  L.push('```');
  fs.writeFileSync(path.join(dir, 'summary.md'), L.join('\n') + '\n');
}

function writeRunReport(matrix, runStamp) {
  const L = [`# 矩阵汇总 RUN-${runStamp}`, '', `目标: ${BASE}(${CFG.target.dockerContainer})  mock: ${MOCK}`, ''];
  L.push('| 轮次 | 完成率 | p50 | p95 | 首击429 | 终失败 | Σ429 | Δrej | maxQ | 判定 |');
  L.push('| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |');
  for (const m of matrix) {
    L.push(`| ${m.round} | ${pct(m.completionRate)} | ${ms(m.p50)} | ${ms(m.p95)} | ${pct(m.first429Rate)} | ${pct(m.finalFailRate)} | ${m.client429Total} | ${m.deltaRejections} | ${m.maxQueued} | ${m.pass.verdict ? 'PASS' : 'FAIL'} |`);
  }
  L.push('');
  L.push(`p95 合格线: ${matrix[0]?.pass.source || 'N/A'};config 快照见各轮 summary.md。`);
  const file = path.join(RESULTS, `RUN-${runStamp}.md`);
  fs.writeFileSync(file, L.join('\n') + '\n');
  console.log('\n' + L.join('\n'));
  log(`矩阵汇总已写: ${file}`);
}

// ---------------- 单轮编排 ----------------
async function runRound(unit, matrix) {
  const dirName = `${unit.key}-${stamp()}`;
  const dir = path.join(RESULTS, dirName);
  fs.mkdirSync(dir, { recursive: true });
  const jsonl = path.join(dir, 'clients.jsonl');
  fs.writeFileSync(jsonl, '');
  fs.writeFileSync(path.join(dir, 'snapshot.jsonl'), '');
  log(`=== 轮次 ${unit.key} 开始 dir=${dirName} 队列=${unit.queue.name} 渠道=${unit.channels} keys=${unit.keys} mode=${unit.mode}`);
  // 每个轮次按 unit.keyLimit 调整 token 闸(M7 设 0 让 channel 闸为唯一瓶颈,M1-M6 保持默认 3)
  KEYS = await ensureKeys(unit);
  // M7 设 RetryTimes=3 让 channel error 触发 gateway 端 retry 退档路径
  await setRetryTimes(unit.retryTimes);
  await setQueue(unit.queue);
  const base = await ensureChannels(unit);

  // 探针单发:1 个小任务必须 200,失败中止该轮
  const pKey = unit.keys === 'shared' ? KEYS['concurrency-test'] : KEYS['pool-key-01'];
  const p0 = now();
  let pres;
  try {
    pres = await fetch(URL_CHAT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${pKey}`, 'New-API-User': '1' },
      body: JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'Reply with exactly: OK' }], stream: false, max_tokens: 16 }),
      signal: AbortSignal.timeout(60000),
    });
  } catch (e) {
    throw new Error(`探针请求异常: ${e?.name || e}`);
  }
  const ptxt = await pres.text().catch(() => '');
  if (pres.status !== 200) {
    log(`探针失败 ${pres.status}: ${ptxt.slice(0, 400)}`);
    throw new Error(`探针单发失败: HTTP ${pres.status}`);
  }
  log(`探针 ok ${now() - p0}ms`);

  const st = { dir, samples: [], stop: false, maxQueued: 0, sumQueued: 0, count429: 0 };
  await sampleOnce(st); // 轮始先采 1 次(Δrejections 起点)
  const t0 = now();
  const all = genTasks();
  const fifos = Array.from({ length: CLIENTS }, () => new Fifo());
  const ctx = {
    url: URL_CHAT,
    attemptTimeout: unit.mode === 'real' ? 180000 : 60000,
    keyMode: unit.keys === 'shared' ? 'shared' : 'per-client',
    keyFor: (c) => (unit.keys === 'shared' ? KEYS['concurrency-test'] : KEYS[`pool-key-${p2(c + 1)}`]),
    records: [],
    count429: 0,
    jsonl,
  };
  const workers = [];
  for (let c = 0; c < CLIENTS; c++) for (let w = 0; w < CFG.load.clientConcurrency; w++) workers.push(worker(fifos[c], ctx));
  const poller = pollLoop(st);

  await dispatch(all, fifos, unit);
  fifos.forEach((f) => f.close());
  log('全部下发完毕,进入 drain(等终态)…');
  await withTimeout(
    Promise.all(workers),
    DRAIN_BUDGET_MS,
    () => log(`[warn] drain 超出预算(${Math.round(DRAIN_BUDGET_MS / 60000)}min),未完成 ${all.length - ctx.records.length} 个任务,继续落盘`),
  );
  st.stop = true;
  await poller;
  await sampleOnce(st); // 轮终采样(Δrejections 终点 + 无泄漏断言)
  st.count429 = ctx.count429;

  const m = metrics(ctx.records, st, unit, all.length, t0, dirName, base);
  writeSummary(dir, m);
  matrix.push(m);
  log(
    `${unit.key}  完成=${pct(m.completionRate)} p50=${ms(m.p50)} p95=${ms(m.p95)} 首击429=${pct(m.first429Rate)} 终失败=${pct(m.finalFailRate)} ` +
      `Σ429=${m.client429Total} Δrej=${m.deltaRejections} maxQ=${m.maxQueued} 终态q/inF=${m.queuedEnd}/${m.inFlightEnd} → ${m.pass.verdict ? 'PASS' : 'FAIL'}(${m.pass.source})`,
  );
  if (!m.reconcile) log(`[warn] 429 对账不相等: Σclient429=${m.client429Total} vs Δrejections=${m.deltaRejections}`);
}

// ---------------- main ----------------
let KEYS = {};

async function main() {
  const sel = selectUnits(process.argv.slice(2));
  if (!sel.length) fail(`没有匹配的轮次(参数: ${process.argv.slice(2).join(' ') || '默认'})`);
  if (!(CFG.upstream.realBudgetTokens > 0)) log('跳过 M6(realBudgetTokens=0)');
  log(`执行轮次: ${sel.map((u) => u.key).join(' → ')}`);
  await ready(sel);
  // ensureKeys 需要 unit 信息才能按轮次设 keyLimit(M7 关掉 key 闸),这里传第一个 unit 即可
  // (M1-M6 全部 default keyLimit=3;M7 显式 keyLimit=0,且单测单轮)
  // ensureKeys 在每个 runRound 入口按 unit.keyLimit 调,这里不预调(避免 token limit 错乱)
  fs.mkdirSync(RESULTS, { recursive: true });
  const runStamp = stamp();
  const matrix = [];
  let broken = null;
  for (const unit of sel) {
    try {
      await runRound(unit, matrix);
    } catch (e) {
      broken = e;
      log(`轮次 ${unit.key} 失败,停止后续轮(已写数据保留): ${e?.message || e}`);
      break;
    }
    await sleep(3000); // 轮间歇
  }
  if (matrix.length) writeRunReport(matrix, runStamp);
  else log('没有任何轮次完成,不写矩阵汇总');
  if (broken) process.exitCode = 1;
  await sleep(300); // 等 stdout 落盘(Windows 管道异步)
  process.exit(process.exitCode || 0);
}

main().catch((e) => {
  console.error('Fatal:', e);
  process.exit(1);
});
