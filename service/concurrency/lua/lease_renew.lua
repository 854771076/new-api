-- lease_renew.lua — batch-renew one node's held lease members across their
-- scope buckets in a single EVALSHA (D-01: one ticker, one round-trip).
--
-- KEYS[1..K]    scope bucket ZSETs this node holds members in
-- ARGV[1]       lease TTL in milliseconds
-- ARGV[2..]     pairs of (1-based KEYS index, lease member), one pair per
--               (member, bucket) of the held-lease registry
--
-- Returns one status code per pair, in ARGV order:
--   1 = renewed  — member existed, score extended to now+TTL (CH counts it)
--   0 = gone     — member no longer in the ZSET (reclaimed past TTL, or
--                  released); the caller must stop renewing it and must NOT
--                  re-acquire (D-02: fewer counts beat corruption)
--
-- XX is the anti-zombie guard: only existing members are updated, so a
-- released or reclaimed member is never re-added (no zombie slot can be
-- revived by a late tick — research accepted #1, T-02-18).
--
-- The time source is redis.call('TIME') inside the script (Pitfall 4): the
-- expiry score must be computed from the Redis server clock, never from a
-- gateway node's local clock.

local ttlMs = tonumber(ARGV[1])

local time = redis.call('TIME')
local nowMs = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)
local newScore = nowMs + ttlMs

local out = {}
local i = 2
while i < #ARGV do
  local key = KEYS[tonumber(ARGV[i])]
  local member = ARGV[i + 1]
  -- XX: update existing members only; CH: score updates count as 1, so an
  -- existing member reads 1 and an absent member reads 0.
  if redis.call('ZADD', key, 'XX', 'CH', newScore, member) == 1 then
    out[#out + 1] = 1
  else
    out[#out + 1] = 0
  end
  i = i + 2
end
return out
