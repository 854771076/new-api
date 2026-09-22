// Package concurrency implements per-user / per-API-key in-flight concurrency
// limits for new-api-monster. The package is deliberately split into:
//
//   - ScopeKey + MemoryLimiter (this file): pure Go, no external dependencies.
//     Drives unit tests and the dev-mode fallback when Redis is offline.
//   - RedisLimiter (limiter_redis.go): production path using INCR/DECR.
//   - token_limits.go: CSV decoder for the Token.ConcurrencyLimitByModel column.
//
// Wire-up happens in middleware/concurrency.go (request gate) and
// controller/concurrency.go (admin snapshot).
package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ScopeKind enumerates the two subject axes the gateway can throttle on.
type ScopeKind uint8

const (
	ScopeKindKey  ScopeKind = 1
	ScopeKindUser ScopeKind = 2
)

// ScopeKey identifies a throttle bucket. Wire format: "<key|user>:<id>:<model|*>"
// where "*" denotes the global (any-model) bucket for that subject.
type ScopeKey string

// ErrConcurrencyExceeded is the sentinel error returned by Acquire when the
// scope's current in-flight count already equals its limit. Callers must use
// errors.Is(err, ErrConcurrencyExceeded), not direct equality, because future
// transports may wrap the error with transport-specific context.
var ErrConcurrencyExceeded = errors.New("concurrency limit exceeded")

// Kind returns which axis the scope addresses.
func (s ScopeKey) Kind() ScopeKind {
	if strings.HasPrefix(string(s), "user:") {
		return ScopeKindUser
	}
	return ScopeKindKey
}

// SubjectID returns the user or token id encoded in the scope.
func (s ScopeKey) SubjectID() int {
	parts := strings.SplitN(string(s), ":", 3)
	if len(parts) < 2 {
		return 0
	}
	var id int
	_, _ = fmt.Sscanf(parts[1], "%d", &id)
	return id
}

// Model returns the model name or "*" for the global bucket.
func (s ScopeKey) Model() string {
	parts := strings.SplitN(string(s), ":", 3)
	if len(parts) < 3 {
		return "*"
	}
	return parts[2]
}

// IsModelScoped reports whether the scope is bound to a specific model.
func (s ScopeKey) IsModelScoped() bool {
	return s.Model() != "*"
}

// String renders the scope for hashing / Redis keys.
func (s ScopeKey) String() string { return string(s) }

// ScopeSnapshot is one row of the admin snapshot endpoint.
type ScopeSnapshot struct {
	Scope       ScopeKey `json:"scope"`
	Kind        string   `json:"kind"`
	SubjectID   int      `json:"subjectId"`
	Model       string   `json:"model"`
	Current     int      `json:"current"`
	Limit       int      `json:"limit"`
	Utilization float64  `json:"utilization"`
	LastUpdated int64    `json:"lastUpdatedAt"`
}

// Limiter is the interface every backing store must satisfy.
type Limiter interface {
	// Acquire increments the in-flight count for scope. If limit is 0 or
	// unset, Acquire always succeeds. On success the returned release func
	// must be called exactly once when the request finishes; double-release
	// is a no-op, missed release leaks a slot.
	Acquire(ctx context.Context, scope ScopeKey) (release func(), err error)
	// SetLimit adjusts the cap for scope. limit <= 0 means unlimited.
	SetLimit(ctx context.Context, scope ScopeKey, limit int) error
	// Snapshot returns the current state of every known scope, ordered by
	// ScopeKey for stable rendering.
	Snapshot(ctx context.Context) ([]ScopeSnapshot, error)
}

// MemoryLimiter is a goroutine-safe in-process Limiter used as the test double
// and the dev-mode fallback when Redis is unavailable. It is intentionally
// small: a sync.RWMutex, two maps. When Redis is online, callers should swap in
// RedisLimiter (see limiter_redis.go).
type MemoryLimiter struct {
	mu      sync.RWMutex
	limits  map[string]int
	holders map[string]int
	updated map[string]int64
}

// NewMemoryLimiter returns a fresh, empty MemoryLimiter.
func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{
		limits:  map[string]int{},
		holders: map[string]int{},
		updated: map[string]int64{},
	}
}

// SetLimit records limit for future pulls. limit <= 0 means unlimited and the
// scope is treated as effectively absent from the limiter.
func (m *MemoryLimiter) SetLimit(_ context.Context, scope ScopeKey, limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		delete(m.limits, scope.String())
		return nil
	}
	m.limits[scope.String()] = limit
	return nil
}

// Acquire checks the in-flight count for this subject and either reserves a slot
// (returning a release func) or returns ErrConcurrencyExceeded.
func (m *MemoryLimiter) Acquire(_ context.Context, scope ScopeKey) (func(), error) {
	m.mu.Lock()
	key := scope.String()
	limit, hasLimit := m.limits[key]
	if !hasLimit || limit <= 0 {
		m.holders[key]++
		m.updated[key] = nowUnix()
		m.mu.Unlock()
		return m.releaseFunc(key), nil
	}
	if m.holders[key] >= limit {
		m.mu.Unlock()
		return nil, ErrConcurrencyExceeded
	}
	m.holders[key]++
	m.updated[key] = nowUnix()
	m.mu.Unlock()
	return m.releaseFunc(key), nil
}

// releaseFunc returns a closure that decrements the in-flight count for key.
// It is idempotent: a second call is a no-op so the contract "call exactly
// once" is forgiving on misuse. Real bugs should not rely on this.
func (m *MemoryLimiter) releaseFunc(key string) func() {
	var done bool
	return func() {
		if done {
			return
		}
		done = true
		m.mu.Lock()
		if m.holders[key] > 0 {
			m.holders[key]--
		}
		if m.holders[key] == 0 {
			delete(m.holders, key)
		}
		m.updated[key] = nowUnix()
		m.mu.Unlock()
	}
}

// Snapshot returns current state for every known scope in deterministic order
// (by ScopeKey string).
func (m *MemoryLimiter) Snapshot(_ context.Context) ([]ScopeSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	scopes := make([]ScopeSnapshot, 0, len(m.limits)+len(m.holders))
	seen := map[string]struct{}{}
	for k, lim := range m.limits {
		cur := m.holders[k]
		scopes = append(scopes, scopeRow(ScopeKey(k), cur, lim, m.updated[k]))
		seen[k] = struct{}{}
	}
	for k, cur := range m.holders {
		if _, ok := seen[k]; ok {
			continue
		}
		scopes = append(scopes, scopeRow(ScopeKey(k), cur, 0, m.updated[k]))
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Scope < scopes[j].Scope })
	return scopes, nil
}

// nowUnix is overridable for tests; see limiter_clock_test.go for the seam.
var nowUnix = func() int64 { return unixMilli() }

// unixMilli uses the runtime clock; replaced by tests with a fixed value.
var unixMilli = func() int64 {
	return wallClockMillis()
}

func scopeRow(s ScopeKey, current, limit int, updated int64) ScopeSnapshot {
	util := 0.0
	if limit > 0 {
		util = float64(current) / float64(limit)
	}
	kind := "key"
	if s.Kind() == ScopeKindUser {
		kind = "user"
	}
	return ScopeSnapshot{
		Scope:       s,
		Kind:        kind,
		SubjectID:   s.SubjectID(),
		Model:       s.Model(),
		Current:     current,
		Limit:       limit,
		Utilization: util,
		LastUpdated: updated,
	}
}

// wallClockMillis returns wall-clock milliseconds. Defined as a var so tests
// can swap in a deterministic clock without touching time.Now directly.
var wallClockMillis = func() int64 {
	return realWallClockMillis()
}
