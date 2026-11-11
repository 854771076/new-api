/**
 * key-pool.test.mjs — RTE-04 client key-pool feedback-loop test suite.
 *
 * Source: 02-token phase plan 02-19 (D-R3 locked decision: per-key EWMA
 * success+latency -> effective weight = staticWeight x success decay x
 * latency penalty, periodic recompute; 429/timeout instant demote,
 * recovery ramp-limited). Zero-dependency constraint: only node: builtins
 * and relative imports (node:test + node:assert/strict, Node >= 18).
 * Fully deterministic: injected virtual clock, scripted outcomes,
 * no real sleeps, no network, no randomness.
 */

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { createKeyPool, parseKeyPoolEnv, SUCCESS, REJECTED_429, TIMEOUT, ERROR } from './key-pool.mjs';
import { classifyOutcome, createStrategyPool } from './key-pool-cli.mjs';

const EPS = 1e-9;
const close = (actual, expected, label) =>
  assert.ok(
    Math.abs(actual - expected) < EPS,
    `${label}: expected ${expected} (±1e-9), got ${actual}`,
  );

// ---------------------------------------------------------------------------
// Formula constants (hand-computed closed forms, pinned to ±1e-9):
//   alpha_success(dt=60s) = 1 - 2^(-60/300) = 1 - 2^(-0.2)
//   alpha_latency(dt=60s) = 1 - 2^(-60/60)  = 0.5
//   EWMA after k failure samples at 60s steps from seed 1.0 = 2^(-0.2k)
// ---------------------------------------------------------------------------
const ALPHA_S_60 = 0.12944943670387588; // 1 - 2^(-0.2)
const TWOPOW = {
  m02: 0.87055056329612412, // 2^(-0.2)
  m04: 0.75785828325519899, // 2^(-0.4)
  m06: 0.6597539553864471, // 2^(-0.6)
  m08: 0.57434917749851744, // 2^(-0.8)
};

// Tracer scenario constants (virtual clock; identical copy in demo-stub.mjs —
// the two carriers must stay in sync: same scenario, demo prints what the
// test asserts).
//
// Scenario rationale (numbers are demo discretion, property bounds are not):
//  - both keys are healthy-fast (100ms) so pre/post-storm latencyFactor=1 and
//    A's final target can climb back above 0.75;
//  - the storm's 429s are slow (3000ms): A's latency EWMA rises, latencyFactor
//    for B clamps at 2.0 (eB -> 4) and A's target is squeezed toward the 0.2
//    floor (successEwma 300s half-life + latency penalty) — that floor-pin is
//    what drives the storm-window share under 25%;
//  - 2400s storm gives the slow success-EWMA time to reach the pin; 700s
//    recovery lets A's final target land in (0.75, 1.0).
const SC = {
  stepMs: 500, // virtual time per request
  periodMs: 60_000, // recompute cadence (default 10s in prod; scenario seam)
  latencyOk: 100, // healthy response latency for both keys (ms)
  latency429: 3_000, // storm: A's 429 responses are slow (ms)
  stormStartMs: 320_000, // storm window start (virtual)
  stormEndMs: 2_720_000, // storm window end (2400s storm)
  totalMs: 3_420_000, // end of run (700s recovery after storm)
  weightA: 6,
  weightB: 2,
  finalSegmentMs: 100_000, // "final segment" for the share-recovery check
};

/**
 * Shared tracer driver (dual carrier with demo-stub.mjs): scripted A-storm
 * -> recovery, B always healthy. Returns measured quantities only; all
 * invariants are asserted by the caller.
 */
function runTracerScenario() {
  let t = 0;
  const pool = createKeyPool(
    [
      { name: 'A', key: 'stub-key-a', weight: SC.weightA },
      { name: 'B', key: 'stub-key-b', weight: SC.weightB },
    ],
    { now: () => t, periodMs: SC.periodMs },
  );

  let stormEnded = false;
  let lastT = 0;
  let picksInStorm = 0;
  let aPicksInStorm = 0;
  let minMultAStorm = Infinity;
  let firstTickMultAfterStorm = null;
  let finalSegmentPicks = 0;
  let finalSegmentAPicks = 0;
  let minMultB = Infinity;
  let maxIdentityErr = 0;
  let identityChecks = 0;

  const checkIdentity = () => {
    for (const row of pool.status()) {
      const err = Math.abs(row.effectiveWeight - row.staticWeight * row.multiplier);
      if (err > maxIdentityErr) maxIdentityErr = err;
      identityChecks++;
    }
  };

  for (;;) {
    if (t >= SC.totalMs) break;
    // Lazy-recompute tick gate: after the storm ends, jump the virtual clock
    // one full period past the last pick so the very next pick entry performs
    // the FIRST recompute after storm end (no picks happen inside the gap).
    if (!stormEnded && t >= SC.stormEndMs) {
      stormEnded = true;
      t = lastT + SC.periodMs + 1;
      if (t < SC.stormEndMs) t = SC.stormEndMs;
    }

    const entry = pool.pick();
    lastT = t;
    t += SC.stepMs;

    const inStorm = t >= SC.stormStartMs && t < SC.stormEndMs;
    const outcome = entry.name === 'A' && inStorm ? REJECTED_429 : SUCCESS;
    const latency = outcome === REJECTED_429 ? SC.latency429 : SC.latencyOk;
    pool.recordOutcome(entry.name, outcome, latency);

    if (inStorm) {
      picksInStorm++;
      if (entry.name === 'A') aPicksInStorm++;
    }
    if (t >= SC.totalMs - SC.finalSegmentMs) {
      finalSegmentPicks++;
      if (entry.name === 'A') finalSegmentAPicks++;
    }

    const status = pool.status();
    const rowA = status[0];
    const rowB = status[1];
    if (inStorm && rowA.multiplier < minMultAStorm) minMultAStorm = rowA.multiplier;
    if (rowB.multiplier < minMultB) minMultB = rowB.multiplier;
    if (firstTickMultAfterStorm === null && stormEnded) {
      firstTickMultAfterStorm = rowA.multiplier;
    }
    checkIdentity();
  }

  const status = pool.status();
  return {
    stormShare: aPicksInStorm / picksInStorm,
    minMultAStorm,
    firstTickMultAfterStorm,
    finalMultA: status[0].multiplier,
    finalMultB: status[1].multiplier,
    minMultB,
    finalSegmentShare: finalSegmentAPicks / finalSegmentPicks,
    maxIdentityErr,
    identityChecks,
    status,
  };
}

// Reference smooth weighted round-robin (nginx wrAccum algorithm, ported
// verbatim from concurrency-test/multi-key-test.mjs — degenerate-identity
// yardstick for zero-sample pools).
function referenceWrAccumSequence(entries, count) {
  const weights = entries.map((e) => e.weight);
  const accum = entries.map(() => 0);
  const out = [];
  for (let n = 0; n < count; n++) {
    let totalWeight = 0;
    for (const w of weights) totalWeight += w;
    let best = 0;
    for (let i = 0; i < entries.length; i++) {
      accum[i] += weights[i];
      if (accum[i] > accum[best]) best = i;
    }
    accum[best] -= totalWeight;
    out.push(entries[best].name);
  }
  return out;
}

const baseEntries = () => [
  { name: 'k-a', key: 'stub-key-a', weight: 6 },
  { name: 'k-b', key: 'stub-key-b', weight: 2 },
];
const entriesAB = () => [
  { name: 'A', key: 'stub-key-a', weight: 6 },
  { name: 'B', key: 'stub-key-b', weight: 2 },
];

describe('formula exact-value table (Δt=60s hand-computed, clock injected)', () => {
  it('pins successEwma/latencyEwma/target/multiplier per 60s step (single warm record ⇒ latencyFactor=1)', () => {
    let t = 0;
    const pool = createKeyPool(
      [
        { name: 'A', key: 'stub-key-a', weight: 6 },
        { name: 'B', key: 'stub-key-b', weight: 2 },
      ],
      { now: () => t, periodMs: 0 },
    );
    const A = () => pool.status().find((r) => r.name === 'A');

    // t=0: first sample seeds directly (successEwma:=1, latencyEwma:=100).
    t = 0;
    pool.recordOutcome('A', SUCCESS, 100);
    close(A().successEwma, 1, 'E seed');
    close(A().latencyEwma, 100, 'L seed');
    assert.equal(A().samples, 1);
    assert.equal(A().targetMultiplier, 1, 'warm gate: 1 sample ⇒ target 1');

    // t=60: failure sample, alpha_s = 1-2^-0.2, alpha_l = 0.5.
    t = 60_000;
    pool.recordOutcome('A', ERROR, 300);
    close(A().successEwma, TWOPOW.m02, 'E after 1st failure');
    close(A().latencyEwma, 200, 'L after 200/300 mix');

    t = 120_000;
    pool.recordOutcome('A', ERROR, 300);
    close(A().successEwma, TWOPOW.m04, 'E after 2nd failure');
    close(A().latencyEwma, 250, 'L step 3');

    t = 180_000;
    pool.recordOutcome('A', ERROR, 300);
    close(A().successEwma, TWOPOW.m06, 'E after 3rd failure');
    close(A().latencyEwma, 275, 'L step 4');
    assert.equal(A().samples, 4);
    assert.equal(A().targetMultiplier, 1, 'warm gate boundary: 4 samples ⇒ target 1');

    // 5th sample leaves the warm gate: target = (0.2+0.8*E) x 1 (single warm).
    t = 240_000;
    pool.recordOutcome('A', ERROR, 300);
    close(A().successEwma, TWOPOW.m08, 'E after 4th failure');
    close(A().latencyEwma, 287.5, 'L step 5');
    assert.equal(A().samples, 5);
    const target5 = 0.2 + 0.8 * TWOPOW.m08; // 0.65947934199881397
    close(A().targetMultiplier, target5, 'target at 5 warm samples');
    close(A().multiplier, target5, 'tracked snaps down to target (no demote yet)');

    // Success sample: E = 2^-1 + alpha_s exactly; ramp = min(target, prev+0.2).
    t = 300_000;
    pool.recordOutcome('A', SUCCESS, 100);
    const e6 = 0.5 + ALPHA_S_60; // 0.62944943670387588
    close(A().successEwma, e6, 'E after recovery success');
    close(A().latencyEwma, 193.75, 'L mixes back to 100');
    const target6 = 0.2 + 0.8 * e6; // 0.70355954936310083
    close(A().targetMultiplier, target6, 'target after success');
    assert.ok(target6 < target5 + 0.2, 'ramp ceiling min(target, prev+rampStep)');
    close(A().multiplier, target6, 'tracked = min(target, prev+0.2) = target here');

    // B was never sampled: cold key degrades to static (target 1, multiplier 1).
    const B = pool.status().find((r) => r.name === 'B');
    assert.equal(B.samples, 0);
    assert.equal(B.targetMultiplier, 1);
    close(B.multiplier, 1, 'cold key multiplier stays 1');
  });

  it('warm gate: 4 samples target=1.0, 5 samples leave 1.0 (explicit boundary)', () => {
    let t = 0;
    const pool = createKeyPool(entriesAB(), { now: () => t, periodMs: 0 });
    const A = () => pool.status().find((r) => r.name === 'A');
    pool.recordOutcome('A', SUCCESS, 120); // seed: successEwma=1, sample 1
    assert.equal(A().samples, 1);
    assert.equal(A().targetMultiplier, 1, 'sample 1: still warm-gated');
    for (let i = 0; i < 3; i++) {
      t += 60_000;
      pool.recordOutcome('A', ERROR, 120);
      assert.equal(A().samples, i + 2);
      assert.equal(A().targetMultiplier, 1, `sample ${i + 2}: still warm-gated`);
    }
    t += 60_000;
    pool.recordOutcome('A', ERROR, 120); // 5th sample leaves the warm gate
    assert.equal(A().samples, 5);
    assert.notEqual(A().targetMultiplier, 1, '5th sample leaves warm gate');
    // Seed 1.0 then 4 failures at 60s steps: E = 2^(-0.8)
    close(A().successEwma, TWOPOW.m08, 'E after 4 failures');
    close(A().targetMultiplier, 0.2 + 0.8 * TWOPOW.m08, 'target after 5th sample');
  });

  it('0.2 floor: success-decay x latency-penalty clamps at multiplierFloor', () => {
    let t = 0;
    const pool = createKeyPool(entriesAB(), { now: () => t, periodMs: 0 });
    const A = () => pool.status().find((r) => r.name === 'A');
    const B = () => pool.status().find((r) => r.name === 'B');
    // A slow (2000ms), B fast (100ms): both eventually warm.
    t = 0;
    pool.recordOutcome('A', SUCCESS, 2_000);
    for (let i = 0; i < 4; i++) {
      t += 60_000;
      pool.recordOutcome('A', ERROR, 2_000);
    }
    assert.equal(A().samples, 5);
    for (let i = 0; i < 5; i++) {
      t += 60_000;
      pool.recordOutcome('B', SUCCESS, 100);
    }
    assert.equal(B().samples, 5);
    // Both warm now: latencyFactor_A = ((2000+100)/2)/2000 = 0.525.
    close(A().targetMultiplier, (0.2 + 0.8 * TWOPOW.m08) * 0.525, 'A target once both warm');
    // Keep failing A until successFactor x 0.525 drops below the 0.2 floor.
    // Decay accounting: E was 2^-0.8 at A's last sample (t=240s); B's warm-up
    // advanced the clock to t=540s, so A's next sample carries dt=360s
    // (2^-1.2), then 6 more failures at 60s steps (2^-1.2 total):
    //   E = 2^-0.8 x 2^-1.2 x 2^-1.2 = 2^-3.2
    for (let i = 0; i < 7; i++) {
      t += 60_000;
      pool.recordOutcome('A', ERROR, 2_000);
    }
    close(A().successEwma, 0.10881882041201547, 'E after the full failure run (2^-3.2)');
    const raw = (0.2 + 0.8 * 0.10881882041201547) * 0.525; // 0.1507... < 0.2
    assert.ok(raw < 0.2, 'precondition: raw target below floor');
    close(A().targetMultiplier, 0.2, 'target clamped to multiplierFloor');
    close(A().multiplier, 0.2, 'multiplier pinned at floor 0.2');
  });

  it('2.0 ceiling: fast key latencyFactor clamps at multiplierCap', () => {
    let t = 0;
    const pool = createKeyPool(entriesAB(), { now: () => t, periodMs: 0 });
    const B = () => pool.status().find((r) => r.name === 'B');
    // Warm both (A slow 2000, B fast 100) so the warm mean is defined.
    t = 0;
    pool.recordOutcome('A', SUCCESS, 2_000);
    for (let i = 0; i < 4; i++) {
      t += 60_000;
      pool.recordOutcome('A', SUCCESS, 2_000);
    }
    for (let i = 0; i < 5; i++) {
      t += 60_000;
      pool.recordOutcome('B', SUCCESS, 100);
    }
    // target_B = 1.0 x clamp((2000+100)/2 / 100, .5, 2) = 2.0; each pooled
    // recompute ramps +0.2 until the cap pins it exactly at 2.0.
    for (let i = 0; i < 6; i++) {
      t += 60_000;
      pool.recordOutcome('B', SUCCESS, 100);
    }
    close(B().targetMultiplier, 2, 'target clamped to multiplierCap');
    close(B().multiplier, 2, 'multiplier pinned at cap 2.0');
  });
});

describe('429/timeout instant demote + 5xx separation (immediate layer)', () => {
  it('REJECTED_429 demotes x0.5 per event down to the 0.2 floor, bypassing the recompute gate', () => {
    let t = 0;
    const pool = createKeyPool(entriesAB(), { now: () => t, periodMs: 10_000 });
    const A = () => pool.status().find((r) => r.name === 'A');
    pool.recordOutcome('A', SUCCESS, 100); // seed; construction-time gate keeps recompute closed
    close(A().multiplier, 1, 'start at 1');

    // Clock stays frozen inside the 10s period: every recordOutcome's
    // recompute gate is closed, so only the immediate demote layer is visible.
    pool.recordOutcome('A', REJECTED_429, 1_500);
    close(A().multiplier, 0.5, 'first 429: x0.5');
    pool.recordOutcome('A', REJECTED_429, 1_500);
    close(A().multiplier, 0.25, 'second 429: 0.25');
    pool.recordOutcome('A', REJECTED_429, 1_500);
    close(A().multiplier, 0.2, 'third 429 hits the 0.2 floor');
    pool.recordOutcome('A', REJECTED_429, 1_500);
    close(A().multiplier, 0.2, 'floor holds');
    assert.equal(A().rejected429, 4);
    assert.equal(A().consecutiveRejections, 4);
    assert.equal(A().timeouts, 0);
    // Δt=0 between samples: alpha=0, latency EWMA deliberately unchanged.
    assert.equal(A().latencyEwma, 100, 'Δt=0 samples: alpha=0, EWMA unchanged');
    assert.equal(A().successEwma, 1, 'Δt=0 samples: success EWMA unchanged');
  });

  it('TIMEOUT demotes immediately and records the timeout budget as the latency sample', () => {
    let t = 0;
    const pool = createKeyPool(entriesAB(), { now: () => t, periodMs: 10_000 });
    const A = () => pool.status().find((r) => r.name === 'A');
    // First-ever sample on A is the timeout: seeds successEwma=0 and the
    // budget as latency; the instant demote still applies on top.
    pool.recordOutcome('A', TIMEOUT, 60_000); // caller passes the AbortSignal budget
    close(A().multiplier, 0.5, 'timeout demotes x0.5');
    assert.equal(A().timeouts, 1);
    assert.equal(A().consecutiveRejections, 1);
    assert.equal(A().latencyEwma, 60_000, 'budget value seeded as latency sample');
    close(A().successEwma, 0, 'timeout is a failure sample');
  });

  it('ERROR (5xx) does NOT touch the tracked multiplier — steady-state layer only', () => {
    let t = 0;
    const pool = createKeyPool(entriesAB(), { now: () => t, periodMs: 10_000 });
    const A = () => pool.status().find((r) => r.name === 'A');
    pool.recordOutcome('A', SUCCESS, 100);
    t = 1_000;
    pool.recordOutcome('A', REJECTED_429, 1_500);
    close(A().multiplier, 0.5, 'precondition: demoted');
    t = 2_000;
    pool.recordOutcome('A', ERROR, 3_000);
    close(A().multiplier, 0.5, 'ERROR leaves multiplier untouched');
    assert.equal(A().consecutiveRejections, 1, 'ERROR neither resets nor increments');
    assert.equal(A().errors, 1);
    // ERROR still feeds the success-rate EWMA as a failure sample.
    // E bookkeeping: seed 1.0 @t=0; 429 failure @Δt=1s; ERROR @Δt=1s;
    // ERROR @Δt=98s — total failure decay = 2^(-100/300) = 2^(-1/3).
    t = 100_000;
    pool.recordOutcome('A', ERROR, 3_000);
    close(A().successEwma, Math.pow(2, -1 / 3), 'ERROR counted as successEwma 0-sample');
    // SUCCESS resets the consecutive-rejection counter.
    t = 200_000;
    pool.recordOutcome('A', SUCCESS, 100);
    assert.equal(A().consecutiveRejections, 0, 'SUCCESS resets consecutiveRejections');
  });
});

describe('ramp-limited recovery (D-R3: never an instant full-weight restore)', () => {
  it('storm leaves m=0.2 with target≈1; k recompute ticks hold m ≤ 0.2+0.2k AND m ≤ target', () => {
    let t = 0;
    const pool = createKeyPool(entriesAB(), { now: () => t, periodMs: 10_000 });
    const A = () => pool.status().find((r) => r.name === 'A');
    // Seed healthy, then storm: four instant 429 demotes inside one period.
    pool.recordOutcome('A', SUCCESS, 100);
    for (let i = 0; i < 4; i++) {
      pool.recordOutcome('A', REJECTED_429, 1_500);
    }
    close(A().multiplier, 0.2, 'storm end: m pinned at floor');
    close(A().targetMultiplier, 1, 'target still ≈1 (EWMA barely moved, Δt=0)');

    const expectations = [0.4, 0.6, 0.8, 1.0, 1.0, 1.0];
    for (let k = 1; k <= expectations.length; k++) {
      t += 10_000;
      pool.pick(); // pick entry is a lazy recompute trigger
      const m = A().multiplier;
      close(m, expectations[k - 1], `tick ${k} multiplier`);
      assert.ok(m <= 0.2 + 0.2 * k + EPS, `tick ${k}: m ≤ 0.2+0.2k (slope bound)`);
      assert.ok(m <= A().targetMultiplier + EPS, `tick ${k}: m ≤ target (cap bound)`);
    }
  });
});

describe('degenerate identity (zero samples ≡ static smooth-WRR)', () => {
  it('100 picks with no recordOutcome match the wrAccum reference sequence exactly', () => {
    const entries = baseEntries();
    const pool = createKeyPool(entries, { now: () => 0, periodMs: 0 });
    const got = [];
    for (let i = 0; i < 100; i++) got.push(pool.pick().name);
    const want = referenceWrAccumSequence(entries, 100);
    assert.deepEqual(got, want, 'zero-sample sequence must equal the nginx wrAccum yardstick');
    // multipliers never moved off 1.
    for (const row of pool.status()) {
      close(row.multiplier, 1, `zero-sample multiplier for ${row.name}`);
      close(row.targetMultiplier, 1, `zero-sample target for ${row.name}`);
      assert.equal(row.latencyEwma, null, 'no latency sample ⇒ no value');
    }
  });

  it('construction validation: duplicate name / weight≤0 / missing key all throw', () => {
    const bad = [
      [{ name: 'x', key: 'k1', weight: 1 }, { name: 'x', key: 'k2', weight: 1 }],
      [{ name: 'x', key: 'k1', weight: 0 }],
      [{ name: 'x', key: 'k1', weight: -3 }],
      [{ name: 'x', key: 'k1', weight: NaN }],
      [{ name: 'x', key: 'k1', weight: Infinity }],
      [{ name: 'x', key: 'k1', weight: '6' }],
      [{ name: '', key: 'k1', weight: 1 }],
      [{ name: 'x', key: '', weight: 1 }],
      [{ name: 'x', weight: 1 }],
      [{ key: 'k1', weight: 1 }],
      [],
      'not-an-array',
    ];
    for (const entries of bad) {
      assert.throws(() => createKeyPool(entries, {}), `must throw: ${JSON.stringify(entries)}`);
    }
    // Valid construction does not throw.
    assert.doesNotThrow(() => createKeyPool(baseEntries(), {}));
  });
});

describe('in-flight accounting and status hygiene', () => {
  it('begin/end maintain inFlight only — pick never mutates it', () => {
    const pool = createKeyPool(baseEntries(), { now: () => 0, periodMs: 0 });
    pool.pick();
    pool.begin('k-a');
    pool.begin('k-a');
    pool.begin('k-b');
    let row = pool.status().find((r) => r.name === 'k-a');
    assert.equal(row.inFlight, 2);
    assert.equal(row.sent, 1, 'pick increments sent');
    pool.end('k-a');
    row = pool.status().find((r) => r.name === 'k-a');
    assert.equal(row.inFlight, 1);
    pool.end('k-a');
    pool.end('k-a'); // over-release clamps at 0
    row = pool.status().find((r) => r.name === 'k-a');
    assert.equal(row.inFlight, 0);
    assert.throws(() => pool.begin('nope'), /nope/);
    assert.throws(() => pool.recordOutcome('nope', SUCCESS, 1), /nope/);
  });

  it('status rows never expose key material (names only)', () => {
    const pool = createKeyPool(baseEntries(), { now: () => 0, periodMs: 0 });
    pool.recordOutcome('k-a', SUCCESS, 100);
    const rows = pool.status();
    for (const row of rows) {
      assert.ok(!('key' in row), `row ${row.name} must not carry a key field`);
      assert.ok(!JSON.stringify(row).includes('stub-key-a'), 'key value leaked into status');
    }
    // Full snapshot shape (plan contract).
    assert.deepEqual(
      Object.keys(rows[0]).sort(),
      [
        'consecutiveRejections',
        'effectiveWeight',
        'errors',
        'inFlight',
        'latencyEwma',
        'multiplier',
        'name',
        'ok',
        'rejected429',
        'samples',
        'sent',
        'staticWeight',
        'successEwma',
        'targetMultiplier',
        'timeouts',
      ].sort(),
      'status row field set',
    );
  });

  it('two pools are fully isolated (no module-level singleton state)', () => {
    let t1 = 0;
    let t2 = 0;
    const p1 = createKeyPool(baseEntries(), { now: () => t1, periodMs: 10_000 });
    const p2 = createKeyPool(baseEntries(), { now: () => t2, periodMs: 10_000 });
    p1.recordOutcome('k-a', REJECTED_429, 1_500);
    p1.recordOutcome('k-a', REJECTED_429, 1_500);
    p2.recordOutcome('k-a', SUCCESS, 100);
    p2.recordOutcome('k-b', SUCCESS, 100);
    const r1 = p1.status().find((r) => r.name === 'k-a');
    const r2 = p2.status().find((r) => r.name === 'k-a');
    close(r1.multiplier, 0.25, 'pool1 demoted');
    close(r2.multiplier, 1, 'pool2 unaffected');
    assert.equal(r1.rejected429, 2);
    assert.equal(r2.ok, 1);
    assert.equal(r2.rejected429, 0);
    // Interleave picks: each accumulator lives on its own instance.
    const s1 = [];
    const s2 = [];
    for (let i = 0; i < 20; i++) {
      s1.push(p1.pick().name);
      s2.push(p2.pick().name);
    }
    // p2 is a pristine zero-sample pool: equals the reference sequence.
    assert.deepEqual(s2, referenceWrAccumSequence(baseEntries(), 20));
    assert.equal(s1.length, 20);
  });
});

describe('tracer e2e: storm demotes -> ramp-limited recovery -> distribution shift', () => {
  it('A(6)/B(2) stub keys across the scripted storm satisfy every D-R3 property', () => {
    const r = runTracerScenario();

    // Storm window: A share drops far below the 75% static-WRR expectation.
    assert.ok(
      r.stormShare < 0.25,
      `storm share A = ${r.stormShare} must be < 0.25 (static baseline 0.75)`,
    );
    // A's multiplier hit the 0.2 floor during the storm.
    close(r.minMultAStorm, 0.2, 'A multiplier reached the 0.2 floor in-storm');
    // First recompute tick after the storm is slope-clamped (≤ 0.4).
    assert.ok(
      r.firstTickMultAfterStorm <= 0.4 + EPS,
      `first post-storm tick m=${r.firstTickMultAfterStorm} must be ≤ 0.4`,
    );
    // Final state: recovery happened but never overshoots the target (<1).
    assert.ok(
      r.finalMultA > 0.75 && r.finalMultA < 1,
      `final A multiplier ${r.finalMultA} must be in (0.75, 1.0)`,
    );
    // B never gets demoted below 1.0 anywhere in the run.
    assert.ok(r.minMultB >= 1 - EPS, `B min multiplier ${r.minMultB} must be ≥ 1.0`);
    // Share recovered after the storm.
    assert.ok(
      r.finalSegmentShare > r.stormShare,
      `final-segment share ${r.finalSegmentShare} must exceed storm share ${r.stormShare}`,
    );
    // status() identity: effectiveWeight == staticWeight x multiplier (±1e-9),
    // checked on every row of every pick (formula observability).
    close(r.maxIdentityErr, 0, 'effectiveWeight = staticWeight x multiplier over all snapshots');
    assert.ok(r.identityChecks > 0, 'identity actually exercised');
  });

  it('reset() restores pristine zero-sample state (degenerate identity again)', () => {
    let t = 0;
    const pool = createKeyPool(baseEntries(), { now: () => t, periodMs: 0 });
    t = 1_000;
    pool.recordOutcome('k-a', REJECTED_429, 1_500);
    t = 2_000;
    pool.recordOutcome('k-a', ERROR, 500);
    pool.reset();
    for (const row of pool.status()) {
      assert.equal(row.samples, 0);
      assert.equal(row.sent, 0);
      assert.equal(row.multiplier, 1);
      assert.equal(row.latencyEwma, null);
      assert.equal(row.consecutiveRejections, 0);
    }
    const entries = baseEntries();
    const got = [];
    for (let i = 0; i < 50; i++) got.push(pool.pick().name);
    assert.deepEqual(got, referenceWrAccumSequence(entries, 50));
  });
});

describe('KEY_POOL env contract (parseKeyPoolEnv validation matrix)', () => {
  it('missing/blank env throws guidance that names KEY_POOL with Git Bash and cmd examples', () => {
    for (const raw of [undefined, null, '', '   ']) {
      let message = '';
      assert.throws(
        () => parseKeyPoolEnv(raw),
        (e) => {
          message = e.message;
          return true;
        },
        `must throw for ${JSON.stringify(raw)}`,
      );
      assert.ok(message.includes('KEY_POOL'), 'guidance must name KEY_POOL');
      assert.ok(message.includes('Git Bash'), 'guidance must carry the Git Bash example');
      assert.ok(message.includes('cmd.exe'), 'guidance must carry the cmd example');
    }
  });

  it('invalid JSON / wrong shape / bad entries each throw with a precise message', () => {
    const cases = [
      ['not-json', /not valid JSON/],
      ['{}', /non-empty JSON array/],
      ['[]', /non-empty JSON array/],
      ['[1]', /KEY_POOL\[0\] must be an object/],
      ['[{"key":"k","weight":1}]', /KEY_POOL\[0\]\.name/],
      ['[{"name":"","key":"k","weight":1}]', /KEY_POOL\[0\]\.name/],
      ['[{"name":"a","weight":1}]', /KEY_POOL\[0\]\.key/],
      ['[{"name":"a","key":""}]', /KEY_POOL\[0\]\.key/],
      ['[{"name":"a","key":"k"}]', /KEY_POOL\[0\]\.weight/],
      ['[{"name":"a","key":"k","weight":0}]', /KEY_POOL\[0\]\.weight/],
      ['[{"name":"a","key":"k","weight":-1}]', /KEY_POOL\[0\]\.weight/],
      ['[{"name":"a","key":"k","weight":NaN}]', /not valid JSON/],
      ['[{"name":"a","key":"k","weight":"6"}]', /KEY_POOL\[0\]\.weight/],
      [
        '[{"name":"a","key":"k1","weight":1},{"name":"a","key":"k2","weight":2}]',
        /KEY_POOL\[1\]\.name "a" is duplicated/,
      ],
      [
        '[{"name":"a","key":"k1","weight":1},{"name":"b","key":"k2","weight":0}]',
        /KEY_POOL\[1\]\.weight/,
      ],
    ];
    for (const [raw, pattern] of cases) {
      assert.throws(() => parseKeyPoolEnv(raw), pattern, `must throw for ${raw}`);
    }
  });

  it('valid JSON round-trips to normalized {name,key,weight} rows', () => {
    const rows = parseKeyPoolEnv(
      '[{"name":"pool-key-01","key":"sk-alpha","weight":6},{"name":"pool-key-02","key":"sk-beta","weight":2}]',
    );
    assert.deepEqual(rows, [
      { name: 'pool-key-01', key: 'sk-alpha', weight: 6 },
      { name: 'pool-key-02', key: 'sk-beta', weight: 2 },
    ]);
    // Parsed rows feed createKeyPool without further adaptation.
    assert.doesNotThrow(() => createKeyPool(rows, {}));
  });
});

describe('CLI response → outcome classifier (pure function)', () => {
  it('maps status/abort combinations to the four pool outcomes exactly', () => {
    const table = [
      [{ status: 200, aborted: false }, SUCCESS],
      [{ status: 201, aborted: false }, SUCCESS],
      [{ status: 204, aborted: false }, SUCCESS],
      [{ status: 429, aborted: false }, REJECTED_429],
      [{ status: 0, aborted: true }, TIMEOUT], // AbortSignal.timeout fired
      [{ status: 500, aborted: false }, ERROR],
      [{ status: 502, aborted: false }, ERROR],
      [{ status: 404, aborted: false }, ERROR],
      [{ status: 400, aborted: false }, ERROR],
      [{ status: 0, aborted: false }, ERROR], // transport error, not a timeout
    ];
    for (const [input, expected] of table) {
      assert.equal(
        classifyOutcome(input),
        expected,
        `classifyOutcome(${JSON.stringify(input)}) must be ${expected}`,
      );
    }
  });
});

describe('CLI strategy pools (weighted ≡ feedback on zero samples)', () => {
  it('100-pick sequences match across both strategies and the static yardstick', () => {
    const entries = baseEntries();
    const feedback = createStrategyPool('feedback', entries, { now: () => 0 });
    const weighted = createStrategyPool('weighted', entries, { now: () => 0 });
    const sFeedback = [];
    const sWeighted = [];
    for (let i = 0; i < 100; i++) {
      sFeedback.push(feedback.pick().name);
      sWeighted.push(weighted.pick().name);
    }
    assert.deepEqual(sFeedback, sWeighted, 'weighted must equal feedback at zero samples');
    assert.deepEqual(sWeighted, referenceWrAccumSequence(entries, 100));
    // By contract the weighted strategy never calls recordOutcome, so every
    // multiplier stays at its initial 1 (periodMs=Infinity freezes recompute).
    for (const row of weighted.status()) close(row.multiplier, 1, `weighted ${row.name}`);
  });
});
