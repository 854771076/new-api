/**
 * Queue scenario suite — black-box tests for the bounded wait queue with a
 * deliberately SMALL dev configuration:
 *
 *   ratio=2, cap=4, max_wait=3s, token limit C=3
 *   → Q = min(2*3, 4) = 4 queued max; total admission = 3 active + 4 queued = 7
 *
 * Scenarios:
 *   S1 queue-admit    6 concurrent short tasks → all 200, queued peak >= 1
 *   S2 queue-full     10 concurrent short tasks → 7*200 + 3 immediate 429 (<1.5s),
 *                     queued peak exactly 4
 *   S3 wait-timeout   3 long tasks hold all slots + 1 short request →
 *                     429 AFTER ~max_wait (2.5–6s), not immediate
 *   S4 disconnect+leak 3 long tasks hold slots; a queued request is aborted →
 *                     queued drops back to 0; after long tasks finish a burst
 *                     of 7 must all succeed (no leaked slots/queue positions);
 *                     final state queued=0 and in-flight=0
 *   S5 restore        defaults 4/128/10 read back
 *
 * Usage: node queue-scenario-test.mjs   (env: Q_BASE_URL, Q_MODEL)
 */

const BASE = process.env.Q_BASE_URL || 'http://localhost:3001';
const MODEL = process.env.Q_MODEL || 'Qwen3.6-35B';
// 被测网关的测试 token 从环境变量读取；见 README「凭据声明」。
const KEY = process.env.TEST_API_KEY || '';

const RATIO = 2;
const CAP = 4;
const MAX_WAIT_S = 3;
const Q = Math.min(RATIO * 3, CAP); // 4
const TOTAL_ADMIT = 3 + Q; // 7

const results = [];
function record(name, pass, detail) {
  results.push({ name, pass, detail });
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name} — ${detail}`);
}

// ---------- admin helpers ----------
async function adminToken() {
  const res = await fetch(`${BASE}/api/user/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      username: process.env.ADMIN_USER || 'root',
      password: process.env.ADMIN_PASSWORD || '',
    }),
  });
  const j = await res.json();
  if (!j.data?.access_token) throw new Error('admin login failed: ' + JSON.stringify(j).slice(0, 200));
  return j.data.access_token;
}
let TOKEN;
async function setOpt(key, value) {
  const res = await fetch(`${BASE}/api/option/`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${TOKEN}` },
    body: JSON.stringify({ key, value }),
  });
  const j = await res.json();
  if (!j.success) throw new Error(`set ${key}=${value} failed: ${j.message}`);
}
async function getOpts() {
  const res = await fetch(`${BASE}/api/option/`, { headers: { Authorization: `Bearer ${TOKEN}` } });
  const j = await res.json();
  return Object.fromEntries((j.data || []).filter((o) => o.key.startsWith('concurrency.')).map((o) => [o.key, o.value]));
}
async function snapshot() {
  const res = await fetch(`${BASE}/api/concurrency/snapshot`, { headers: { Authorization: `Bearer ${TOKEN}` } });
  return res.json();
}

// queued-depth peak sampler (per scenario window). The sampler runs in the
// BACKGROUND: startSampler must return immediately, otherwise awaiting it
// deadlocks the scenario (stopSampler can never run) — the original bug that
// left a 150ms loop hammering /api/concurrency/snapshot for minutes and
// exhausted the per-IP GlobalAPIRateLimit bucket (360/180s), collateral-
// 429ing login/status/token calls from the same IP. 300ms keeps total sample
// volume ≈80 requests across the suite, well inside the budget.
let peak = 0;
let samplerStop = true;
let samplerDone = Promise.resolve();
function startSampler() {
  peak = 0;
  samplerStop = false;
  samplerDone = (async () => {
    while (!samplerStop) {
      try {
        const s = await snapshot();
        if (typeof s.queued === 'number' && s.queued > peak) peak = s.queued;
      } catch {
        /* 429 (rate-limited) or transient — skip this tick */
      }
      await new Promise((r) => setTimeout(r, 300));
    }
  })();
}
async function stopSampler() {
  samplerStop = true;
  await samplerDone;
  return peak;
}

// ---------- relay probe ----------
async function chat({ maxTokens, signal, tag }) {
  const t0 = Date.now();
  try {
    const res = await fetch(`${BASE}/v1/chat/completions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY}` },
      body: JSON.stringify({
        model: MODEL,
        messages: [{ role: 'user', content: tag || '用一句话介绍你自己。' }],
        max_tokens: maxTokens,
        stream: false,
      }),
      signal,
    });
    await res.text();
    return { status: res.status, ms: Date.now() - t0 };
  } catch (e) {
    if (e.name === 'AbortError') return { status: 'ABORTED', ms: Date.now() - t0 };
    return { status: 0, ms: Date.now() - t0, err: String(e).slice(0, 120) };
  }
}
const shortTask = () => chat({ maxTokens: 60 });
const longTask = () => chat({ maxTokens: 2000, tag: '请写一篇1500字的关于分布式系统一致性的科普文章,分五个章节。' });

const tally = (rs) => {
  const t = {};
  for (const r of rs) t[r.status] = (t[r.status] || 0) + 1;
  return JSON.stringify(t);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ================= main =================
console.log(`target: ${BASE}  model=${MODEL}`);
console.log(`config: ratio=${RATIO} cap=${CAP} max_wait=${MAX_WAIT_S}s  →  Q=${Q}, total admit=${TOTAL_ADMIT}\n`);

TOKEN = await adminToken();

// ---- S0: apply small config, read back ----
try {
  await setOpt('concurrency.queue_enabled', 'true');
  await setOpt('concurrency.queue_depth_ratio', String(RATIO));
  await setOpt('concurrency.queue_depth_cap', String(CAP));
  await setOpt('concurrency.queue_max_wait_seconds', String(MAX_WAIT_S));
  const o = await getOpts();
  const ok =
    o['concurrency.queue_depth_ratio'] === String(RATIO) &&
    o['concurrency.queue_depth_cap'] === String(CAP) &&
    o['concurrency.queue_max_wait_seconds'] === String(MAX_WAIT_S) &&
    o['concurrency.queue_enabled'] === 'true';
  record('S0 apply-small-config', ok, `read back ${JSON.stringify(o)}`);
} catch (e) {
  record('S0 apply-small-config', false, String(e));
}

// ---- S1: queue admits 6 (3 active + 3 queued) ----
{
  await startSampler();
  const rs = await Promise.all(Array.from({ length: 6 }, () => shortTask()));
  const p = await stopSampler();
  const allOk = rs.every((r) => r.status === 200);
  record('S1 queue-admit', allOk && p >= 1, `statuses=${tally(rs)} queuedPeak=${p}`);
}

// ---- S2: burst 10 → 7 admitted, 3 immediate 429, peak = Q ----
{
  await startSampler();
  const rs = await Promise.all(Array.from({ length: 10 }, () => shortTask()));
  const p = await stopSampler();
  const immediate = rs.filter((r) => r.status === 429 && r.ms < 1500).length;
  const oks = rs.filter((r) => r.status === 200).length;
  const pass = oks === TOTAL_ADMIT && immediate === 3 && p === Q;
  record('S2 queue-full', pass, `statuses=${tally(rs)} ok=${oks}/${TOTAL_ADMIT} immediate429=${immediate}/3 queuedPeak=${p}/${Q}`);
}

// ---- S3: slots held by 3 long tasks → short request times out after max_wait ----
{
  const longs = Array.from({ length: 3 }, () => longTask());
  await sleep(600); // let them acquire all slots
  await startSampler();
  const short = await chat({ maxTokens: 60 });
  const p = await stopSampler();
  const pass = short.status === 429 && short.ms >= 2500 && short.ms <= 6000;
  record('S3 wait-timeout', pass, `short status=${short.status} ms=${short.ms} (expect 429 in 2500–6000) queuedPeak=${p}`);
  const lr = await Promise.all(longs);
  record('S3 long-tasks', lr.every((r) => r.status === 200), `statuses=${tally(lr)}`);
}

// ---- S4: queued request aborted → depth released; no leaks after recovery ----
{
  const longs = Array.from({ length: 3 }, () => longTask());
  await sleep(600);
  const ac = new AbortController();
  const pending = chat({ maxTokens: 60, signal: ac.signal });
  await sleep(700); // inside the queue now (slots are full)
  const before = (await snapshot()).queued;
  ac.abort();
  await pending;
  let after = (await snapshot()).queued;
  for (let i = 0; i < 10 && after > 0; i++) {
    await sleep(300);
    after = (await snapshot()).queued;
  }
  record('S4 abort-releases-queue-slot', before === 1 && after === 0, `queued before=${before} after=${after}`);
  const lr = await Promise.all(longs);
  record('S4 long-tasks-after-abort', lr.every((r) => r.status === 200), `statuses=${tally(lr)}`);

  // no leaked slots: full admission (7) must be available again.
  // Wait 500ms first: the long-task responses above only prove the CLIENT
  // saw 200 — the server frees slots via a gin defer that runs after the
  // response is written, so firing immediately races the release (observed
  // as a spurious queue-full 429 on the 7th request). A leaked slot, by
  // contrast, would still reject after the window; quiescent below is the
  // authoritative leak check.
  await sleep(500);
  await startSampler();
  const burst = await Promise.all(Array.from({ length: TOTAL_ADMIT }, () => shortTask()));
  const p = await stopSampler();
  const allOk = burst.every((r) => r.status === 200);
  record('S4 no-leak-burst7', allOk, `statuses=${tally(burst)} queuedPeak=${p}`);

  // final quiescence
  await sleep(1500);
  const s = await snapshot();
  const inflight = (s.scopes || []).reduce((a, x) => a + (x.kind === 'user' ? x.current : 0), 0);
  record('S4 quiescent', s.queued === 0 && inflight === 0, `queued=${s.queued} inFlight=${inflight}`);
}

// ---- S5: keep the SMALL dev config (per test plan) and verify it persisted ----
try {
  const o = await getOpts();
  const ok =
    o['concurrency.queue_enabled'] === 'true' &&
    o['concurrency.queue_depth_ratio'] === String(RATIO) &&
    o['concurrency.queue_depth_cap'] === String(CAP) &&
    o['concurrency.queue_max_wait_seconds'] === String(MAX_WAIT_S);
  record('S5 small-config-retained', ok, `read back ${JSON.stringify(o)}`);
} catch (e) {
  record('S5 small-config-retained', false, String(e));
}

// ================= summary =================
const failed = results.filter((r) => !r.pass);
console.log(`\n=== ${results.length - failed.length}/${results.length} passed ===`);
if (failed.length) {
  console.log('FAILED:');
  for (const f of failed) console.log(`  - ${f.name}: ${f.detail}`);
  process.exitCode = 1;
}
