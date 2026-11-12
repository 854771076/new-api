/**
 * Multi-key pool balancing test.
 *
 * 10 local tokens (pool-key-01..10), each with its own gateway concurrency
 * bucket — the pool's aggregate capacity is the SUM of per-key limits (the
 * user axis is unlimited). This measures how different routing strategies
 * distribute tasks across keys with DIFFERENT limits (weights):
 *
 *   rr        naive round-robin            (ignores limits — baseline)
 *   least     least-in-flight              (equal-limit best practice)
 *   weighted  smooth weighted round-robin  (weight = per-key limit)
 *
 * On 429 the task backs off briefly and RE-ROUTES via the strategy (the
 * pool has spare capacity elsewhere, so retry-on-same-key would be dumb).
 *
 * Usage: KEY_POOL='[{"name","key","weight"},...]' node multi-key-test.mjs <rr|least|weighted> [tasks]
 *        (hardcoded keys removed by 02-19 — env-only; see also the evolved
 *         four-strategy driver scripts/key-pool/key-pool-cli.mjs)
 */

import { parseKeyPoolEnv } from '../scripts/key-pool/key-pool.mjs';

const BASE_URL = 'http://localhost:3001';
const MODEL = 'Qwen3.6-35B';

// name | key | concurrency limit (= routing weight for `weighted`)
// KEY_POOL JSON array is the only credential path; unset/invalid => throws.
const POOL = parseKeyPoolEnv(process.env.KEY_POOL).map(({ name, key, weight }) => ({
  name,
  key,
  limit: weight,
}));

const STRATEGY = process.argv[2] || 'least';
const TOTAL = Number(process.argv[3] || 150);
if (!['rr', 'least', 'weighted'].includes(STRATEGY)) {
  console.error('strategy must be rr | least | weighted');
  process.exit(1);
}

const PROMPTS = [
  { content: '用三段话解释量子纠缠：物理现象、爱因斯坦的反对、量子计算用途。每段不超过50字。', max_tokens: 400 },
  { content: 'Write a production-quality Python binary search function with type hints, docstring, and empty-list handling. Only the function.', max_tokens: 400 },
  { content: '写一个关于机器人学会做梦的微型科幻故事，250字左右，有起承转合，结尾留悬念。', max_tokens: 400 },
  { content: 'Compare HTTP/1.1, HTTP/2, HTTP/3 in three bullet points: multiplexing, head-of-line blocking, transport. Max 80 words.', max_tokens: 400 },
  { content: '从成本、性能、生态三个角度分析中小团队为何倾向自建推理服务，每点一句话。', max_tokens: 400 },
  { content: 'Describe a day in the life of a cat who secretly runs a corporation. Exactly 4 sentences, humorous but professional.', max_tokens: 400 },
];

// per-key live state
const pool = POOL.map((k) => ({ ...k, inFlight: 0, sent: 0, ok: 0, rejected: 0, errors: 0 }));
let rrCursor = 0;
// smooth weighted round-robin accumulators (nginx algorithm)
const wrAccum = pool.map(() => 0);

function pickKey() {
  if (STRATEGY === 'rr') {
    return rrCursor++ % pool.length;
  }
  if (STRATEGY === 'least') {
    let best = 0;
    for (let i = 1; i < pool.length; i++) {
      if (pool[i].inFlight < pool[best].inFlight) best = i;
    }
    return best;
  }
  // weighted: smooth weighted round-robin, weight = per-key limit
  let totalWeight = 0;
  for (const k of pool) totalWeight += k.limit;
  let best = 0;
  for (let i = 0; i < pool.length; i++) {
    wrAccum[i] += pool[i].limit;
    if (wrAccum[i] > wrAccum[best]) best = i;
  }
  wrAccum[best] -= totalWeight;
  return best;
}

function backoff(attempt) {
  const ms = Math.min(250 * Math.pow(1.3, attempt), 3000);
  return Math.round(ms + Math.random() * 200);
}

async function runTask(taskIdx) {
  const prompt = PROMPTS[taskIdx % PROMPTS.length];
  let attempts = 0;
  for (;;) {
    attempts++;
    const ki = pickKey();
    const k = pool[ki];
    k.sent++;
    k.inFlight++;
    try {
      const res = await fetch(`${BASE_URL}/v1/chat/completions`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${k.key}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({
          model: MODEL,
          messages: [{ role: 'user', content: prompt.content }],
          max_tokens: prompt.max_tokens,
          stream: false,
        }),
        signal: AbortSignal.timeout(60_000),
      });
      k.inFlight--;
      if (res.status === 429) {
        k.rejected++;
        await res.text().catch(() => {});
        await sleep(backoff(attempts));
        continue;
      }
      if (res.ok) {
        await res.json().catch(() => {});
        k.ok++;
        return;
      }
      await res.text().catch(() => {});
      k.errors++;
      return;
    } catch {
      k.inFlight--;
      k.errors++;
      return;
    }
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const limits = pool.map((k) => k.limit).join(',');
  console.log(`strategy=${STRATEGY} tasks=${TOTAL} per-key limits=[${limits}] aggregate=${pool.reduce((s, k) => s + k.limit, 0)}`);
  const t0 = Date.now();
  await Promise.all(Array.from({ length: TOTAL }, (_, i) => runTask(i)));
  const wall = Date.now() - t0;

  const ok = pool.reduce((s, k) => s + k.ok, 0);
  const rejected = pool.reduce((s, k) => s + k.rejected, 0);
  const errors = pool.reduce((s, k) => s + k.errors, 0);

  console.log('\n========== RESULT ==========');
  console.log(`wall time        : ${(wall / 1000).toFixed(1)}s`);
  console.log(`completed        : ${ok}/${TOTAL} (errors ${errors})`);
  console.log(`total 429s       : ${rejected}`);
  console.log(`throughput       : ${(ok / (wall / 1000)).toFixed(2)} tasks/s`);
  console.log('per-key breakdown:');
  const oks = pool.map((k) => k.ok);
  const mean = oks.reduce((s, v) => s + v, 0) / oks.length;
  const sd = Math.sqrt(oks.reduce((s, v) => s + (v - mean) ** 2, 0) / oks.length);
  for (const k of pool) {
    const bar = '#'.repeat(Math.round(k.ok / 3));
    console.log(
      `  ${k.name} limit=${k.limit} ok=${String(k.ok).padStart(3)} 429=${String(k.rejected).padStart(4)} ${bar}`,
    );
  }
  console.log(`ok distribution  : mean=${mean.toFixed(1)} stddev=${sd.toFixed(1)} (lower = fairer)`);
  // proportional expectation for weighted runs
  if (STRATEGY === 'weighted') {
    const totalLimit = pool.reduce((s, k) => s + k.limit, 0);
    const expected = pool.map((k) => ((k.limit / totalLimit) * ok).toFixed(1));
    console.log(`proportional fit : expected ok = [${expected.join(', ')}]`);
  }
  console.log('============================');
}

main().catch((e) => {
  console.error('Fatal:', e);
  process.exit(1);
});
