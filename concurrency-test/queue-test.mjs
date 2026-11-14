/**
 * Bounded wait-queue behaviour test (aggressive tier: Q = min(4xC, 128)).
 *
 * Token concurrency-test has limit 3 → Q = 12. Firing 20 concurrent requests
 * should produce: 3 admitted immediately + 12 queued (wait then proceed) +
 * 5 immediate 429 (queue full). Polls /concurrency/snapshot for queued>0.
 *
 * Usage: node queue-test.mjs   (env: Q_BURST=20, Q_PROMPT_TOKENS=200)
 */

const BASE_URL = 'http://localhost:3001';
// 被测网关的测试 token 从环境变量读取；见 README「凭据声明」。
const API_KEY = process.env.TEST_API_KEY || '';
const MODEL = 'Qwen3.6-35B';

const BURST = Number(process.env.Q_BURST || 20);
const MAX_TOKENS = Number(process.env.Q_PROMPT_TOKENS || 200);

const PROMPT =
  '写一个关于机器人学会做梦的微型科幻故事，300字左右，有起承转合。';

async function sendOne(i) {
  const t0 = Date.now();
  try {
    const res = await fetch(`${BASE_URL}/v1/chat/completions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${API_KEY}`,
      },
      body: JSON.stringify({
        model: MODEL,
        messages: [{ role: 'user', content: `${PROMPT} #${i}` }],
        max_tokens: MAX_TOKENS,
        stream: false,
      }),
    });
    const ms = Date.now() - t0;
    if (res.ok) {
      await res.json();
      return { i, status: res.status, ms, retryAfter: res.headers.get('retry-after') };
    }
    await res.text();
    return { i, status: res.status, ms, retryAfter: res.headers.get('retry-after') };
  } catch (e) {
    return { i, status: 0, ms: Date.now() - t0, error: String(e) };
  }
}

// Poll snapshot for queued depth while the burst runs.
let maxQueued = 0;
let pollError = null;
const pollStop = { stop: false };
async function pollQueued() {
  while (!pollStop.stop) {
    try {
      // Reuse nothing fancy: endpoint needs auth; reuse the relay key? No —
      // snapshot is admin-only. Skip silently if unauthorized (best effort).
      const res = await fetch(`${BASE_URL}/api/concurrency/snapshot`, {
        headers: { Authorization: `Bearer ${process.env.ADMIN_TOKEN || ''}` },
      });
      if (res.ok) {
        const j = await res.json();
        if (typeof j.queued === 'number' && j.queued > maxQueued) {
          maxQueued = j.queued;
        }
      }
    } catch (e) {
      pollError = String(e);
    }
    await new Promise((r) => setTimeout(r, 300));
  }
}

const adminToken = process.env.ADMIN_TOKEN;
if (!adminToken) {
  console.log('NOTE: ADMIN_TOKEN not set — queued-depth polling disabled.');
}

const t0 = Date.now();
const poller = adminToken ? pollQueued() : Promise.resolve();
const results = await Promise.all(
  Array.from({ length: BURST }, (_, i) => sendOne(i)),
);
pollStop.stop = true;
await poller;

const byStatus = {};
for (const r of results) byStatus[r.status] = (byStatus[r.status] || 0) + 1;

const ok = results.filter((r) => r.status === 200);
const rejected = results.filter((r) => r.status === 429);
const quick429 = rejected.filter((r) => r.ms < 1000);
const slow429 = rejected.filter((r) => r.ms >= 1000);
const p50 = (arr) => {
  if (!arr.length) return 0;
  const s = [...arr].sort((a, b) => a.ms - b.ms);
  return s[Math.floor(s.length / 2)].ms;
};

console.log('=== queue behaviour ===');
console.log(`burst=${BURST} limit=3 Q=min(4*3,128)=12`);
console.log('status counts:', JSON.stringify(byStatus));
console.log(
  `ok=${ok.length} (p50 ${p50(ok)}ms, max ${Math.max(...ok.map((r) => r.ms), 0)}ms)`,
);
console.log(
  `429=${rejected.length} — immediate(<1s, queue full)=${quick429.length}, timeout(>=1s)=${slow429.length}`,
);
if (rejected.length) {
  const ras = [...new Set(rejected.map((r) => r.retryAfter))];
  console.log(`429 Retry-After values: ${JSON.stringify(ras)}`);
}
console.log(`max queued depth observed: ${maxQueued}${pollError ? ` (poll error: ${pollError})` : ''}`);
console.log(`wall=${((Date.now() - t0) / 1000).toFixed(1)}s`);
