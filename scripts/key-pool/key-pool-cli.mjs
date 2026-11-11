#!/usr/bin/env node
/**
 * key-pool-cli.mjs — four-strategy multi-key load driver (RTE-04 / D-R3).
 *
 * Evolution lineage: concurrency-test/multi-key-test.mjs -> this file. The
 * three baseline strategies (rr / least / weighted) are ported verbatim so
 * historical REPORT baselines stay reproducible; `feedback` (default) wires
 * the full key-pool feedback loop (EWMA reweighting, instant 429/timeout
 * demote, ramp-limited recovery) from ./key-pool.mjs.
 *
 * env contract (keys never appear in code, output or args — env only):
 *   KEY_POOL    required, JSON array [{"name","key","weight"}, ...]
 *   MODEL       required
 *   BASE_URL    default http://localhost:3001
 *   MAX_ATTEMPTS default 5 (bounded: a fully-429 pool must not self-spin)
 *   PERIOD_MS   default 10000 (feedback recompute gate)
 * usage: node scripts/key-pool/key-pool-cli.mjs [feedback|weighted|least|rr] [tasks]
 *
 * Zero-dependency constraint: node: builtins + relative imports only.
 * Node >= 18 baseline (AbortSignal.timeout is the newest API used — no
 * Array.fromAsync or later additions).
 */

import { pathToFileURL } from 'node:url';
import {
  createKeyPool,
  parseKeyPoolEnv,
  SUCCESS,
  REJECTED_429,
  TIMEOUT,
  ERROR,
} from './key-pool.mjs';

export const STRATEGIES = ['feedback', 'weighted', 'least', 'rr'];
export const DEFAULT_TASKS = 150;
export const DEFAULT_MAX_ATTEMPTS = 5;
export const DEFAULT_PERIOD_MS = 10_000;
export const DEFAULT_BASE_URL = 'http://localhost:3001';
export const REQUEST_TIMEOUT_MS = 60_000;

/** Map a completed fetch to the pool outcome vocabulary (pure, unit-tested). */
export function classifyOutcome({ status, aborted }) {
  if (aborted) return TIMEOUT;
  if (status >= 200 && status < 300) return SUCCESS;
  if (status === 429) return REJECTED_429;
  return ERROR;
}

/**
 * Build the weight pool for a strategy. `weighted` and `feedback` share the
 * same createKeyPool machinery — weighted freezes recompute (periodMs=Infinity)
 * and never records outcomes, degenerating to the static smooth-WRR; rr/least
 * do not use the pool (they return null) and are driven by the CLI-local
 * cursors below.
 */
export function createStrategyPool(strategy, entries, opts = {}) {
  if (strategy === 'feedback') {
    return createKeyPool(entries, {
      now: opts.now,
      periodMs: opts.periodMs ?? DEFAULT_PERIOD_MS,
    });
  }
  if (strategy === 'weighted') {
    return createKeyPool(entries, { now: opts.now, periodMs: Infinity });
  }
  return null;
}

const PROMPTS = [
  { content: '用三段话解释量子纠缠：物理现象、爱因斯坦的反对、量子计算用途。每段不超过50字。', max_tokens: 400 },
  { content: 'Write a production-quality Python binary search function with type hints, docstring, and empty-list handling. Only the function.', max_tokens: 400 },
  { content: '写一个关于机器人学会做梦的微型科幻故事，250字左右，有起承转合，结尾留悬念。', max_tokens: 400 },
  { content: 'Compare HTTP/1.1, HTTP/2, HTTP/3 in three bullet points: multiplexing, head-of-line blocking, transport. Max 80 words.', max_tokens: 400 },
  { content: '从成本、性能、生态三个角度分析中小团队为何倾向自建推理服务，每点一句话。', max_tokens: 400 },
  { content: 'Describe a day in the life of a cat who secretly runs a corporation. Exactly 4 sentences, humorous but professional.', max_tokens: 400 },
];

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// multi-key backoff: 250ms x 1.3^n capped at 3s plus jitter (runtime only).
function backoff(attempt) {
  const ms = Math.min(250 * Math.pow(1.3, attempt), 3000);
  return Math.round(ms + Math.random() * 200);
}

async function main() {
  const strategy = process.argv[2] || 'feedback';
  if (!STRATEGIES.includes(strategy)) {
    console.error(`strategy must be ${STRATEGIES.join(' | ')}`);
    process.exit(1);
  }
  const tasks = Number(process.argv[3] || DEFAULT_TASKS);

  let entries;
  try {
    entries = parseKeyPoolEnv(process.env.KEY_POOL);
  } catch (e) {
    console.error(e.message);
    process.exit(1);
  }
  const MODEL = process.env.MODEL;
  if (!MODEL) {
    console.error('MODEL env is required (the model name to request)');
    process.exit(1);
  }
  const BASE_URL = process.env.BASE_URL || DEFAULT_BASE_URL;
  const MAX_ATTEMPTS = Number(process.env.MAX_ATTEMPTS || DEFAULT_MAX_ATTEMPTS);
  const PERIOD_MS = Number(process.env.PERIOD_MS || DEFAULT_PERIOD_MS);

  const pool = createStrategyPool(strategy, entries, { periodMs: PERIOD_MS });
  const stats = new Map(entries.map((e) => [e.name, { sent: 0, ok: 0, rejected429: 0, timeouts: 0, errors: 0 }]));
  const inFlight = new Map(entries.map((e) => [e.name, 0]));
  let rrCursor = 0;

  function pickEntry() {
    if (strategy === 'rr') {
      return entries[rrCursor++ % entries.length];
    }
    if (strategy === 'least') {
      let best = 0;
      for (let i = 1; i < entries.length; i++) {
        if (inFlight.get(entries[i].name) < inFlight.get(entries[best].name)) best = i;
      }
      return entries[best];
    }
    return pool.pick(); // feedback + weighted (wrAccum over effective/static weight)
  }

  async function runTask(taskIdx) {
    const prompt = PROMPTS[taskIdx % PROMPTS.length];
    for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
      const entry = pickEntry();
      const s = stats.get(entry.name);
      s.sent++;
      inFlight.set(entry.name, inFlight.get(entry.name) + 1);
      if (strategy === 'feedback') pool.begin(entry.name);

      const t0 = Date.now();
      let outcome;
      try {
        const res = await fetch(`${BASE_URL}/v1/chat/completions`, {
          method: 'POST',
          headers: { Authorization: `Bearer ${entry.key}`, 'Content-Type': 'application/json' },
          body: JSON.stringify({
            model: MODEL,
            messages: [{ role: 'user', content: prompt.content }],
            max_tokens: prompt.max_tokens,
            stream: false,
          }),
          signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
        });
        await res.text().catch(() => {});
        outcome = classifyOutcome({ status: res.status, aborted: false });
      } catch (e) {
        const timedOut = e.name === 'TimeoutError' || e.name === 'AbortError';
        outcome = classifyOutcome({ status: 0, aborted: timedOut });
      }

      const latencyMs = outcome === TIMEOUT ? REQUEST_TIMEOUT_MS : Date.now() - t0;
      inFlight.set(entry.name, inFlight.get(entry.name) - 1);
      if (strategy === 'feedback') {
        pool.end(entry.name);
        pool.recordOutcome(entry.name, outcome, latencyMs);
      }

      if (outcome === SUCCESS) {
        s.ok++;
        return;
      }
      if (outcome === ERROR) {
        s.errors++; // 5xx/other: task fails, no retry (multi-key semantics)
        return;
      }
      // REJECTED_429 / TIMEOUT: demote already applied by the pool (feedback);
      // back off briefly and re-route via the strategy.
      if (outcome === REJECTED_429) s.rejected429++;
      else s.timeouts++;
      if (attempt >= MAX_ATTEMPTS) {
        s.errors++; // bounded: attempts exhausted, count as failed task
        return;
      }
      await sleep(backoff(attempt));
    }
  }

  console.log(
    `strategy=${strategy} tasks=${tasks} weights=[${entries.map((e) => e.weight).join(',')}] aggregate=${entries.reduce((sum, e) => sum + e.weight, 0)}`,
  );
  const t0 = Date.now();
  await Promise.all(Array.from({ length: tasks }, (_, i) => runTask(i)));
  const wall = Date.now() - t0;

  const totalOk = [...stats.values()].reduce((sum, s) => sum + s.ok, 0);
  const total429 = [...stats.values()].reduce((sum, s) => sum + s.rejected429, 0);
  const totalErrors = [...stats.values()].reduce((sum, s) => sum + s.errors, 0);

  console.log('\n========== RESULT ==========');
  console.log(`wall time        : ${(wall / 1000).toFixed(1)}s`);
  console.log(`completed        : ${totalOk}/${tasks} (errors ${totalErrors})`);
  console.log(`total 429s       : ${total429}`);
  console.log(`throughput       : ${(wall > 0 ? totalOk / (wall / 1000) : 0).toFixed(2)} tasks/s`);
  console.log('per-key breakdown:');
  const okValues = entries.map((e) => stats.get(e.name).ok);
  const mean = okValues.reduce((sum, v) => sum + v, 0) / okValues.length;
  const sd = Math.sqrt(okValues.reduce((sum, v) => sum + (v - mean) ** 2, 0) / okValues.length);
  const statusRows = pool ? new Map(pool.status().map((row) => [row.name, row])) : null;
  for (const e of entries) {
    const s = stats.get(e.name);
    const bar = '#'.repeat(Math.round(s.ok / 3));
    const weightLabel = strategy === 'feedback' || strategy === 'weighted'
      ? `w=${e.weight}`
      : `limit=${e.weight}`;
    console.log(
      `  ${e.name} ${weightLabel} ok=${String(s.ok).padStart(3)} sent=${String(s.sent).padStart(3)} 429=${String(s.rejected429).padStart(4)} timeout=${String(s.timeouts).padStart(3)} err=${String(s.errors).padStart(3)} ${bar}`,
    );
  }
  console.log(`ok distribution  : mean=${mean.toFixed(1)} stddev=${sd.toFixed(1)} (lower = fairer)`);
  if (strategy === 'weighted') {
    const totalWeight = entries.reduce((sum, e) => sum + e.weight, 0);
    const expected = entries.map((e) => ((e.weight / totalWeight) * totalOk).toFixed(1));
    console.log(`proportional fit : expected ok = [${expected.join(', ')}]`);
  }
  if (statusRows) {
    // feedback/weighted: terminal multiplier + effective weight observability.
    for (const e of entries) {
      const row = statusRows.get(e.name);
      console.log(
        `  final weight    : ${e.name} multiplier=${row.multiplier.toFixed(4)} effectiveWeight=${row.effectiveWeight.toFixed(4)}`,
      );
    }
  }
  console.log('============================');
}

const isDirectRun =
  process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href;
if (isDirectRun) {
  main().catch((e) => {
    console.error('Fatal:', e);
    process.exit(1);
  });
}
