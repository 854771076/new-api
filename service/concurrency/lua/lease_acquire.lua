-- lease_acquire.lua — grant lease slots for a batch of scope buckets,
-- all-or-nothing (D-05): reclaim expired leases, judge every bucket, then
-- either write all slots or write nothing. One EVALSHA per request batch.
--
-- KEYS[1]       known-scope registry (Set of scope ZSET keys)
-- KEYS[2..N+1]  scope bucket ZSETs (member = lease id, score = expiry ms)
-- ARGV[1]       lease member, shared by every bucket of this batch ({nodeID}:{random})
-- ARGV[2]       lease TTL in milliseconds
-- ARGV[3..N+2]  per-bucket limit (<= 0 counts without enforcing, D-18)
--
-- Returns {1} on grant, or {0, <1-based bucket index>} when a bucket is at
-- its limit — in which case nothing is written (no partial grant, no
-- rollback window; the rejection index is diagnostic only).

local registry = KEYS[1]
local bucketCount = #KEYS - 1
local member = ARGV[1]
local ttlMs = tonumber(ARGV[2])

local time = redis.call('TIME')
local nowMs = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)

-- Reclaim expired leases first so stale slots never block a grant. Score is
-- the lease expiry instant in milliseconds; empty ZSETs are auto-deleted.
for i = 2, #KEYS do
  redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', nowMs)
end

-- Judge every bucket before writing anything: all-or-nothing (D-05).
-- limit <= 0 skips enforcement but the slot is still written (D-18).
for i = 1, bucketCount do
  local limit = tonumber(ARGV[i + 2])
  if limit > 0 and redis.call('ZCARD', KEYS[i + 1]) >= limit then
    return {0, i}
  end
end

-- Grant: one member per bucket plus registry membership, in the same script
-- so the registry write costs no extra round-trip (D-17).
local expiresAt = nowMs + ttlMs
for i = 1, bucketCount do
  redis.call('ZADD', KEYS[i + 1], expiresAt, member)
  redis.call('SADD', registry, KEYS[i + 1])
end
return {1}
