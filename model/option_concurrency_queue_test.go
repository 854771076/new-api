package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOptionValueRejectsInvalidConcurrencyQueueOptions(t *testing.T) {
	invalid := map[string][]string{
		"concurrency.queue_enabled":          {"", "TRUE", "1", "yes"},
		"concurrency.queue_depth_ratio":      {"-1", "65", "1.5", "x", ""},
		"concurrency.queue_depth_cap":        {"0", "1025", "x", ""},
		"concurrency.queue_max_wait_seconds": {"-1", "61", "x", ""},
	}
	for key, values := range invalid {
		for _, value := range values {
			t.Run(key+"="+value, func(t *testing.T) {
				assert.Error(t, validateOptionValue(key, value))
			})
		}
	}

	valid := []struct {
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
	for _, tc := range valid {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			require.NoError(t, validateOptionValue(tc.key, tc.value))
		})
	}
}

// GET /api/option/ 读的是 common.OptionMap，所以这 4 个 key 必须在
// InitOptionMap 里带默认值注册。
func TestInitOptionMapRegistersConcurrencyQueueDefaults(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	backup := make(map[string]string, len(common.OptionMap))
	for k, v := range common.OptionMap {
		backup[k] = v
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = backup
		common.OptionMapRWMutex.Unlock()
	})

	InitOptionMap()

	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	require.NotNil(t, common.OptionMap)
	for key, want := range map[string]string{
		"concurrency.queue_enabled":          "true",
		"concurrency.queue_depth_ratio":      "4",
		"concurrency.queue_depth_cap":        "128",
		"concurrency.queue_max_wait_seconds": "10",
	} {
		assert.Equal(t, want, common.OptionMap[key], "option %s must be registered with its default", key)
	}
}

// updateOptionMap 是 loadOptionsFromDatabase 与 UpdateOption 的共同分发点：
// 它必须把 concurrency.* 落到 setting 的原子状态上，且校验失败要返回错误。
func TestUpdateOptionMapDispatchesConcurrencyQueueOptions(t *testing.T) {
	restore := setting.ConcurrencyQueueOptionDefaults()
	t.Cleanup(func() {
		for key, value := range restore {
			require.NoError(t, setting.UpdateConcurrencyQueueOption(key, value))
		}
	})

	require.NoError(t, updateOptionMap("concurrency.queue_enabled", "false"))
	require.NoError(t, updateOptionMap("concurrency.queue_depth_ratio", "7"))
	require.NoError(t, updateOptionMap("concurrency.queue_depth_cap", "32"))
	require.NoError(t, updateOptionMap("concurrency.queue_max_wait_seconds", "5"))

	assert.False(t, setting.GetConcurrencyQueueEnabled())
	assert.Equal(t, 7, setting.GetConcurrencyQueueDepthRatio())
	assert.Equal(t, 32, setting.GetConcurrencyQueueDepthCap())
	assert.Equal(t, 5*time.Second, setting.GetConcurrencyQueueMaxWait())

	assert.Error(t, updateOptionMap("concurrency.queue_depth_ratio", "65"),
		"a rejected value must surface as an error so the admin sees it")
	assert.Equal(t, 7, setting.GetConcurrencyQueueDepthRatio())
}
