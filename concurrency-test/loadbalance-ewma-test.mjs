// loadbalance-ewma-test.mjs — EWMA 动态有效权重负载均衡测试(LOADBALANCE-PLAN.md)
// 四档任务打流 + 3s 采样 routing-health/snapshot + 结束拉取 usage log 归因。
// 用法(在仓库根目录): NODE_NO_WARNINGS=1 node concurrency-test/loadbalance-ewma-test.mjs
import fs from 'node:fs';

const BASE = 'http://127.0.0.1:3001';
const MODEL = 'Qwen3.6-35B';
const KEY = fs.readFileSync('logs/lb-token.txt', 'utf8').trim();
const ADMIN = {
  username: process.env.ADMIN_USER || 'root',
  password: process.env.ADMIN_PASSWORD || '',
};
const CONCURRENCY = Number(process.env.CONCURRENCY || 4);
const PACE_MS = Number(process.env.PACE_MS || 0); // 派发间隔,>0 时逐发限速
const EXP = process.env.EXP || 'homo'; // 实验标签,进入输出文件名
const REQ_TIMEOUT_MS = 180_000;

// 任务矩阵(方案定稿):长度≈token,输出恒短;SHORT_N 可用环境变量加长 warm 段
const SHORT_N = Number(process.env.SHORT_N || 32);
const TIERS = [
  { tier: 'short', tokens: 500, count: SHORT_N },
  { tier: 'medium', tokens: 1000, count: 20 },
  { tier: 'long', tokens: 2000, count: 10 },
  { tier: 'ultra', tokens: 4000, count: 5 },
];

async function api(path, opts = {}, tok) {
  const r = await fetch(`${BASE}${path}`, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(tok ? { Authorization: `Bearer ${tok}`, 'New-API-User': '1' } : {}),
      ...(opts.headers || {}),
    },
  });
  return r.json();
}

function makePrompt(targetTokens, reqId) {
  // ~1.3 token/word 英文填充 → 目标词数 = tokens/1.3;确定性句子循环 + 唯一标记
  const words = Math.floor(targetTokens / 1.3);
  const sentence = 'packet latency jitter fairness probe channel weighting observation repeat. ';
  const fill = sentence.repeat(Math.ceil(words / 10)).split(' ').slice(0, words).join(' ');
  return `[req:${reqId}] ${fill}\nReply with exactly: OK`;
}

// seeded RNG(可复现乱序)
function mulberry32(a) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------- 任务构建 ----------
const tasks = [];
let rid = 0;
for (const t of TIERS) for (let i = 0; i < t.count; i++) tasks.push({ ...t, id: `r${++rid}` });
// Phase A: 全部 short 先打(warm 门 ≥10 样本/渠道)
const phaseA = tasks.filter((t) => t.tier === 'short');
// Phase B: 其余乱序(seed 固定)
const phaseB = tasks.filter((t) => t.tier !== 'short');
{
  const rnd = mulberry32(20261014);
  for (let i = phaseB.length - 1; i > 0; i--) {
    const j = Math.floor(rnd() * (i + 1));
    [phaseB[i], phaseB[j]] = [phaseB[j], phaseB[i]];
  }
}

const requestLog = [];
const timeline = [];
const markers = [];

async function runTask(t) {
  const t0 = Date.now();
  let status = 0, err = '';
  try {
    const ctl = new AbortController();
    const to = setTimeout(() => ctl.abort(), REQ_TIMEOUT_MS);
    const r = await fetch(`${BASE}/v1/chat/completions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY}` },
      body: JSON.stringify({
        model: MODEL,
        messages: [{ role: 'user', content: makePrompt(t.tokens, t.id) }],
        max_tokens: 8, stream: false,
      }),
      signal: ctl.signal,
    });
    status = r.status;
    await r.text();
    clearTimeout(to);
  } catch (e) { err = String(e).slice(0, 120); status = -1; }
  requestLog.push({ id: t.id, tier: t.tier, tokens: t.tokens, ms: Date.now() - t0, status, err, ts: t0 });
}

async function pool(list, n) {
  let i = 0;
  const workers = Array.from({ length: n }, async () => {
    while (i < list.length) {
      const t = list[i++];
      if (PACE_MS > 0) await new Promise((r) => setTimeout(r, PACE_MS));
      await runTask(t);
    }
  });
  await Promise.all(workers);
}

// ---------- 观测采样 ----------
let sampling = true;
async function sampleLoop() {
  let tok = '';
  while (sampling) {
    try {
      if (!tok) {
        const lg = await api('/api/user/login', { method: 'POST', body: JSON.stringify(ADMIN) });
        tok = lg.data?.access_token || '';
      }
      const [rh, snap] = await Promise.all([
        api('/api/concurrency/routing-health', {}, tok),
        api('/api/concurrency/snapshot', {}, tok),
      ]);
      const now = Date.now();
      if (rh.success) timeline.push({ ts: now, kind: 'health', rows: rh.data });
      else tok = ''; // token 过期重登
      if (snap.success) {
        const scopes = snap.data?.scopes || [];
        const chRows = scopes.filter((r) => r.kind === 'channel');
        timeline.push({ ts: now, kind: 'inflight', rows: chRows, queued: snap.data?.queued });
      }
    } catch { /* tick skip */ }
    await new Promise((r) => setTimeout(r, 3000));
  }
}

// ---------- 主流程 ----------
console.log(`tasks: A(warm short)=${phaseA.length}, B(mixed)=${phaseB.length}, conc=${CONCURRENCY}`);
const samplerDone = sampleLoop();
const wallStart = Date.now();

await pool(phaseA, CONCURRENCY);
markers.push({ ts: Date.now(), label: 'phaseA_done' });
console.log(`phase A done in ${((Date.now() - wallStart) / 1000).toFixed(1)}s`);

await pool(phaseB, CONCURRENCY);
markers.push({ ts: Date.now(), label: 'phaseB_done' });
console.log(`phase B done in ${((Date.now() - wallStart) / 1000).toFixed(1)}s`);

// Phase C(可选):POST_N 发短请求按 3s 间隔散打——强制 warm 后的惰性重算落地并进时间线
const POST_N = Number(process.env.POST_N || 0);
if (POST_N > 0) {
  for (let i = 0; i < POST_N; i++) {
    await runTask({ tier: 'short', tokens: 500, id: `p${i + 1}` });
    await new Promise((r) => setTimeout(r, 3000));
  }
  markers.push({ ts: Date.now(), label: 'phaseC_done' });
  console.log(`phase C done in ${((Date.now() - wallStart) / 1000).toFixed(1)}s`);
}

// 尾采样 6s(EWMA 最后一次重算可见)
await new Promise((r) => setTimeout(r, 6000));
sampling = false;
await samplerDone;

// ---------- usage log 归因 ----------
let tok = (await api('/api/user/login', { method: 'POST', body: JSON.stringify(ADMIN) })).data.access_token;
const usageLogs = [];
for (let p = 1; p <= 10; p++) {
  const jl = await api(`/api/log/?p=${p}&size=100&type=0`, {}, tok);
  if (!jl.success || !jl.data?.items?.length) break;
  const items = jl.data.items.filter((l) => l.model_name === MODEL && l.created_at * 1000 >= wallStart - 5000);
  usageLogs.push(...items);
  if (items.length < 100) break;
}

// ---------- 落盘 ----------
const write = (f, d) => fs.writeFileSync(f, typeof d === 'string' ? d : JSON.stringify(d, null, 1));
write(`logs/lb-requests-${EXP}.json`, requestLog);
write(`logs/lb-timeline-${EXP}.json`, timeline);
write(`logs/lb-markers-${EXP}.json`, markers);
write(`logs/lb-usage-logs-${EXP}.json`, usageLogs);

const ok = requestLog.filter((r) => r.status === 200).length;
console.log(`DONE: ${ok}/${requestLog.length} ok, timeline ${timeline.length} ticks, usage logs ${usageLogs.length}`);
