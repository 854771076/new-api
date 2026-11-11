-- lease_flush.lua — batch-cleanup one node's locally-released leases across
-- their scope buckets in a single EVALSHA: the batched variant of
-- lease_release.lua's cleanup chain, shared by the D-13 pending-release flush
-- (recovery after a Redis outage) and the D-19 shutdown bulk cleanup.
--
-- KEYS[1]       known-scope registry (Set of scope ZSET keys)
-- KEYS[2..K+1]  scope bucket ZSETs the batched members may live in
-- ARGV[1..]     pairs of (1-based KEYS index, lease member), one pair per
--               buffered (member, bucket) — same packing shape as
--               lease_renew.lua
--
-- Returns the number of members actually removed. ZREM ignores absent members
-- ("Non existing members are ignored" — research accepted #4), so a repeated
-- flush of the same members is a zero-change no-op: retries, double-flushes,
-- and a flush racing a normal release are all safe by construction.
--
-- The cleanup chain runs after all ZREMs, once per touched key: when a bucket
-- drops to ZCARD=0 it is SREMed from the registry (Redis deletes the emptied
-- ZSET itself, and the emptied registry Set deletes itself) — identical to
-- lease_release.lua and atomic inside this one script.

local registry = KEYS[1]
local removed = 0
local touched = {}

local i = 1
while i < #ARGV do
  local key = KEYS[tonumber(ARGV[i])]
  local member = ARGV[i + 1]
  removed = removed + redis.call('ZREM', key, member)
  touched[key] = true
  i = i + 2
end

for key in pairs(touched) do
  if redis.call('ZCARD', key) == 0 then
    redis.call('SREM', registry, key)
  end
end
return removed
