package setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetConcurrencyQueueForTest 把 4 个队列 option 恢复成出厂默认，
// 每个测试 Setup + t.Cleanup 各调一次，避免用例之间互相污染。
func resetConcurrencyQueueForTest(t *testing.T) {
	t.Helper()
	for key, value := range ConcurrencyQueueOptionDefaults() {
		require.NoError(t, UpdateConcurrencyQueueOption(key, value))
	}
	t.Cleanup(func() {
		for key, value := range ConcurrencyQueueOptionDefaults() {
			require.NoError(t, UpdateConcurrencyQueueOption(key, value))
		}
	})
}

func TestConcurrencyQueueOptionDefaults(t *testing.T) {
	resetConcurrencyQueueForTest(t)

	defaults := ConcurrencyQueueOptionDefaults()
	require.Equal(t, map[string]string{
		"concurrency.queue_enabled":          "true",
		"concurrency.queue_depth_ratio":      "4",
		"concurrency.queue_depth_cap":        "128",
		"concurrency.queue_max_wait_seconds": "10",
	}, defaults)

	assert.True(t, GetConcurrencyQueueEnabled())
	assert.Equal(t, 4, GetConcurrencyQueueDepthRatio())
	assert.Equal(t, 128, GetConcurrencyQueueDepthCap())
	assert.Equal(t, 10*time.Second, GetConcurrencyQueueMaxWait())
}

func TestValidateConcurrencyQueueOptionAcceptsBoundaries(t *testing.T) {
	resetConcurrencyQueueForTest(t)

	cases := []struct {
		key   string
		value string
	}{
		{"concurrency.queue_enabled", "true"},
		{"concurrency.queue_enabled", "false"},
		{"concurrency.queue_depth_ratio", "0"},
		{"concurrency.queue_depth_ratio", "64"},
		{"concurrency.queue_depth_cap", "1"},
		{"concurrency.queue_depth_cap", "1024"},
		{"concurrency.queue_max_wait_seconds", "0"},
		{"concurrency.queue_max_wait_seconds", "60"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			assert.NoError(t, ValidateConcurrencyQueueOption(tc.key, tc.value))
		})
	}
}

func TestValidateConcurrencyQueueOptionRejectsOutOfRange(t *testing.T) {
	resetConcurrencyQueueForTest(t)

	cases := []struct {
		key   string
		value string
	}{
		{"concurrency.queue_enabled", ""},
		{"concurrency.queue_enabled", "TRUE"},
		{"concurrency.queue_enabled", "1"},
		{"concurrency.queue_enabled", "yes"},
		{"concurrency.queue_depth_ratio", "-1"},
		{"concurrency.queue_depth_ratio", "65"},
		{"concurrency.queue_depth_ratio", "1.5"},
		{"concurrency.queue_depth_ratio", "x"},
		{"concurrency.queue_depth_ratio", ""},
		{"concurrency.queue_depth_cap", "0"},
		{"concurrency.queue_depth_cap", "1025"},
		{"concurrency.queue_depth_cap", "x"},
		{"concurrency.queue_depth_cap", ""},
		{"concurrency.queue_max_wait_seconds", "-1"},
		{"concurrency.queue_max_wait_seconds", "61"},
		{"concurrency.queue_max_wait_seconds", "x"},
		{"concurrency.queue_max_wait_seconds", ""},
		{"concurrency.queue_unknown", "1"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			assert.Error(t, ValidateConcurrencyQueueOption(tc.key, tc.value))
		})
	}
}

func TestUpdateConcurrencyQueueOptionAppliesToGetters(t *testing.T) {
	resetConcurrencyQueueForTest(t)

	require.NoError(t, UpdateConcurrencyQueueOption("concurrency.queue_enabled", "false"))
	require.NoError(t, UpdateConcurrencyQueueOption("concurrency.queue_depth_ratio", "8"))
	require.NoError(t, UpdateConcurrencyQueueOption("concurrency.queue_depth_cap", "16"))
	require.NoError(t, UpdateConcurrencyQueueOption("concurrency.queue_max_wait_seconds", "3"))

	assert.False(t, GetConcurrencyQueueEnabled())
	assert.Equal(t, 8, GetConcurrencyQueueDepthRatio())
	assert.Equal(t, 16, GetConcurrencyQueueDepthCap())
	assert.Equal(t, 3*time.Second, GetConcurrencyQueueMaxWait())
}

func TestUpdateConcurrencyQueueOptionRejectsInvalidWithoutChangingState(t *testing.T) {
	resetConcurrencyQueueForTest(t)

	require.NoError(t, UpdateConcurrencyQueueOption("concurrency.queue_depth_ratio", "8"))
	require.Error(t, UpdateConcurrencyQueueOption("concurrency.queue_depth_ratio", "65"))
	assert.Equal(t, 8, GetConcurrencyQueueDepthRatio(), "a rejected value must not reach the runtime state")

	require.NoError(t, UpdateConcurrencyQueueOption("concurrency.queue_depth_cap", "16"))
	require.Error(t, UpdateConcurrencyQueueOption("concurrency.queue_depth_cap", "0"))
	assert.Equal(t, 16, GetConcurrencyQueueDepthCap())

	require.Error(t, UpdateConcurrencyQueueOption("concurrency.queue_enabled", "maybe"))
	assert.True(t, GetConcurrencyQueueEnabled())

	require.NoError(t, UpdateConcurrencyQueueOption("concurrency.queue_max_wait_seconds", "0"))
	require.Error(t, UpdateConcurrencyQueueOption("concurrency.queue_max_wait_seconds", "61"))
	assert.Equal(t, time.Duration(0), GetConcurrencyQueueMaxWait(),
		"0 seconds means no wait at all — the request is rejected immediately")
}
