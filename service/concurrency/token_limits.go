package concurrency

import (
	"fmt"
	"strconv"
	"strings"
)

// Entry classification for the shared "model=limit" CSV tokenizer. The same
// classes and reason codes drive the read-side parser, the write-boundary
// validator and the TypeScript mirror's fixture (TUI-02 BCD conclusion: one
// parse source, no duplicated semantics).
const (
	EntryClassAccept = "accept"
	EntryClassEmpty  = "empty"
	EntryClassDefect = "defect"
)

// Reason codes for defect segments (exported so controller errors and the TS
// mirror surface the identical wording).
const (
	ReasonMissingSeparator = "missing_separator"  // entry has no '=', or '=' is first/last
	ReasonEmptyModel       = "empty_model"        // model side of '=' trims to empty
	ReasonInvalidLimit     = "invalid_limit"      // limit side is not an integer
	ReasonNonPositiveLimit = "non_positive_limit" // limit parses but is <= 0
)

// ParseEntry is one classified CSV segment. Raw is the segment after outer
// TrimSpace (what the UI preview shows and error messages quote); Model and
// Limit are only populated for accept segments; Reason only for defect ones.
type ParseEntry struct {
	Raw    string
	Model  string
	Limit  int
	Class  string
	Reason string
}

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

// EffectiveLimit returns the model-scoped cap when set, otherwise the global
// cap. NOTE: this is a single-value display/compat helper — the enforcement
// path does NOT use it. buildScopes acquires both buckets and the stricter of
// min(model, global) governs (ENF-02). A return value of 0 means "no limit"
// for the selected axis.
func (l TokenConcurrencyLimits) EffectiveLimit(model string) int {
	if v := l.LimitForModel(model); v > 0 {
		return v
	}
	return l.Global
}

// ParseEntries is the single parse source for the per-model CSV: it splits,
// trims and classifies every segment as accept / empty (pure-whitespace,
// tolerated) / defect (with a reason code). Classification boundaries are the
// read-side parser's historic ones: a defect entry exists, it is never
// invented — consumers decide what to do with it (the parser drops defects,
// the write-boundary validator rejects on them).
func ParseEntries(byModelCSV string) []ParseEntry {
	segments := strings.Split(byModelCSV, ",")
	entries := make([]ParseEntry, 0, len(segments))
	for _, segment := range segments {
		entry := strings.TrimSpace(segment)
		parsed := ParseEntry{Raw: entry, Class: EntryClassEmpty}
		if entry == "" {
			entries = append(entries, parsed)
			continue
		}
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 || eq >= len(entry)-1 {
			// Missing, leading or trailing '=' — defect (read side drops it).
			parsed.Class, parsed.Reason = EntryClassDefect, ReasonMissingSeparator
			entries = append(entries, parsed)
			continue
		}
		model := strings.TrimSpace(entry[:eq])
		valStr := strings.TrimSpace(entry[eq+1:])
		if model == "" {
			// Defensive parity with the read-side model check (unreachable
			// after the outer TrimSpace, kept so Go and the TS mirror agree).
			parsed.Class, parsed.Reason = EntryClassDefect, ReasonEmptyModel
			entries = append(entries, parsed)
			continue
		}
		n, err := strconv.Atoi(valStr)
		if err != nil {
			parsed.Class, parsed.Reason = EntryClassDefect, ReasonInvalidLimit
			entries = append(entries, parsed)
			continue
		}
		if n <= 0 {
			// Zero/negative per-model limits mean "same as omitting the entry"
			// at read time; the write boundary rejects them instead of
			// silently dropping (SC: no silent normalization).
			parsed.Class, parsed.Reason = EntryClassDefect, ReasonNonPositiveLimit
			entries = append(entries, parsed)
			continue
		}
		parsed.Class, parsed.Model, parsed.Limit = EntryClassAccept, model, n
		entries = append(entries, parsed)
	}
	return entries
}

// ValidateConcurrencyLimits enforces the write-boundary policy for explicitly
// submitted concurrency fields (02-REVIEW IN-10 Fix): a negative global cap
// (0 = unlimited stays legal) and any defect CSV segment are rejected with an
// error naming the offending raw entry and its reason code — bad entries are
// never silently normalized or dropped before persisting. Pure-whitespace
// segments (trailing commas) and repeated models (last write wins, current
// semantics) are accepted. Read-side callers keep their defensive skip via
// ParseTokenConcurrencyLimits; this gate only applies at the write boundary.
func ValidateConcurrencyLimits(global int, byModelCSV string) error {
	if global < 0 {
		return fmt.Errorf("invalid concurrency_limit: %d (must be >= 0, 0 means unlimited)", global)
	}
	for _, entry := range ParseEntries(byModelCSV) {
		if entry.Class == EntryClassDefect {
			return fmt.Errorf("invalid concurrency_limit_by_model entry %q: %s", entry.Raw, entry.Reason)
		}
	}
	return nil
}

// ParseTokenConcurrencyLimits parses the two columns and returns a ready
// struct, consuming the shared ParseEntries tokenizer and keeping only accept
// segments — every entry's accept/drop decision is identical to the historic
// implementation (defective entries are dropped, not fatal). global <= 0 is
// treated as "unlimited" and Global field stays 0 so the caller can
// distinguish "unset" from "set to 1".
func ParseTokenConcurrencyLimits(global int, byModelCSV string) TokenConcurrencyLimits {
	out := TokenConcurrencyLimits{
		Global:  global,
		ByModel: map[string]int{},
	}
	for _, entry := range ParseEntries(byModelCSV) {
		if entry.Class != EntryClassAccept {
			continue
		}
		out.ByModel[entry.Model] = entry.Limit
	}
	return out
}

// DecodeTokenConcurrencyLimits is an alias kept for the contract tests in the
// same package; naming chosen so the test file does not depend on the parser
// internals.
func DecodeTokenConcurrencyLimits(global int, byModelCSV string) TokenConcurrencyLimits {
	return ParseTokenConcurrencyLimits(global, byModelCSV)
}
