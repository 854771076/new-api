package setting

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"
)

// 网关内有界等待队列的 4 个 option key（与 PUT /api/option/ 的前后端契约
// 严格一致，key / 默认值 / 取值范围都不得改动）。
const (
	ConcurrencyQueueEnabledKey        = "concurrency.queue_enabled"
	ConcurrencyQueueDepthRatioKey     = "concurrency.queue_depth_ratio"
	ConcurrencyQueueDepthCapKey       = "concurrency.queue_depth_cap"
	ConcurrencyQueueMaxWaitSecondsKey = "concurrency.queue_max_wait_seconds"
)

// 出厂默认值：ratio=4（激进）、cap=128、最多等 10s、默认开启。
const (
	DefaultConcurrencyQueueEnabled        = true
	DefaultConcurrencyQueueDepthRatio     = 4
	DefaultConcurrencyQueueDepthCap       = 128
	DefaultConcurrencyQueueMaxWaitSeconds = 10
)

// 合法范围（闭区间）：ratio 0–64（0 = 排队深度 0 = 等效关闭排队），
// cap 1–1024，maxWait 0–60（0 = 不等待立即拒绝）。
const (
	MaxConcurrencyQueueDepthRatio     = 64
	MinConcurrencyQueueDepthCap       = 1
	MaxConcurrencyQueueDepthCap       = 1024
	MaxConcurrencyQueueMaxWaitSeconds = 60
)

var (
	concurrencyQueueEnabled        atomic.Bool
	concurrencyQueueDepthRatio     atomic.Int64
	concurrencyQueueDepthCap       atomic.Int64
	concurrencyQueueMaxWaitSeconds atomic.Int64
)

func init() {
	restoreConcurrencyQueueDefaults()
}

// restoreConcurrencyQueueDefaults 把 4 个原子量拉回出厂默认值。
// init 与测试的 Setup/Cleanup 都走它，保证默认值只有一处定义。
func restoreConcurrencyQueueDefaults() {
	concurrencyQueueEnabled.Store(DefaultConcurrencyQueueEnabled)
	concurrencyQueueDepthRatio.Store(DefaultConcurrencyQueueDepthRatio)
	concurrencyQueueDepthCap.Store(DefaultConcurrencyQueueDepthCap)
	concurrencyQueueMaxWaitSeconds.Store(DefaultConcurrencyQueueMaxWaitSeconds)
}

// ConcurrencyQueueOptionDefaults 返回 InitOptionMap 需要注册到
// common.OptionMap 的 4 个默认字符串值；GET /api/option/ 因此能读到它们。
func ConcurrencyQueueOptionDefaults() map[string]string {
	return map[string]string{
		ConcurrencyQueueEnabledKey:        strconv.FormatBool(DefaultConcurrencyQueueEnabled),
		ConcurrencyQueueDepthRatioKey:     strconv.Itoa(DefaultConcurrencyQueueDepthRatio),
		ConcurrencyQueueDepthCapKey:       strconv.Itoa(DefaultConcurrencyQueueDepthCap),
		ConcurrencyQueueMaxWaitSecondsKey: strconv.Itoa(DefaultConcurrencyQueueMaxWaitSeconds),
	}
}

// ValidateConcurrencyQueueOption 校验一个队列 option 的字符串值。
// 未知 key 也报错，避免并发相关配置被静默接受后不生效。
func ValidateConcurrencyQueueOption(key string, value string) error {
	switch key {
	case ConcurrencyQueueEnabledKey:
		if value != "true" && value != "false" {
			return fmt.Errorf("%s must be \"true\" or \"false\"", key)
		}
		return nil
	case ConcurrencyQueueDepthRatioKey:
		return validateIntInRange(key, value, 0, MaxConcurrencyQueueDepthRatio)
	case ConcurrencyQueueDepthCapKey:
		return validateIntInRange(key, value, MinConcurrencyQueueDepthCap, MaxConcurrencyQueueDepthCap)
	case ConcurrencyQueueMaxWaitSecondsKey:
		return validateIntInRange(key, value, 0, MaxConcurrencyQueueMaxWaitSeconds)
	default:
		return fmt.Errorf("unknown concurrency queue option: %s", key)
	}
}

func validateIntInRange(key string, value string, min, max int) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s must be an integer", key)
	}
	if n < min || n > max {
		return fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return nil
}

// UpdateConcurrencyQueueOption 校验并原子更新一个队列 option。
// 校验失败时不写状态，直接返回错误 —— model.UpdateOption 的 PUT 路径与
// loadOptionsFromDatabase 的 updateOptionMap 分发都靠这层兜底。
func UpdateConcurrencyQueueOption(key string, value string) error {
	if err := ValidateConcurrencyQueueOption(key, value); err != nil {
		return err
	}
	switch key {
	case ConcurrencyQueueEnabledKey:
		concurrencyQueueEnabled.Store(value == "true")
	case ConcurrencyQueueDepthRatioKey:
		n, _ := strconv.Atoi(value)
		concurrencyQueueDepthRatio.Store(int64(n))
	case ConcurrencyQueueDepthCapKey:
		n, _ := strconv.Atoi(value)
		concurrencyQueueDepthCap.Store(int64(n))
	case ConcurrencyQueueMaxWaitSecondsKey:
		n, _ := strconv.Atoi(value)
		concurrencyQueueMaxWaitSeconds.Store(int64(n))
	}
	return nil
}

// GetConcurrencyQueueEnabled 报告排队是否开启（关闭时走既有 429 路径）。
func GetConcurrencyQueueEnabled() bool {
	return concurrencyQueueEnabled.Load()
}

// GetConcurrencyQueueDepthRatio 返回每个 scope 的排队深度放大系数。
func GetConcurrencyQueueDepthRatio() int {
	return int(concurrencyQueueDepthRatio.Load())
}

// GetConcurrencyQueueDepthCap 返回每个 scope 排队深度的上限截断值。
func GetConcurrencyQueueDepthCap() int {
	return int(concurrencyQueueDepthCap.Load())
}

// GetConcurrencyQueueMaxWait 返回单个请求在队列里最长的等待时间；
// 0 表示不等待，直接拒绝。
func GetConcurrencyQueueMaxWait() time.Duration {
	return time.Duration(concurrencyQueueMaxWaitSeconds.Load()) * time.Second
}
