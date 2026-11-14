// hold-buckets.mjs — fires N concurrent long generations so /concurrency
// scope buckets stay populated long enough for a UI-state snapshot/screenshot.
// usage (repo root): node concurrency-test/hold-buckets.mjs
const BASE = 'http://127.0.0.1:3001';
const MODEL = 'Qwen3.6-35B';
// 被测网关的测试 token 从环境变量读取；见 README「凭据声明」。
const KEY = process.env.TEST_API_KEY || '';
const N = Number(process.env.HOLD_N || 3);
const MAXT = Number(process.env.HOLD_MAX_TOKENS || 2000);

async function chat(tag, maxTokens) {
  const t0 = Date.now();
  const r = await fetch(`${BASE}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY}`, 'New-API-User': '1' },
    body: JSON.stringify({
      model: MODEL, stream: false, max_tokens: maxTokens,
      messages: [{ role: 'user', content: tag }],
    }),
  });
  return { status: r.status, ms: Date.now() - t0 };
}

const prompt = '请写一篇1500字的关于分布式系统一致性的科普文章,分五个章节。';
console.log(`HELD firing ${N} long tasks (max_tokens=${MAXT}) at ${new Date().toISOString()}`);
const results = await Promise.all(Array.from({ length: N }, (_, i) => chat(prompt + ` #${i}`, MAXT)));
console.log('DONE', JSON.stringify(results));
