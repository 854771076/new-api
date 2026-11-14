/**
 * Sustained load generator for the local gateway (localhost:3001).
 *
 * Goal: hold enough in-flight requests for long enough that the /concurrency
 * monitoring page (3s refresh) can capture non-zero buckets, show utilisation
 * on capped scopes, and register 429 rejections when over the token limit.
 *
 * Token concurrency_limit = 5.
 */

const BASE_URL = 'http://localhost:3001';
// 被测网关的测试 token 从环境变量读取；见 README「凭据声明」。
const API_KEY = process.env.TEST_API_KEY || '';
const MODEL = 'Qwen3.6-35B';
const LIMIT = 5;

const DURATION_MS = Number(process.env.LOAD_DURATION_MS || 45_000);
const BURST_SIZE = Number(process.env.LOAD_BURST || 10); // > limit to force 429s
const BURST_INTERVAL_MS = Number(process.env.LOAD_INTERVAL_MS || 800);

// Long prompts + high max_tokens so requests stay in-flight long enough for
// the 3s-refresh monitoring page to observe them.
const PROMPTS = [
  {
    label: '长-分析',
    content:
      '请从经济、社会、技术三个角度详细分析人工智能对未来就业市场的影响。每个角度写一个完整段落，包含具体行业案例和数据趋势预测。',
    max_tokens: 800,
  },
  {
    label: '长-创意',
    content:
      'Describe a full day in the life of a cat who secretly runs a corporation. Include morning meetings, lunch negotiations, and an evening board review. Be creative and humorous.',
    max_tokens: 800,
  },
  {
    label: '长-对比',
    content:
      'Compare Go and Rust programming languages comprehensively: performance characteristics, learning curve, ecosystem maturity, concurrency models, and ideal use cases. Use detailed bullet points.',
    max_tokens: 800,
  },
  {
    label: '中-代码',
    content:
      'Write a production-quality Python rate limiter class with token bucket algorithm, supporting async usage, configurable burst, and thread safety. Include docstrings.',
    max_tokens: 600,
  },
  {
    label: '中-故事',
    content: '写一个关于机器人学会做梦的微型科幻故事，300字左右，有起承转合。',
    max_tokens: 500,
  },
];

let stats = { sent: 0, ok: 0, limited: 0, errors: 0, inFlight: 0 };
const t0 = Date.now();

async function sendOne(i) {
  const prompt = PROMPTS[i % PROMPTS.length];
  stats.sent++;
  stats.inFlight++;
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
    if (res.status === 429) {
      stats.limited++;
      await res.text().catch(() => '');
    } else if (res.ok) {
      stats.ok++;
      await res.json().catch(() => ({}));
    } else {
      stats.errors++;
      await res.text().catch(() => '');
    }
  } catch {
    stats.errors++;
  } finally {
    stats.inFlight--;
  }
}

function report() {
  const elapsed = ((Date.now() - t0) / 1000).toFixed(0);
  console.log(
    `[${elapsed}s] sent=${stats.sent} ok=${stats.ok} 429=${stats.limited} err=${stats.errors} inFlight=${stats.inFlight}`,
  );
}

async function main() {
  console.log(
    `Load test: ${BURST_SIZE} req every ${BURST_INTERVAL_MS}ms for ${DURATION_MS}ms (limit=${LIMIT})`,
  );
  const reporter = setInterval(report, 3000);
  const end = Date.now() + DURATION_MS;
  let i = 0;

  while (Date.now() < end) {
    const batch = [];
    for (let j = 0; j < BURST_SIZE; j++) {
      batch.push(sendOne(i++));
    }
    await Promise.allSettled(batch);
    // Small gap between bursts so buckets drain and refill visibly
    if (Date.now() < end) {
      await new Promise((r) => setTimeout(r, BURST_INTERVAL_MS));
    }
  }

  // Drain in-flight
  const drainStart = Date.now();
  while (stats.inFlight > 0 && Date.now() - drainStart < 30_000) {
    await new Promise((r) => setTimeout(r, 500));
  }

  clearInterval(reporter);
  report();
  console.log('LOAD TEST COMPLETE');
}

main().catch((e) => {
  console.error('Fatal:', e);
  process.exit(1);
});
