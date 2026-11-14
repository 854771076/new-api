/**
 * Concurrency Control Test — Local new-api instance
 *
 * Target: localhost:3001 (local build with Redis concurrency limiter)
 * Token concurrency_limit = 5
 *
 * Tests:
 * 1. Sequential baseline
 * 2. Burst below limit (3 concurrent → all pass)
 * 3. Burst at limit (5 concurrent → all pass)
 * 4. Burst above limit (10 concurrent → 5 pass, 5 get 429)
 * 5. Recovery after burst
 * 6. Streaming concurrency
 * 7. Sustained load
 */

const BASE_URL = 'http://localhost:3001';
// 被测网关的测试 token 从环境变量读取；见 README「凭据声明」。
const API_KEY = process.env.TEST_API_KEY || '';
const MODEL = 'Qwen3.6-35B';
const LIMIT = 5;

// Different prompts of varying length and content
const PROMPTS = [
  // Short
  { label: '短-数学', content: '1+1等于几？只回答数字。', max_tokens: 20 },
  { label: '短-翻译', content: 'Translate "hello" to Chinese. One word only.', max_tokens: 20 },
  { label: '短-常识', content: '水的化学式是什么？只写化学式。', max_tokens: 20 },
  // Medium
  { label: '中-解释', content: '用三句话解释什么是量子纠缠。要求通俗易懂，每句话不超过20个字。', max_tokens: 150 },
  { label: '中-代码', content: 'Write a Python function that checks if a number is prime. Keep it under 10 lines. Only code, no explanation.', max_tokens: 200 },
  { label: '中-故事', content: '用50个字以内写一个关于机器人学会做梦的微型科幻故事。', max_tokens: 150 },
  // Longer
  { label: '长-分析', content: '请从经济、社会、技术三个角度分析人工智能对未来就业市场的影响。每个角度用一个段落，总共不超过150字。', max_tokens: 400 },
  { label: '长-创意', content: 'Describe a day in the life of a cat who secretly runs a corporation. Write exactly 5 sentences. Be creative and humorous.', max_tokens: 400 },
  { label: '长-对比', content: 'Compare Go and Rust programming languages in terms of performance, learning curve, and ecosystem maturity. Use bullet points. Max 100 words total.', max_tokens: 400 },
];

function timestamp() { return new Date().toISOString().slice(11, 23); }

async function sendRequest(prompt, stream = false) {
  const t0 = Date.now();
  try {
    const res = await fetch(`${BASE_URL}/v1/chat/completions`, {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${API_KEY}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        model: MODEL,
        messages: [{ role: 'user', content: prompt.content }],
        max_tokens: prompt.max_tokens,
        stream,
      }),
      signal: AbortSignal.timeout(60_000),
    });
    const elapsed = Date.now() - t0;

    if (res.status === 429) {
      const retryAfter = res.headers.get('Retry-After');
      const body = await res.json().catch(() => ({}));
      return { status: 429, elapsed, retryAfter, error: body?.error };
    }

    if (stream) {
      const reader = res.body?.getReader();
      let chunks = 0;
      if (reader) {
        try {
          while (true) {
            const { done } = await reader.read();
            if (done) break;
            chunks++;
          }
        } catch { /* stream closed */ }
      }
      return { status: res.status, elapsed, chunks, stream: true };
    }

    if (!res.ok) {
      const text = await res.text().catch(() => '');
      return { status: res.status, elapsed, error: text.slice(0, 200) };
    }

    const data = await res.json();
    const usage = data?.usage || {};
    return {
      status: 200,
      elapsed,
      prompt_tokens: usage.prompt_tokens,
      completion_tokens: usage.completion_tokens,
      total_tokens: usage.total_tokens,
    };
  } catch (err) {
    return { status: -1, elapsed: Date.now() - t0, error: err.message };
  }
}

function printResult(idx, label, result) {
  const tag = result.status === 200 ? '✅' : result.status === 429 ? '🚫' : '❌';
  const detail = result.status === 200
    ? `${result.total_tokens}tok ${result.elapsed}ms${result.stream ? ` ${result.chunks}chunks` : ''}`
    : result.status === 429
      ? `429 Retry-After=${result.retryAfter}s ${JSON.stringify(result.error)}`
      : `status=${result.status} ${result.error}`;
  console.log(`  ${tag} [${String(idx).padStart(2)}] ${label.padEnd(10)} ${detail}`);
}

function summarize(results) {
  const ok = results.filter(r => r.status === 200).length;
  const rateLimited = results.filter(r => r.status === 429).length;
  const errors = results.filter(r => r.status !== 200 && r.status !== 429).length;
  const okLatencies = results.filter(r => r.status === 200).map(r => r.elapsed);
  const avgLatency = okLatencies.length ? Math.round(okLatencies.reduce((s, l) => s + l, 0) / okLatencies.length) : 0;
  return { ok, rateLimited, errors, avgLatency };
}

// ── Tests ──

async function test01_sequential() {
  console.log('\n═══ Test 01: Sequential Baseline (3 requests, limit=5) ═══');
  const results = [];
  for (let i = 0; i < 3; i++) {
    const prompt = PROMPTS[i];
    const r = await sendRequest(prompt);
    results.push(r);
    printResult(i, prompt.label, r);
  }
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

async function test02_burst(concurrency) {
  console.log(`\n═══ Test 02: Burst ${concurrency} concurrent (limit=${LIMIT}) ═══`);
  const promises = [];
  for (let i = 0; i < concurrency; i++) {
    const prompt = PROMPTS[i % PROMPTS.length];
    promises.push(sendRequest(prompt).then(r => ({ idx: i, label: prompt.label, ...r })));
  }
  const results = await Promise.all(promises);
  results.sort((a, b) => a.idx - b.idx);
  for (const r of results) printResult(r.idx, r.label, r);
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

async function test03_recovery() {
  console.log('\n═══ Test 03: Recovery After Burst (sequential 3) ═══');
  await sleep(2000);
  const results = [];
  for (let i = 0; i < 3; i++) {
    const prompt = PROMPTS[(i + 3) % PROMPTS.length];
    const r = await sendRequest(prompt);
    results.push(r);
    printResult(i, prompt.label, r);
    await sleep(500);
  }
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

async function test04_streaming(concurrency) {
  console.log(`\n═══ Test 04: Streaming ${concurrency} concurrent (limit=${LIMIT}) ═══`);
  const promises = [];
  for (let i = 0; i < concurrency; i++) {
    const prompt = PROMPTS[(i + 4) % PROMPTS.length];
    promises.push(sendRequest(prompt, true).then(r => ({ idx: i, label: prompt.label, ...r })));
  }
  const results = await Promise.all(promises);
  results.sort((a, b) => a.idx - b.idx);
  for (const r of results) printResult(r.idx, r.label, r);
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

async function test05_sustained(rps, durationSec) {
  console.log(`\n═══ Test 05: Sustained ${rps} req/s for ${durationSec}s (limit=${LIMIT}) ═══`);
  const results = [];
  const interval = 1000 / rps;
  const totalRequests = rps * durationSec;

  for (let i = 0; i < totalRequests; i++) {
    const prompt = PROMPTS[i % PROMPTS.length];
    sendRequest(prompt).then(r => results.push({ idx: i, label: prompt.label, ...r }));
    if (i < totalRequests - 1) await sleep(interval);
  }
  await sleep(durationSec * 1000 + 15000);

  results.sort((a, b) => a.idx - b.idx);
  const s = summarize(results);
  console.log(`  Total sent: ${totalRequests}, received: ${results.length}`);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

// ── Main ──

async function main() {
  console.log('╔══════════════════════════════════════════════════════╗');
  console.log('║   Local New API — Concurrency Control Test          ║');
  console.log('║   ' + timestamp().padEnd(50) + '║');
  console.log('╚══════════════════════════════════════════════════════╝');
  console.log(`Target: ${BASE_URL}`);
  console.log(`Model: ${MODEL}`);
  console.log(`Token concurrency_limit: ${LIMIT}`);
  console.log(`Prompts: ${PROMPTS.length} (short/medium/long)`);

  // T1: Baseline
  const t1 = await test01_sequential();
  if (t1.ok === 0) {
    console.log('\n⛔ Baseline failed — aborting.');
    return;
  }

  // T2: Escalating bursts
  for (const n of [3, 5, 8, 10, 15]) {
    await test02_burst(n);
    await sleep(3000); // cool down
  }

  // T3: Recovery
  await test03_recovery();

  // T4: Streaming
  await test04_streaming(8);

  // T5: Sustained
  await test05_sustained(3, 10);

  console.log('\n═══ ALL TESTS COMPLETE ═══');
}

main().catch(err => {
  console.error('Fatal:', err);
  process.exit(1);
});
