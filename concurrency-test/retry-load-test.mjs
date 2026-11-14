/**
 * 150-task batch with 429 retry + escalating backoff against the local
 * gateway (token concurrency_limit = 3).
 *
 * Design:
 *  - ALL 150 requests fire at once — the gateway's limit does the gating,
 *    exactly like a thundering-herd client pool would.
 *  - Every 429 is retried by the SAME task loop with escalating backoff:
 *      delay(attempt) = min(BASE * FACTOR^attempt, CAP) + jitter
 *    BASE honours the server's Retry-After: 1; CAP keeps the tail bounded.
 *  - A task succeeds only when one of its attempts returns 200; the run
 *    finishes when all 150 tasks settle.
 *
 * Reports: total wall time, throughput, 429 volume by attempt number
 * (proves the backoff ladder), per-task queue wait vs execution latency.
 */

const BASE_URL = 'http://localhost:3001';
// 被测网关的测试 token 从环境变量读取；见 README「凭据声明」。
const API_KEY = process.env.TEST_API_KEY || '';
const MODEL = 'Qwen3.6-35B';

const TOTAL_TASKS = Number(process.env.TASKS || 150);
const BACKOFF_BASE_MS = 1000; // server Retry-After: 1
const BACKOFF_FACTOR = 1.5;
const BACKOFF_CAP_MS = 10_000;
const JITTER_MS = 300;
const MAX_ATTEMPTS = 80; // safety net, effectively never hit at cap 10s

// Medium-length prompts, varied content (cycled across the 150 tasks).
const PROMPTS = [
  {
    label: '中-解释',
    content:
      '用三段话解释什么是量子纠缠：第一段讲物理现象，第二段讲为什么爱因斯坦不喜欢它，第三段讲它在量子计算里的用途。每段不超过50字。',
    max_tokens: 400,
  },
  {
    label: '中-代码',
    content:
      'Write a production-quality Python function implementing binary search over a sorted list of integers. Include type hints, a docstring, and handle the empty-list case. Only the function, no explanation.',
    max_tokens: 400,
  },
  {
    label: '中-故事',
    content:
      '写一个关于机器人学会做梦的微型科幻故事，250字左右，有起承转合，结尾留一个悬念。',
    max_tokens: 400,
  },
  {
    label: '中-对比',
    content:
      'Compare HTTP/1.1, HTTP/2 and HTTP/3 in exactly three bullet points: one bullet per version covering multiplexing, head-of-line blocking, and transport protocol. Max 80 words total.',
    max_tokens: 400,
  },
  {
    label: '中-分析',
    content: '从成本、性能、生态三个角度分析为什么中小团队倾向于自建推理服务而非调用云API，每点一句话，共三句。',
    max_tokens: 400,
  },
  {
    label: '中-创意',
    content:
      'Describe a day in the life of a cat who secretly runs a corporation. Write exactly 4 sentences, humorous but professional in tone.',
    max_tokens: 400,
  },
];

const stats = {
  sent: 0,
  ok: 0,
  rejected429: 0,
  errors: 0,
  byAttempt: new Array(12).fill(0), // 429s seen at attempt index
  tasks: [], // {start, settled, attempts, execMs, waitedMs}
};

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function backoffFor(attempt) {
  // attempt starts at 0 for the first retry after the initial 429.
  const exp = Math.min(
    BACKOFF_BASE_MS * Math.pow(BACKOFF_FACTOR, attempt),
    BACKOFF_CAP_MS,
  );
  return Math.round(exp + Math.random() * JITTER_MS);
}

async function runTask(i) {
  const prompt = PROMPTS[i % PROMPTS.length];
  const start = Date.now();
  let attempts = 0;
  let execMs = 0;

  for (;;) {
    attempts++;
    stats.sent++;
    const t0 = Date.now();
    try {
      const res = await fetch(`${BASE_URL}/v1/chat/completions`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${API_KEY}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          model: MODEL,
          messages: [{ role: 'user', content: prompt.content }],
          max_tokens: prompt.max_tokens,
          stream: false,
        }),
        signal: AbortSignal.timeout(60_000),
      });
      execMs = Date.now() - t0;

      if (res.status === 429) {
        stats.rejected429++;
        const bucket = Math.min(attempts - 1, stats.byAttempt.length - 1);
        stats.byAttempt[bucket]++;
        await res.text().catch(() => '');
        if (attempts >= MAX_ATTEMPTS) {
          stats.errors++;
          return { ok: false, reason: 'max_attempts', attempts };
        }
        await sleep(backoffFor(attempts - 1));
        continue;
      }
      if (res.ok) {
        await res.json().catch(() => ({}));
        stats.ok++;
        const settled = Date.now();
        stats.tasks.push({
          start,
          settled,
          attempts,
          execMs,
          waitedMs: settled - start - execMs,
        });
        return { ok: true, attempts };
      }
      await res.text().catch(() => '');
      stats.errors++;
      return { ok: false, reason: `status_${res.status}`, attempts };
    } catch (err) {
      execMs = Date.now() - t0;
      stats.errors++;
      return { ok: false, reason: err.name || 'network', attempts };
    }
  }
}

async function main() {
  console.log(
    `150-task batch | gateway token concurrency_limit=3 | backoff ` +
      `base=${BACKOFF_BASE_MS}ms factor=${BACKOFF_FACTOR} cap=${BACKOFF_CAP_MS}ms +${JITTER_MS}ms jitter`,
  );
  const t0 = Date.now();

  const progress = setInterval(() => {
    const s = Math.round((Date.now() - t0) / 1000);
    console.log(
      `[${s}s] done=${stats.ok}/${TOTAL_TASKS} 429s=${stats.rejected429} ` +
        `errors=${stats.errors} attempts_in_flight≈${TOTAL_TASKS - stats.tasks.length - stats.errors}`,
    );
  }, 10_000);

  const results = await Promise.all(
    Array.from({ length: TOTAL_TASKS }, (_, i) => runTask(i)),
  );
  const wallMs = Date.now() - t0;
  clearInterval(progress);

  // ---- report ----
  const failed = results.filter((r) => !r.ok);
  const times = stats.tasks.map((t) => t.settled - t.start).sort((a, b) => a - b);
  const execs = stats.tasks.map((t) => t.execMs).sort((a, b) => a - b);
  const waits = stats.tasks.map((t) => t.waitedMs).sort((a, b) => a - b);
  const pct = (arr, p) => arr[Math.min(arr.length - 1, Math.floor((arr.length * p) / 100))];

  console.log('\n========== RESULT ==========');
  console.log(`total wall time      : ${(wallMs / 1000).toFixed(1)}s`);
  console.log(`completed            : ${stats.ok}/${TOTAL_TASKS} (failed ${failed.length})`);
  console.log(`429s received        : ${stats.rejected429}`);
  console.log(`throughput           : ${(stats.ok / (wallMs / 1000)).toFixed(2)} tasks/s`);
  console.log(
    `queue wait  p50/p95   : ${pct(waits, 50)}ms / ${pct(waits, 95)}ms (max ${waits[waits.length - 1]}ms)`,
  );
  console.log(
    `exec latency p50/p95  : ${pct(execs, 50)}ms / ${pct(execs, 95)}ms`,
  );
  console.log(
    `total per task p50/p95: ${pct(times, 50)}ms / ${pct(times, 95)}ms (max ${times[times.length - 1]}ms)`,
  );
  const attemptsDist = {};
  for (const t of stats.tasks) attemptsDist[t.attempts] = (attemptsDist[t.attempts] || 0) + 1;
  console.log(
    `attempts distribution : ${Object.entries(attemptsDist).sort((a, b) => a[0] - b[0]).map(([k, v]) => `${k}x:${v}`).join(' ')}`,
  );
  console.log('429s by attempt #     :');
  stats.byAttempt.forEach((n, i) => {
    if (n > 0) console.log(`  attempt ${i + 1}: ${n}`);
  });
  if (failed.length) {
    console.log('failures:', JSON.stringify(failed.slice(0, 5)));
  }
  console.log('============================');
}

main().catch((e) => {
  console.error('Fatal:', e);
  process.exit(1);
});
