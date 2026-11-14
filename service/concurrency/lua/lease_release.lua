-- lease_release.lua — release one batch's lease member across its buckets
-- and run the cleanup chain (D-17/D-18): ZREM the member (idempotent — ZREM
-- ignores absent members), and when a bucket drops to ZCARD=0 SREM it from
-- the known-scope registry; Redis deletes the emptied ZSET automatically,
-- and the emptied registry Set deletes itself.
--
-- KEYS[1]       known-scope registry (Set of scope ZSET keys)
-- KEYS[2..N+1]  scope bucket ZSETs this batch may hold members in
-- ARGV[1]       lease member to remove ({nodeID}:{random})
--
-- Returns the number of buckets this member was found in (diagnostic;
-- a second release of the same member returns 0 and changes nothing).

local registry = KEYS[1]
local member = ARGV[1]
local released = 0

for i = 2, #KEYS do
  if redis.call('ZREM', KEYS[i], member) > 0 then
    released = released + 1
  end
  if redis.call('ZCARD', KEYS[i]) == 0 then
    redis.call('SREM', registry, KEYS[i])
  end
end
return released
