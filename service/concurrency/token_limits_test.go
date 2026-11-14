package concurrency

import (
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokenLimitParseCase mirrors one row of the shared Go/TS fixture at
// testdata/token_limit_parse_cases.json. Task 02-13-02's TypeScript mirror
// consumes the same file, so the two implementations cannot drift without
// breaking one of the two suites (double-sided lock, TUI-02 BCD conclusion).
type tokenLimitParseCase struct {
	Input      string                       `json:"input"`
	MustReject bool                         `json:"mustReject"`
	Segments   []tokenLimitParseCaseSegment `json:"segments"`
}

// tokenLimitParseCaseSegment is one classified CSV segment: raw (the entry
// after outer TrimSpace), class (accept/empty/defect), model/limit (accept
// only) and reason (defect only).
type tokenLimitParseCaseSegment struct {
	Raw    string `json:"raw"`
	Class  string `json:"class"`
	Model  string `json:"model"`
	Limit  int    `json:"limit"`
	Reason string `json:"reason"`
}

// loadTokenLimitParseCases reads the shared fixture. A missing fixture is a
// hard failure (never a skip): the file is the cross-stack semantic contract.
func loadTokenLimitParseCases(t *testing.T) []tokenLimitParseCase {
	t.Helper()

	data, err := os.ReadFile("testdata/token_limit_parse_cases.json")
	require.NoError(t, err, "shared Go/TS fixture testdata/token_limit_parse_cases.json must exist")
	var cases []tokenLimitParseCase
	require.NoError(t, common.Unmarshal(data, &cases))
	require.NotEmpty(t, cases)
	return cases
}

// TestParseEntriesFixtureClassification locks ParseEntries segment-by-segment
// classification (raw/class/model/limit/reason) against the shared fixture.
func TestParseEntriesFixtureClassification(t *testing.T) {
	for i, tc := range loadTokenLimitParseCases(t) {
		t.Run(fmt.Sprintf("case_%02d_%s", i, tc.Input), func(t *testing.T) {
			entries := ParseEntries(tc.Input)
			require.Len(t, entries, len(tc.Segments))
			for j, want := range tc.Segments {
				got := entries[j]
				assert.Equal(t, want.Raw, got.Raw, "segment %d raw", j)
				assert.Equal(t, want.Class, got.Class, "segment %d class (raw=%q)", j, want.Raw)
				assert.Equal(t, want.Model, got.Model, "segment %d model (raw=%q)", j, want.Raw)
				assert.Equal(t, want.Limit, got.Limit, "segment %d limit (raw=%q)", j, want.Raw)
				assert.Equal(t, want.Reason, got.Reason, "segment %d reason (raw=%q)", j, want.Raw)
			}
		})
	}
}

// TestValidateConcurrencyLimitsFixturePolicy locks the write-boundary policy:
// mustReject iff at least one defect segment exists, and a rejecting error
// names the first defect's raw entry plus its reason code. Non-negative global
// caps (0 = unlimited) are always accepted.
func TestValidateConcurrencyLimitsFixturePolicy(t *testing.T) {
	for i, tc := range loadTokenLimitParseCases(t) {
		t.Run(fmt.Sprintf("case_%02d_%s", i, tc.Input), func(t *testing.T) {
			err := ValidateConcurrencyLimits(0, tc.Input)
			if !tc.MustReject {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var firstDefect *tokenLimitParseCaseSegment
			for j := range tc.Segments {
				if tc.Segments[j].Class == "defect" {
					firstDefect = &tc.Segments[j]
					break
				}
			}
			require.NotNil(t, firstDefect, "mustReject case must contain a defect segment")
			assert.Contains(t, err.Error(), firstDefect.Raw, "error must name the offending entry")
			assert.Contains(t, err.Error(), firstDefect.Reason, "error must name the reason code")
		})
	}

	t.Run("negative global limit is rejected", func(t *testing.T) {
		err := ValidateConcurrencyLimits(-1, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "concurrency_limit")
		assert.Contains(t, err.Error(), "-1")
	})

	t.Run("zero and positive global limits are accepted", func(t *testing.T) {
		require.NoError(t, ValidateConcurrencyLimits(0, ""))
		require.NoError(t, ValidateConcurrencyLimits(5, ""))
	})
}

// TestTokenConcurrencyAccessor_DefaultZeroIsUnlimited: brand-new token has zero limits = unlimited.
func TestTokenConcurrencyAccessor_DefaultZeroIsUnlimited(t *testing.T) {
	limits := DecodeTokenConcurrencyLimits(0, "")
	assert.Equal(t, 0, limits.Global, "global default 0 must be unlimited")
	assert.Empty(t, limits.ByModel)
	assert.False(t, limits.HasGlobalCap())
}

// TestTokenConcurrencyAccessor_GlobalOnly.
func TestTokenConcurrencyAccessor_GlobalOnly(t *testing.T) {
	limits := DecodeTokenConcurrencyLimits(5, "")
	assert.True(t, limits.HasGlobalCap())
	assert.Equal(t, 5, limits.Global)
	assert.Empty(t, limits.ByModel)
}

// TestTokenConcurrencyAccessor_ModelScopedCSV.
func TestTokenConcurrencyAccessor_ModelScopedCSV(t *testing.T) {
	limits := DecodeTokenConcurrencyLimits(10, "gpt-4o=3,claude-sonnet=2")
	assert.Equal(t, 10, limits.Global)
	assert.Equal(t, 3, limits.LimitForModel("gpt-4o"))
	assert.Equal(t, 2, limits.LimitForModel("claude-sonnet"))
	assert.Equal(t, 0, limits.LimitForModel("unlisted-model"), "LimitForModel only reflects explicit model entry; fall-back is EffectiveLimit's job")
	assert.Equal(t, 10, limits.EffectiveLimit("unlisted-model"), "EffectiveLimit falls back to global for unlisted")
}

// TestTokenConcurrencyAccessor_InvalidCSVNoPanic.
func TestTokenConcurrencyAccessor_InvalidCSVNoPanic(t *testing.T) {
	limits := DecodeTokenConcurrencyLimits(0, ",, =,=value,foo,model-ok=5")
	require.NotNil(t, limits)
	// Only the well-formed entry should survive.
	assert.Equal(t, 5, limits.LimitForModel("model-ok"))
	assert.Equal(t, 0, limits.LimitForModel("foo"))
}

// TestTokenConcurrencyAccessor_NegativeOrZeroSkipped.
func TestTokenConcurrencyAccessor_NegativeOrZeroSkipped(t *testing.T) {
	limits := DecodeTokenConcurrencyLimits(0, "good=4,zero=0,minus=-3")
	assert.Equal(t, 4, limits.LimitForModel("good"))
	assert.Equal(t, 0, limits.LimitForModel("zero"))
	assert.Equal(t, 0, limits.LimitForModel("minus"))
}

// TestTokenConcurrencyAccessor_EffectiveLimit_PrefersModelOverGlobal.
func TestTokenConcurrencyAccessor_EffectiveLimit_PrefersModelOverGlobal(t *testing.T) {
	limits := DecodeTokenConcurrencyLimits(5, "gpt-4o=2")
	// For gpt-4o: explicit model cap (2) wins.
	assert.Equal(t, 2, limits.EffectiveLimit("gpt-4o"))
	// For unlisted: fall back to global (5).
	assert.Equal(t, 5, limits.EffectiveLimit("claude-sonnet"))
	// Zero global + no model entry: unlimited (0).
	limits2 := DecodeTokenConcurrencyLimits(0, "")
	assert.Equal(t, 0, limits2.EffectiveLimit("anything"))
}
