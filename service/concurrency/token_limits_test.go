package concurrency

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
