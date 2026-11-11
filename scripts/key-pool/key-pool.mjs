/**
 * key-pool.mjs — RTE-04 client key-pool feedback-loop weight library.
 *
 *出处:02-token phase 02-19 / D-R3 locked decision — per-key EWMA success rate
 * + latency, effective weight = staticWeight x success decay x latency penalty,
 * periodic recompute; 429/timeout demote the tracked multiplier immediately
 * (floor 0.2), recovery ramps up at most rampStep per recompute tick (never an
 * instant full-weight restore). Formula isomorphic to the 02-17 gateway-side
 * channel routing EWMA (same physical quantity, same convergence expectations).
 *
 * Zero-dependency constraint: ESM, only node: builtins and relative imports,
 * Node >= 18, no package.json, no timers, no background tasks — every
 * recompute is lazily triggered at pick()/recordOutcome() entry under the
 * periodMs gate.
 */

// outcome constants (exported: pool result vocabulary shared by CLI/tests)
export const SUCCESS = 'success';
export const REJECTED_429 = 'rejected_429';
export const TIMEOUT = 'timeout';
export const ERROR = 'error';

const OUTCOMES = new Set([SUCCESS, REJECTED_429, TIMEOUT, ERROR]);

// ---------------------------------------------------------------------------
// Parameters — named constants with rationale (D-R3 sentence-by-sentence map;
// bounds are the accepted tuning ranges, identical physical values to the
// 02-17 gateway side so both sides share one mental model).
// ---------------------------------------------------------------------------
const SUCCESS_HALF_LIFE_MS = 300_000; // success-rate EWMA half-life 300s, accepted range [30s, 3600s] — slow steady-state health
const LATENCY_HALF_LIFE_MS = 60_000; // latency EWMA half-life 60s, accepted range [10s, 600s] — same value/bounds as 02-17 gateway
const WARM_SAMPLES = 5; // warm gate: fewer samples => target=1.0 (cold key degrades to static; client sample volume is far below gateway aggregate)
const DEMOTE_FACTOR = 0.5; // instant layer: each 429/timeout multiplies the tracked multiplier by 0.5 (outlier-detection style, no waiting for a tick)
const MULTIPLIER_FLOOR = 0.2; // tracked multiplier never below 0.2 (never starves a key); also the target clamp floor
const MULTIPLIER_CAP = 2.0; // tracked multiplier never above 2.0 (successFactor<=1 x latencyFactor<=2 natural ceiling; never monopolizes)
const DEFAULT_PERIOD_MS = 10_000; // recompute at most every 10s (same cadence as 02-17 channelRoutingRecomputeInterval)
const DEFAULT_RAMP_STEP = 0.2; // recovery: at most +0.2 per recompute tick toward target (D-R3 "never instant full restore")
const SUCCESS_FACTOR_BASE = 0.2; // successFactor = 0.2 + 0.8 x successEwma (floor 0.2 even at successEwma=0)
const SUCCESS_FACTOR_SPAN = 0.8;
const LATENCY_FACTOR_MIN = 0.5; // latencyFactor = clamp(warmMean/thisKey, 0.5, 2.0)
const LATENCY_FACTOR_MAX = 2.0;

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

/**
 * Parse the KEY_POOL env contract: JSON array of {name, key, weight}.
 * Pure function shared by the CLI and tests. Throws a guidance error when the
 * variable is missing and names the offending entry for every other defect.
 */
export function parseKeyPoolEnv(raw) {
  if (raw === undefined || raw === null || String(raw).trim() === '') {
    throw new Error(
      'KEY_POOL is required: a JSON array of {"name","key","weight"} entries.\n' +
        '  Git Bash: KEY_POOL=\'[{"name":"pool-key-01","key":"sk-...","weight":6}]\' MODEL=my-model node scripts/key-pool/key-pool-cli.mjs\n' +
        '  cmd.exe:  set "KEY_POOL=[{"name":"pool-key-01","key":"sk-...","weight":6}]" && set MODEL=my-model && node scripts/key-pool/key-pool-cli.mjs',
    );
  }
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (e) {
    throw new Error(`KEY_POOL is not valid JSON: ${e.message}`);
  }
  if (!Array.isArray(parsed) || parsed.length === 0) {
    throw new Error('KEY_POOL must be a non-empty JSON array of {"name","key","weight"} entries');
  }
  const seen = new Set();
  return parsed.map((entry, i) => {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) {
      throw new Error(`KEY_POOL[${i}] must be an object {"name","key","weight"}`);
    }
    const { name, key, weight } = entry;
    if (typeof name !== 'string' || name === '') {
      throw new Error(`KEY_POOL[${i}].name must be a non-empty string`);
    }
    if (seen.has(name)) {
      throw new Error(`KEY_POOL[${i}].name "${name}" is duplicated`);
    }
    seen.add(name);
    if (typeof key !== 'string' || key === '') {
      throw new Error(`KEY_POOL[${i}].key must be a non-empty string`);
    }
    if (typeof weight !== 'number' || !Number.isFinite(weight) || weight <= 0) {
      throw new Error(`KEY_POOL[${i}].weight must be a finite number > 0`);
    }
    return { name, key, weight };
  });
}

/**
 * Create a key pool. Returns {pick, recordOutcome, begin, end, status, reset}.
 *
 * @param entries [{name, key, weight}] — weight must be a finite number > 0,
 *   names unique, key non-empty; any violation throws at construction
 *   (a 0-weight key must never be pickable, so it cannot even exist).
 * @param opts {now, periodMs, rampStep} — now is the injected clock (default
 *   Date.now), periodMs the recompute gate (0 = recompute on every entry,
 *   Infinity = never), rampStep the per-tick recovery slope.
 */
export function createKeyPool(entries, opts = {}) {
  if (!Array.isArray(entries) || entries.length === 0) {
    throw new Error('key-pool: entries must be a non-empty array');
  }
  const now = opts.now ?? Date.now;
  const periodMs = opts.periodMs ?? DEFAULT_PERIOD_MS;
  const rampStep = opts.rampStep ?? DEFAULT_RAMP_STEP;
  if (typeof now !== 'function') throw new Error('key-pool: opts.now must be a function');

  const records = [];
  const seen = new Set();
  for (const e of entries) {
    if (e === null || typeof e !== 'object') {
      throw new Error(`key-pool: entry ${String(e)} must be an object {name, key, weight}`);
    }
    const { name, key, weight } = e;
    if (typeof name !== 'string' || name === '') {
      throw new Error('key-pool: every entry needs a non-empty name');
    }
    if (seen.has(name)) throw new Error(`key-pool: duplicate key name "${name}"`);
    seen.add(name);
    if (typeof key !== 'string' || key === '') {
      throw new Error(`key-pool: entry "${name}" needs a non-empty key`);
    }
    if (typeof weight !== 'number' || !Number.isFinite(weight) || weight <= 0) {
      throw new Error(`key-pool: entry "${name}" needs a finite weight > 0, got ${String(weight)}`);
    }
    records.push({
      name,
      key,
      weight,
      accum: 0, // smooth weighted round-robin accumulator (nginx wrAccum algorithm)
      state: {
        successEwma: 1.0, // prior: unobserved = healthy
        latencyEwma: null, // no prior value until the first latency sample
        samples: 0,
        lastSampleAt: null,
        tracked: 1.0, // tracked multiplier (instant demote layer), starts at full weight
        target: 1.0, // last recomputed EWMA target
        consecutiveRejections: 0, // observation only (429/timeout streak)
        inFlight: 0,
        sent: 0,
        ok: 0,
        rejected429: 0,
        timeouts: 0,
        errors: 0,
      },
    });
  }

  // First recompute lands one full period after construction so the instant
  // demote layer is observable before any ramp-back can fire.
  let lastRecomputeAt = now();

  function effectiveWeight(rec) {
    return rec.weight * clamp(rec.state.tracked, MULTIPLIER_FLOOR, MULTIPLIER_CAP);
  }

  /**
   * Periodic recompute (lazy, gate: at most once per periodMs). Applies the
   * half-life EWMA targets and the two-sided tracked-multiplier law:
   *   tracked <  target -> tracked = min(target, tracked + rampStep)  (slope-limited climb)
   *   tracked >= target -> tracked = target                            (instant settle down)
   */
  function maybeRecompute(t) {
    if (t - lastRecomputeAt < periodMs) return;
    lastRecomputeAt = t;

    // Warm latency mean: keys with >= WARM_SAMPLES and a latency value.
    let warmLatencySum = 0;
    let warmLatencyCount = 0;
    for (const rec of records) {
      if (rec.state.samples >= WARM_SAMPLES && rec.state.latencyEwma !== null) {
        warmLatencySum += rec.state.latencyEwma;
        warmLatencyCount++;
      }
    }
    const warmLatencyMean = warmLatencyCount > 0 ? warmLatencySum / warmLatencyCount : null;

    for (const rec of records) {
      const st = rec.state;
      let target;
      if (st.samples < WARM_SAMPLES) {
        target = 1.0; // warm gate: cold key degrades to its static weight
      } else {
        const successFactor = SUCCESS_FACTOR_BASE + SUCCESS_FACTOR_SPAN * st.successEwma;
        // Single warm record => mean/itself = 1.0 naturally; a warm key without
        // a latency value also falls back to 1.0.
        const latencyFactor =
          st.latencyEwma !== null && warmLatencyMean !== null
            ? clamp(warmLatencyMean / st.latencyEwma, LATENCY_FACTOR_MIN, LATENCY_FACTOR_MAX)
            : 1.0;
        target = clamp(successFactor * latencyFactor, MULTIPLIER_FLOOR, MULTIPLIER_CAP);
      }
      st.target = target;
      if (st.tracked < target) {
        st.tracked = Math.min(target, st.tracked + rampStep);
      } else {
        st.tracked = target;
      }
    }
  }

  /**
   * Record one outcome for a key.
   *  - SUCCESS: success sample 1, resets consecutiveRejections.
   *  - REJECTED_429 / TIMEOUT: success sample 0 + instant demote
   *    tracked = max(tracked x 0.5, 0.2), consecutiveRejections++.
   *    TIMEOUT latencyMs is the caller's timeout budget (slowest possible
   *    request = heaviest latency sample).
   *  - ERROR (5xx and other): success sample 0 only — no instant penalty
   *    (immediate layer vs steady-state layer separation, D-R3 names only
   *    429/timeout for instant demotion).
   * EWMA alpha = 1 - 0.5^(dt/halfLife); the first sample seeds directly.
   */
  function recordOutcome(name, outcome, latencyMs) {
    const rec = records.find((r) => r.name === name);
    if (!rec) throw new Error(`key-pool: unknown key "${name}"`);
    if (!OUTCOMES.has(outcome)) throw new Error(`key-pool: unknown outcome "${outcome}"`);
    const st = rec.state;
    const t = now();

    let successValue;
    if (outcome === SUCCESS) {
      st.ok++;
      st.consecutiveRejections = 0;
      successValue = 1;
    } else if (outcome === REJECTED_429) {
      st.rejected429++;
      st.consecutiveRejections++;
      st.tracked = Math.max(st.tracked * DEMOTE_FACTOR, MULTIPLIER_FLOOR);
      successValue = 0;
    } else if (outcome === TIMEOUT) {
      st.timeouts++;
      st.consecutiveRejections++;
      st.tracked = Math.max(st.tracked * DEMOTE_FACTOR, MULTIPLIER_FLOOR);
      successValue = 0;
    } else {
      st.errors++;
      successValue = 0; // ERROR: immediate layer untouched
    }

    const latencyOk = Number.isFinite(latencyMs) && latencyMs >= 0;
    if (st.lastSampleAt === null) {
      st.successEwma = successValue; // first sample seeds directly
      if (latencyOk) st.latencyEwma = latencyMs;
    } else {
      const dt = Math.max(0, t - st.lastSampleAt);
      if (dt > 0) {
        const alphaSuccess = 1 - Math.pow(0.5, dt / SUCCESS_HALF_LIFE_MS);
        st.successEwma = st.successEwma * (1 - alphaSuccess) + successValue * alphaSuccess;
        if (st.latencyEwma !== null && latencyOk) {
          const alphaLatency = 1 - Math.pow(0.5, dt / LATENCY_HALF_LIFE_MS);
          st.latencyEwma = st.latencyEwma * (1 - alphaLatency) + latencyMs * alphaLatency;
        } else if (st.latencyEwma === null && latencyOk) {
          st.latencyEwma = latencyMs;
        }
      } else if (st.latencyEwma === null && latencyOk) {
        st.latencyEwma = latencyMs;
      }
      // dt == 0 => alpha = 0, EWMA deliberately unchanged (no fake evidence).
    }
    st.lastSampleAt = t;
    st.samples++;

    maybeRecompute(t); // recordOutcome entry trigger (sees the fresh sample)
  }

  /**
   * Pick the next key via smooth weighted round-robin over effective weights
   * (nginx wrAccum algorithm, ported from concurrency-test/multi-key-test.mjs).
   * Zero samples => all multipliers 1 => bit-identical to the static sequence.
   * inFlight is bookkeeping only (caller drives begin/end), not part of picking.
   */
  function pick() {
    maybeRecompute(now());
    let totalWeight = 0;
    for (const rec of records) totalWeight += effectiveWeight(rec);
    let best = 0;
    for (let i = 0; i < records.length; i++) {
      records[i].accum += effectiveWeight(records[i]);
      if (records[i].accum > records[best].accum) best = i;
    }
    records[best].accum -= totalWeight;
    records[best].state.sent++;
    const chosen = records[best];
    return { name: chosen.name, key: chosen.key, weight: chosen.weight };
  }

  function find(name) {
    const rec = records.find((r) => r.name === name);
    if (!rec) throw new Error(`key-pool: unknown key "${name}"`);
    return rec;
  }

  return {
    pick,
    recordOutcome,
    begin(name) {
      find(name).state.inFlight++;
    },
    end(name) {
      const st = find(name).state;
      st.inFlight = Math.max(0, st.inFlight - 1);
    },
    /** Snapshot rows — names and weights only, never key material. */
    status() {
      return records.map((rec) => ({
        name: rec.name,
        staticWeight: rec.weight,
        successEwma: rec.state.successEwma,
        latencyEwma: rec.state.latencyEwma,
        samples: rec.state.samples,
        targetMultiplier: rec.state.target,
        multiplier: rec.state.tracked,
        effectiveWeight: effectiveWeight(rec),
        consecutiveRejections: rec.state.consecutiveRejections,
        inFlight: rec.state.inFlight,
        sent: rec.state.sent,
        ok: rec.state.ok,
        rejected429: rec.state.rejected429,
        timeouts: rec.state.timeouts,
        errors: rec.state.errors,
      }));
    },
    reset() {
      for (const rec of records) {
        rec.accum = 0;
        Object.assign(rec.state, {
          successEwma: 1.0,
          latencyEwma: null,
          samples: 0,
          lastSampleAt: null,
          tracked: 1.0,
          target: 1.0,
          consecutiveRejections: 0,
          inFlight: 0,
          sent: 0,
          ok: 0,
          rejected429: 0,
          timeouts: 0,
          errors: 0,
        });
      }
      lastRecomputeAt = now();
    },
  };
}
