/**
 * demo-stub.mjs — RTE-04 tracer demo command (observable weight timeline).
 *
 * Virtual clock + scripted stub outcomes only: zero fetch, zero sleep, zero
 * real timers — fully deterministic. Drives the same scenario as the tracer
 * test in key-pool.test.mjs (keep the two copies in sync):
 *   A(staticWeight=6) storms 429s in [320s, 2720s), B(2) always healthy;
 *   prints a per-key multiplier / effective weight / cumulative picks line
 *   every virtual 10s, then an invariant verdict table + DONE marker.
 * Any violated invariant prints the reason to stderr and exits 1.
 *
 * Zero-dependency constraint: only node: builtins and relative imports.
 */

import { createKeyPool, SUCCESS, REJECTED_429 } from './key-pool.mjs';

// --- scenario (identical values to key-pool.test.mjs SC) -------------------
const STEP_MS = 500; // virtual time per request
const PERIOD_MS = 60_000; // recompute cadence
const LATENCY_OK = 100; // healthy response latency for both keys (ms)
const LATENCY_429 = 3_000; // storm: A's 429 responses are slow (ms)
const STORM_START_MS = 320_000;
const STORM_END_MS = 2_720_000; // 2400s storm
const TOTAL_MS = 3_420_000; // 700s recovery after the storm
const FINAL_SEGMENT_MS = 100_000;
const PRINT_EVERY_MS = 10_000;
const WEIGHT_A = 6;
const WEIGHT_B = 2;

let t = 0;
const pool = createKeyPool(
  [
    { name: 'A', key: 'stub-key-a', weight: WEIGHT_A },
    { name: 'B', key: 'stub-key-b', weight: WEIGHT_B },
  ],
  { now: () => t, periodMs: PERIOD_MS },
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
let lastPrintBucket = -1;
const picks = { A: 0, B: 0 };

const fmt = (v) => v.toFixed(4);
const inStormWindow = (time) => time >= STORM_START_MS && time < STORM_END_MS;

for (;;) {
  if (t >= TOTAL_MS) break;
  // After the storm ends, jump the virtual clock one full period past the
  // last pick so the very next pick entry performs the FIRST recompute after
  // storm end (lazy recompute: no picks inside the gap, no ticks either).
  if (!stormEnded && t >= STORM_END_MS) {
    stormEnded = true;
    t = lastT + PERIOD_MS + 1;
    if (t < STORM_END_MS) t = STORM_END_MS;
  }

  const entry = pool.pick();
  lastT = t;
  t += STEP_MS;

  const inStorm = inStormWindow(t);
  const outcome = entry.name === 'A' && inStorm ? REJECTED_429 : SUCCESS;
  pool.recordOutcome(entry.name, outcome, outcome === REJECTED_429 ? LATENCY_429 : LATENCY_OK);
  picks[entry.name]++;

  if (inStorm) {
    picksInStorm++;
    if (entry.name === 'A') aPicksInStorm++;
  }
  if (t >= TOTAL_MS - FINAL_SEGMENT_MS) {
    finalSegmentPicks++;
    if (entry.name === 'A') finalSegmentAPicks++;
  }

  const status = pool.status();
  const rowA = status[0];
  const rowB = status[1];
  if (inStorm && rowA.multiplier < minMultAStorm) minMultAStorm = rowA.multiplier;
  if (rowB.multiplier < minMultB) minMultB = rowB.multiplier;
  if (firstTickMultAfterStorm === null && stormEnded) firstTickMultAfterStorm = rowA.multiplier;
  for (const row of status) {
    const err = Math.abs(row.effectiveWeight - row.staticWeight * row.multiplier);
    if (err > maxIdentityErr) maxIdentityErr = err;
  }

  const bucket = Math.floor(t / PRINT_EVERY_MS);
  if (bucket !== lastPrintBucket) {
    lastPrintBucket = bucket;
    console.log(
      `t=${String(Math.floor(t / 1000)).padStart(5, '0')}s ` +
        `| A multiplier=${fmt(rowA.multiplier)} effectiveWeight=${fmt(rowA.effectiveWeight)} picks=${String(picks.A).padStart(4)} ` +
        `| B multiplier=${fmt(rowB.multiplier)} effectiveWeight=${fmt(rowB.effectiveWeight)} picks=${String(picks.B).padStart(4)}`,
    );
  }
}

const finalStatus = pool.status();
const stormShare = aPicksInStorm / picksInStorm;
const finalSegmentShare = finalSegmentAPicks / finalSegmentPicks;
const finalMultA = finalStatus[0].multiplier;

const invariants = [
  {
    label: `storm A share ${fmt(stormShare)} < 0.25 (static-WRR baseline 0.75)`,
    pass: stormShare < 0.25,
  },
  {
    label: `A multiplier reached the 0.2 floor in-storm (min=${fmt(minMultAStorm)})`,
    pass: Math.abs(minMultAStorm - 0.2) < 1e-9,
  },
  {
    label: `first recompute tick after storm ≤ 0.4 (got ${fmt(firstTickMultAfterStorm ?? NaN)})`,
    pass: firstTickMultAfterStorm !== null && firstTickMultAfterStorm <= 0.4 + 1e-9,
  },
  {
    label: `final A multiplier ${fmt(finalMultA)} in (0.75, 1.0)`,
    pass: finalMultA > 0.75 && finalMultA < 1,
  },
  {
    label: `B multiplier ≥ 1.0 for the whole run (min=${fmt(minMultB)})`,
    pass: minMultB >= 1 - 1e-9,
  },
  {
    label: `A final-segment share ${fmt(finalSegmentShare)} > storm share ${fmt(stormShare)}`,
    pass: finalSegmentShare > stormShare,
  },
  {
    label: `status identity effectiveWeight = staticWeight x multiplier (maxErr=${maxIdentityErr.toExponential(2)})`,
    pass: maxIdentityErr < 1e-9,
  },
];

console.log('=== INVARIANTS ===');
let failed = 0;
for (const inv of invariants) {
  console.log(`${inv.pass ? 'PASS' : 'FAIL'}  ${inv.label}`);
  if (!inv.pass) failed++;
}
if (failed > 0) {
  console.error(`demo-stub: ${failed} invariant(s) violated`);
  process.exit(1);
}
console.log('DONE');
process.exit(0);
