package concurrency

import (
	"strconv"
	"strings"
)

// TokenConcurrencyLimits decodes Token.ConcurrencyLimit (global cap) and
// Token.ConcurrencyLimitByModel (CSV like "gpt-4o=3,claude-sonnet=2") into a
// queryable structure. Decoding is intentionally defensive: malformed entries
// are dropped, not fatal, so a bad CSV does not lock out an admin user.
type TokenConcurrencyLimits struct {
	Global  int
	ByModel map[string]int
}

// HasGlobalCap reports whether the global cap is set (>0).
func (l TokenConcurrencyLimits) HasGlobalCap() bool { return l.Global > 0 }

// LimitForModel returns the per-model cap for model, or 0 if not configured.
func (l TokenConcurrencyLimits) LimitForModel(model string) int {
	if model == "" {
		return 0
	}
	return l.ByModel[model]
}

// EffectiveLimit returns the cap that should govern an in-flight check for
// model. Model-scoped caps take precedence over the global cap. A return
// value of 0 means "no limit" and the caller must short-circuit to allow.
func (l TokenConcurrencyLimits) EffectiveLimit(model string) int {
	if v := l.LimitForModel(model); v > 0 {
		return v
	}
	return l.Global
}

// DecodeTokenConcurrencyLimits parses the two columns and returns a ready
// struct. global <= 0 is treated as "unlimited" and Global field stays 0 so the
// caller can distinguish "unset" from "set to 1".
func ParseTokenConcurrencyLimits(global int, byModelCSV string) TokenConcurrencyLimits {
	out := TokenConcurrencyLimits{
		Global:  global,
		ByModel: map[string]int{},
	}
	if byModelCSV == "" {
		return out
	}
	for _, entry := range strings.Split(byModelCSV, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 || eq >= len(entry)-1 {
			// Missing or trailing '=' or leading '=' — drop.
			continue
		}
		model := strings.TrimSpace(entry[:eq])
		valStr := strings.TrimSpace(entry[eq+1:])
		if model == "" {
			continue
		}
		n, err := strconv.Atoi(valStr)
		if err != nil || n <= 0 {
			continue
		}
		out.ByModel[model] = n
	}
	return out
}

// DecodeTokenConcurrencyLimits is an alias kept for the contract tests in the
// same package; naming chosen so the test file does not depend on the parser
// internals.
func DecodeTokenConcurrencyLimits(global int, byModelCSV string) TokenConcurrencyLimits {
	return ParseTokenConcurrencyLimits(global, byModelCSV)
}
