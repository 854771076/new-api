/**
 * Coding-client experience simulator (request-count mode).
 *
 * Models N human-driven coding clients sharing ONE API key against the local
 * gateway. Each client sends exactly SIM_REQUESTS requests (turns may fire
 * 2 parallel requests when backgrounded, still counting toward the budget):
 *   - ~400 completion tokens ≈ 2s upstream,
 *   - think time 2-10s between turns,
 *   - 10% of turns are 2 parallel requests (bg tab + chat).
 *
 * Sweeps configs selected by SIM_CONFIG (comma list of A..E), measures:
 *   success/429/error, ok-latency p50/p95/max/mean, >10s count, 1s-bucket
 *   histogram, peak queued depth (snapshot sampler @1s), per-client ok/429
 *   min/max (fairness across the 10 clients), wall + throughput.
 * Each run appends one JSON line to SIM_OUT for the final report generator.
 *
 * Usage: SIM_CONFIG=C,D SIM_REQUESTS=50 node coding-client-sim.mjs
 */

const BASE = 'http://localhost:3001';
const MODEL = 'Qwen3.6-35B';
// Hardcoded key removed by 02-19: env-only, fail-fast when unset.
const KEY = process.env.SIM_KEY;
if (!KEY) {
  throw new Error('SIM_KEY env var is required (hardcoded key removed; see scripts/key-pool/key-pool-cli.mjs)');
}
const TOKEN_ID = 1;

const CLIENTS = Number(process.env.SIM_CLIENTS || 10);
const REQUESTS = Number(process.env.SIM_REQUESTS || 50); // per client
const OUT = process.env.SIM_OUT || 'sim-report.jsonl';
const SELECTED = (process.env.SIM_CONFIG || 'A,B,C,D,E').split(',').map((s) => s.trim().toUpperCase());
const MAX_TOKENS = 400;

const PROMPTS = [
  'Write a production-quality Python function that merges two sorted linked lists, with type hints, docstring and unit tests.',
  'Refactor this Go snippet to use errors.Is/As instead of string comparison, explain each change in one sentence.',
  '写一个 TypeScript 的防抖 hook,支持取消和立即执行,给出完整代码和使用示例。',
  'Debug: why does this React component re-render in a loop when useEffect depends on an inline object? Show the fix.',
  'Write SQL to find the top 5 customers by revenue per month, excluding refunds. Explain the window function.',
  'Implement an LRU cache in Python with O(1) get/put, thread-safe, with a short rationale of the data structure choice.',
];

// ---------- admin helpers ----------
let ADMIN;
async function login() {
  const r = await fetch(`${BASE}/api/user/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      username: process.env.ADMIN_USER || 'root',
      password: process.env.ADMIN_PASSWORD || '',
    }),
  });
  const j = await r.json();
  ADMIN = j.data.access_token;
  if (!ADMIN) throw new Error('admin login failed');
}
// Lazy: ADMIN is only filled by login() after module load — a static header
// would freeze the value "undefined" (401).
const auth = () => ({ Authorization: `Bearer ${ADMIN}`, 'Content-Type': 'application/json' });

async function setOpt(key, value) {
  const r = await fetch(`${BASE}/api/option/`, { method: 'PUT', headers: auth(), body: JSON.stringify({ key, value }) });
  const j = await r.json();
  if (!j.success) throw new Error(`set ${key}=${value}: ${j.message}`);
}
async function setKeyLimit(limit) {
  const list = await (await fetch(`${BASE}/api/token/?p=1&size=100`, { headers: auth() })).json();
  const t = list.data.items.find((x) => x.id === TOKEN_ID);
  if (!t) throw new Error('token not found');
  const r = await fetch(`${BASE}/api/token/`, {
    method: 'PUT',
    headers: auth(),
    // tokenRequest embeds model.Token flat — fields at the top level, and
    // concurrency_limit is a pointer shadow field on the same level.
    body: JSON.stringify({
      id: t.id, name: t.name, remain_quota: t.remain_quota, unlimited_quota: t.unlimited_quota,
      expired_time: t.expired_time, model_limits_enabled: t.model_limits_enabled, model_limits: t.model_limits,
      allow_ips: t.allow_ips, group: t.group, cross_group_retry: t.cross_group_retry, status: t.status,
      concurrency_limit: limit, concurrency_limit_by_model: t.concurrency_limit_by_model || '',
    }),
  });
  const j = await r.json();
  if (!j.success) throw new Error(`set key limit ${limit}: ${j.message}`);
}
async function queuedPeakSampler(stopRef) {
  let peak = 0;
  while (!stopRef.stop) {
    try {
      const s = await (await fetch(`${BASE}/api/concurrency/snapshot`, { headers: auth() })).json();
      if (typeof s.queued === 'number' && s.queued > peak) peak = s.queued;
    } catch { /* rate-limited tick, skip */ }
    await new Promise((r) => setTimeout(r, 1000)); // 1/s: well under GlobalAPIRateLimit (2/s)
  }
  return peak;
}

// ---------- virtual coding client ----------
function ask(i) {
  const t0 = Date.now();
  return fetch(`${BASE}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY}` },
    body: JSON.stringify({
      model: MODEL,
      messages: [{ role: 'user', content: `${PROMPTS[i % PROMPTS.length]} (case ${Math.random().toString(36).slice(2, 7)})` }],
      max_tokens: MAX_TOKENS,
      stream: false,
    }),
  }).then(async (r) => {
    await r.text();
    return { status: r.status, ms: Date.now() - t0 };
  }).catch(() => ({ status: 0, ms: Date.now() - t0 }));
}

async function runClient(id, budget, latByClient) {
  const mine = [];
  let sent = 0;
  let n = 0;
  while (sent < budget) {
    const parallel = Math.random() < 0.1 && budget - sent >= 2;
    const batch = parallel ? [ask(id * 1000 + n), ask(id * 1000 + n + 500)] : [ask(id * 1000 + n)];
    const rs = await Promise.all(batch);
    for (const r of rs) mine.push(r);
    sent += batch.length;
    n += 1;
    if (sent < budget) await new Promise((r) => setTimeout(r, 2000 + Math.random() * 8000)); // think 2-10s
  }
  latByClient.push(mine);
}

// ---------- stats ----------
const pct = (arr, p) => {
  if (!arr.length) return 0;
  const s = [...arr].sort((a, b) => a - b);
  return s[Math.min(s.length - 1, Math.floor((s.length - 1) * p))];
};
function histogram(ms) {
  const h = {};
  for (const m of ms) {
    const b = m >= 15000 ? '15s+' : `${Math.floor(m / 1000)}s`;
    h[b] = (h[b] || 0) + 1;
  }
  return h;
}

// ---------- sweep ----------
const CONFIGS = {
  A: { name: 'A: C=3 queue-off (harsh baseline)', C: 3, opts: { 'concurrency.queue_enabled': 'false' } },
  B: { name: 'B: C=3 Q=min(2C,4)=4 wait=3s (dev small)', C: 3, opts: { 'concurrency.queue_enabled': 'true', 'concurrency.queue_depth_ratio': '2', 'concurrency.queue_depth_cap': '4', 'concurrency.queue_max_wait_seconds': '3' } },
  C: { name: 'C: C=5 Q=10 wait=5s (recommended)', C: 5, opts: { 'concurrency.queue_enabled': 'true', 'concurrency.queue_depth_ratio': '2', 'concurrency.queue_depth_cap': '10', 'concurrency.queue_max_wait_seconds': '5' } },
  D: { name: 'D: C=8 Q=16 wait=5s (generous)', C: 8, opts: { 'concurrency.queue_enabled': 'true', 'concurrency.queue_depth_ratio': '2', 'concurrency.queue_depth_cap': '16', 'concurrency.queue_max_wait_seconds': '5' } },
  E: { name: 'E: C=5 Q=5(1C) wait=3s (shallow queue check)', C: 5, opts: { 'concurrency.queue_enabled': 'true', 'concurrency.queue_depth_ratio': '1', 'concurrency.queue_depth_cap': '5', 'concurrency.queue_max_wait_seconds': '3' } },
};

import { appendFileSync } from 'node:fs';

await login();
console.log(`sim: ${CLIENTS} clients × ${REQUESTS} requests = ${CLIENTS * REQUESTS} req/config, configs=${SELECTED.join(',')}\n`);

for (const key of SELECTED) {
  const cfg = CONFIGS[key];
  if (!cfg) throw new Error(`unknown config ${key}`);
  await setKeyLimit(cfg.C);
  for (const [k, v] of Object.entries(cfg.opts)) await setOpt(k, v);

  const latByClient = [];
  const stopRef = { stop: false };
  const sampler = queuedPeakSampler(stopRef);
  const t0 = Date.now();
  await Promise.all(Array.from({ length: CLIENTS }, (_, id) => runClient(id, REQUESTS, latByClient)));
  const wall = (Date.now() - t0) / 1000;
  stopRef.stop = true;
  const peakQ = await sampler;

  const all = latByClient.flat();
  const ok = all.filter((r) => r.status === 200);
  const r429 = all.filter((r) => r.status === 429);
  const err = all.filter((r) => r.status !== 200 && r.status !== 429);
  const oks = ok.map((r) => r.ms);
  const perClientOk = latByClient.map((rs) => rs.filter((r) => r.status === 200).length);
  const perClient429 = latByClient.map((rs) => rs.filter((r) => r.status === 429).length);

  const row = {
    config: key, name: cfg.name, C: cfg.C, opts: cfg.opts,
    clients: CLIENTS, requestsPerClient: REQUESTS,
    total: all.length, ok: ok.length, r429: r429.length, err: err.length,
    okPct: +((ok.length / all.length) * 100).toFixed(1),
    p50: pct(oks, 0.5), p95: pct(oks, 0.95),
    mean: oks.length ? Math.round(oks.reduce((a, b) => a + b, 0) / oks.length) : 0,
    max: oks.length ? Math.max(...oks) : 0,
    slow10s: oks.filter((m) => m > 10000).length,
    hist: histogram(oks),
    peakQ,
    wallS: Math.round(wall),
    throughput: +(ok.length / wall).toFixed(2),
    perClientOk: { min: Math.min(...perClientOk), max: Math.max(...perClientOk) },
    perClient429: { min: Math.min(...perClient429), max: Math.max(...perClient429) },
    finishedAt: new Date().toISOString(),
  };
  appendFileSync(OUT, JSON.stringify(row) + '\n');
  console.log(
    `${cfg.name}\n  ok=${row.ok}/${row.total} (${row.okPct}%) 429=${row.r429} err=${row.err} | p50=${row.p50}ms mean=${row.mean}ms p95=${row.p95}ms max=${row.max}ms | >10s=${row.slow10s} peakQ=${row.peakQ} wall=${row.wallS}s thr=${row.throughput}/s | clientOk[min,max]=[${row.perClientOk.min},${row.perClientOk.max}] client429[${row.perClient429.min},${row.perClient429.max}]`,
  );

  // restore dev baseline between configs so each run starts clean
  await setKeyLimit(3);
  await setOpt('concurrency.queue_enabled', 'true');
  await setOpt('concurrency.queue_depth_ratio', '2');
  await setOpt('concurrency.queue_depth_cap', '4');
  await setOpt('concurrency.queue_max_wait_seconds', '3');
}
console.log(`\nrows appended to ${OUT}`);
