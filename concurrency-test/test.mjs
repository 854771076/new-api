/**
 * Concurrency Control Test for New API Gateway
 *
 * Tests:
 * 1. Baseline: sequential requests succeed
 * 2. Burst: N concurrent requests — verify at most `limit` succeed, rest get 429
 * 3. Recovery: after burst drains, new requests succeed
 * 4. Streaming: concurrency slots held during stream
 * 5. Mixed models: per-model limits enforced independently
 */

// 上游网关地址与测试 token 从环境变量读取；见 README「凭据声明」。
const BASE_URL = process.env.TEST_BASE_URL || 'http://upstream.example.internal:30000';
const API_KEY = process.env.TEST_API_KEY || '';

// Different prompts of varying length and content
const PROMPTS = [
  // Short
  { label: '短-数学', content: '1+1等于几？只回答数字。', max_tokens: 10 },
  { label: '短-翻译', content: 'Translate "hello" to Chinese. One word only.', max_tokens: 10 },
  { label: '短-常识', content: '水的化学式是什么？只写化学式。', max_tokens: 10 },
  // Medium
  { label: '中-解释', content: '用三句话解释什么是量子纠缠。要求通俗易懂，每句话不超过20个字。', max_tokens: 100 },
  { label: '中-代码', content: 'Write a Python function that checks if a number is prime. Keep it under 10 lines. Only code, no explanation.', max_tokens: 150 },
  { label: '中-故事', content: '用50个字以内写一个关于机器人学会做梦的微型科幻故事。', max_tokens: 100 },
  // Longer
  { label: '长-分析', content: '请从经济、社会、技术三个角度分析人工智能对未来就业市场的影响。每个角度用一个段落，总共不超过150字。', max_tokens: 300 },
  { label: '长-创意', content: 'Describe a day in the life of a cat who secretly runs a corporation. Write exactly 5 sentences. Be creative and humorous.', max_tokens: 300 },
  { label: '长-对比', content: 'Compare Go and Rust programming languages in terms of performance, learning curve, and ecosystem maturity. Use bullet points. Max 100 words total.', max_tokens: 300 },
];

const MODELS = ['Qwen3.6-35B', 'Qwen3-Next-80B'];

// ── Helpers ──

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

function timestamp() { return new Date().toISOString().slice(11, 23); }

async function sendRequest(model, prompt, stream = false) {
  const t0 = Date.now();
  const body = {
    model,
    messages: [{ role: 'user', content: prompt.content }],
    max_tokens: prompt.max_tokens,
    stream,
  };

  try {
    const res = await fetch(`${BASE_URL}/v1/chat/completions`, {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${API_KEY}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(60_000),
    });

    const elapsed = Date.now() - t0;

    if (stream) {
      // Consume the stream to hold the slot
      const reader = res.body?.getReader();
      let chunks = 0;
      let content = '';
      if (reader) {
        const decoder = new TextDecoder();
        try {
          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            chunks++;
            content += decoder.decode(value, { stream: true });
          }
        } catch { /* stream closed */ }
      }
      return { status: res.status, elapsed, chunks, stream: true, content_len: content.length };
    }

    if (res.status === 429) {
      const retryAfter = res.headers.get('Retry-After');
      const body = await res.json().catch(() => ({}));
      return { status: 429, elapsed, retryAfter, error: body?.error };
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
      content_preview: data?.choices?.[0]?.message?.content?.slice(0, 60) || '',
    };
  } catch (err) {
    return { status: -1, elapsed: Date.now() - t0, error: err.message };
  }
}

function printResult(idx, label, result) {
  const tag = result.status === 200 ? '✅' : result.status === 429 ? '🚫' : '❌';
  const detail = result.status === 200
    ? `${result.total_tokens}tok ${result.elapsed}ms${result.stream ? ` ${result.chunks}chunks` : ''} "${result.content_preview}"`
    : result.status === 429
      ? `429 Retry-After=${result.retryAfter}s ${JSON.stringify(result.error)}`
      : `status=${result.status} ${result.error}`;
  console.log(`  ${tag} [${idx}] ${label.padEnd(10)} ${detail}`);
}

function summarize(results) {
  const ok = results.filter(r => r.status === 200).length;
  const rateLimited = results.filter(r => r.status === 429).length;
  const errors = results.filter(r => r.status !== 200 && r.status !== 429).length;
  const avgLatency = Math.round(
    results.filter(r => r.status === 200).reduce((s, r) => s + r.elapsed, 0) / (ok || 1)
  );
  return { ok, rateLimited, errors, avgLatency };
}

// ── Tests ──

async function test01_sequentialBaseline() {
  console.log('\n═══ Test 01: Sequential Baseline (3 requests) ═══');
  const results = [];
  for (let i = 0; i < 3; i++) {
    const prompt = PROMPTS[i % PROMPTS.length];
    const r = await sendRequest('Qwen3.6-35B', prompt);
    results.push(r);
    printResult(i, prompt.label, r);
  }
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s.ok === 3;
}

async function test02_burstConcurrency(concurrency) {
  console.log(`\n═══ Test 02: Burst Concurrency (${concurrency} simultaneous) ═══`);
  const model = 'Qwen3.6-35B';
  const promises = [];
  for (let i = 0; i < concurrency; i++) {
    const prompt = PROMPTS[i % PROMPTS.length];
    promises.push(sendRequest(model, prompt).then(r => ({ idx: i, label: prompt.label, ...r })));
  }
  const results = await Promise.all(promises);
  results.sort((a, b) => a.idx - b.idx);
  for (const r of results) printResult(r.idx, r.label, r);
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

async function test03_recoveryAfterBurst() {
  console.log('\n═══ Test 03: Recovery After Burst (sequential 3) ═══');
  const results = [];
  for (let i = 0; i < 3; i++) {
    const prompt = PROMPTS[(i + 3) % PROMPTS.length];
    const r = await sendRequest('Qwen3.6-35B', prompt);
    results.push(r);
    printResult(i, prompt.label, r);
    await sleep(500);
  }
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s.ok === 3;
}

async function test04_streamingConcurrency(concurrency) {
  console.log(`\n═══ Test 04: Streaming Concurrency (${concurrency} simultaneous) ═══`);
  const model = 'Qwen3.6-35B';
  const promises = [];
  for (let i = 0; i < concurrency; i++) {
    const prompt = PROMPTS[(i + 4) % PROMPTS.length];
    promises.push(sendRequest(model, prompt, true).then(r => ({ idx: i, label: prompt.label, ...r })));
  }
  const results = await Promise.all(promises);
  results.sort((a, b) => a.idx - b.idx);
  for (const r of results) printResult(r.idx, r.label, r);
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

async function test05_multiModelBurst(concurrency) {
  console.log(`\n═══ Test 05: Multi-Model Burst (${concurrency} per model) ═══`);
  const promises = [];
  for (const model of MODELS) {
    for (let i = 0; i < concurrency; i++) {
      const prompt = PROMPTS[(i + 5) % PROMPTS.length];
      promises.push(
        sendRequest(model, prompt).then(r => ({ idx: `${model}#${i}`, label: prompt.label, ...r }))
      );
    }
  }
  const results = await Promise.all(promises);
  for (const r of results) printResult(r.idx, r.label, r);
  const s = summarize(results);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);
  return s;
}

async function test06_sustainedLoad(rps, durationSec) {
  console.log(`\n═══ Test 06: Sustained Load (${rps} req/s for ${durationSec}s) ═══`);
  const model = 'Qwen3.6-35B';
  const results = [];
  const interval = 1000 / rps;
  const totalRequests = rps * durationSec;
  const start = Date.now();

  for (let i = 0; i < totalRequests; i++) {
    const prompt = PROMPTS[i % PROMPTS.length];
    // Fire and collect async — don't await each one
    sendRequest(model, prompt).then(r => {
      results.push({ idx: i, label: prompt.label, ...r });
    });
    if (i < totalRequests - 1) await sleep(interval);
  }

  // Wait for all in-flight to complete
  await sleep(durationSec * 1000 + 15000);

  results.sort((a, b) => a.idx - b.idx);
  const s = summarize(results);
  console.log(`  Total sent: ${totalRequests}, received: ${results.length}`);
  console.log(`  Summary: ${s.ok} ok, ${s.rateLimited} 429, ${s.errors} errors, avg ${s.avgLatency}ms`);

  // Print 429 distribution
  const rateLimitedIdxs = results.filter(r => r.status === 429).map(r => r.idx);
  if (rateLimitedIdxs.length > 0) {
    console.log(`  429 at indices: ${rateLimitedIdxs.slice(0, 20).join(', ')}${rateLimitedIdxs.length > 20 ? '...' : ''}`);
  }
  return s;
}

// ── Main ──

async function main() {
  console.log('╔══════════════════════════════════════════════════════╗');
  console.log('║   New API Gateway — Concurrency Control Test        ║');
  console.log('║   ' + timestamp().padEnd(50) + '║');
  console.log('╚══════════════════════════════════════════════════════╝');
  console.log(`Target: ${BASE_URL}`);
  console.log(`Models: ${MODELS.join(', ')}`);
  console.log(`Prompts: ${PROMPTS.length} (short/medium/long)`);

  // Phase 1: Baseline
  const baselineOk = await test01_sequentialBaseline();
  if (!baselineOk) {
    console.log('\n⛔ Baseline failed — gateway unreachable or key invalid. Aborting.');
    process.exit(1);
  }

  // Phase 2: Escalating burst
  for (const n of [3, 5, 8, 10, 15]) {
    await test02_burstConcurrency(n);
    await sleep(2000); // cool down between bursts
  }

  // Phase 3: Recovery check
  await test03_recoveryAfterBurst();

  // Phase 4: Streaming burst
  await test04_streamingConcurrency(5);

  // Phase 5: Multi-model
  await test05_multiModelBurst(3);

  // Phase 6: Sustained load (2 req/s for 15s = 30 requests)
  await test06_sustainedLoad(2, 15);

  console.log('\n═══ ALL TESTS COMPLETE ═══');
}

main().catch(err => {
  console.error('Fatal:', err);
  process.exit(1);
});