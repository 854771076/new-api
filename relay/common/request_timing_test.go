package common

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	rootcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTimingTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// snapshotInt64 reads one numeric snapshot entry; the snapshot map is built
// from atomic.Int64 loads so every entry is an int64.
func snapshotInt64(t *testing.T, snapshot map[string]any, key string) int64 {
	t.Helper()
	value, ok := snapshot[key]
	require.True(t, ok, "snapshot must contain %s", key)
	number, ok := value.(int64)
	require.True(t, ok, "snapshot value %s must be an int64", key)
	return number
}

// 场景 5/边界:采集载体刚建好时,各分段耗时都为 0,但尝试次数与三个超时上限
// 始终有值 —— 管理员据此判断"这段等待是不是在预算内"。
func TestRequestTimingSnapshotDefaults(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	snapshot := timing.Snapshot()

	for _, key := range []string{
		TimingKeyQueueWaitMs,
		TimingKeyChannelSelectMs,
		TimingKeyUpstreamHeaderMs,
		TimingKeyUpstreamBodyMs,
		TimingKeyUpstreamFirstByteMs,
		TimingKeyUpstreamStreamMs,
		TimingKeySettleMs,
		TimingKeyAttempts,
		TimingKeyUpstreamHeaderTimeoutS,
		TimingKeyStreamingTimeoutS,
		TimingKeyRelayTimeoutS,
	} {
		require.Contains(t, snapshot, key)
	}

	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyQueueWaitMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyChannelSelectMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyUpstreamHeaderMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyUpstreamBodyMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyUpstreamFirstByteMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyUpstreamStreamMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeySettleMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyAttempts))

	assert.EqualValues(t, rootcommon.RelayResponseHeaderTimeout,
		snapshotInt64(t, snapshot, TimingKeyUpstreamHeaderTimeoutS))
	assert.EqualValues(t, constant.StreamingTimeout,
		snapshotInt64(t, snapshot, TimingKeyStreamingTimeoutS))
	assert.EqualValues(t, rootcommon.RelayTimeout,
		snapshotInt64(t, snapshot, TimingKeyRelayTimeoutS))
}

// 场景 1/2:排队等待时长 —— 直放保持 0,排队命中写入实际毫秒数。
func TestRequestTimingQueueWait(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	timing.SetQueueWait(0)
	assert.EqualValues(t, 0, snapshotInt64(t, timing.Snapshot(), TimingKeyQueueWaitMs))

	timing.SetQueueWait(1500 * time.Millisecond)
	assert.EqualValues(t, 1500, snapshotInt64(t, timing.Snapshot(), TimingKeyQueueWaitMs))
}

// 场景 3:一次请求发生两次渠道重选 → 选渠道耗时为两次之和,尝试次数为 2。
func TestRequestTimingChannelSelectAccumulatesAcrossAttempts(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	timing.AddChannelSelect(12 * time.Millisecond)
	timing.AddAttempt()
	timing.AddChannelSelect(8 * time.Millisecond)
	timing.AddAttempt()

	snapshot := timing.Snapshot()
	assert.EqualValues(t, 20, snapshotInt64(t, snapshot, TimingKeyChannelSelectMs))
	assert.EqualValues(t, 2, snapshotInt64(t, snapshot, TimingKeyAttempts))
}

// 场景 4:上游请求返回响应头耗时 X 毫秒 → 上游响应头等待记录为 X(覆盖写,
// 最后一次 attempt 为准)。
func TestRequestTimingUpstreamHeaderWait(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	timing.SetUpstreamHeaderWait(1020 * time.Millisecond)
	assert.EqualValues(t, 1020, snapshotInt64(t, timing.Snapshot(), TimingKeyUpstreamHeaderMs))

	timing.SetUpstreamHeaderWait(30 * time.Millisecond)
	assert.EqualValues(t, 30, snapshotInt64(t, timing.Snapshot(), TimingKeyUpstreamHeaderMs),
		"the last attempt overwrites the header wait")
}

// 场景 5/6:流式请求的首包 = 响应头到达 → 首个 data 行;流持续 = 首个 data 行
// → 流结束。首包段从响应头起算,因此与"上游响应头等待"两段互不重叠。
func TestRequestTimingFirstByteAndStream(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	headerAt := time.Now()
	timing.MarkUpstreamHeaderReceived(headerAt)
	timing.MarkFirstByte(headerAt.Add(250 * time.Millisecond))
	timing.MarkFirstByte(headerAt.Add(900 * time.Millisecond))
	timing.SetUpstreamStreamDuration(2 * time.Second)

	snapshot := timing.Snapshot()
	assert.EqualValues(t, 250, snapshotInt64(t, snapshot, TimingKeyUpstreamFirstByteMs),
		"only the first upstream byte counts")
	assert.EqualValues(t, 2000, snapshotInt64(t, snapshot, TimingKeyUpstreamStreamMs))
}

// 首包段不得重复计入响应头等待：响应头等了 2 秒后才吐第一个字节,首包段只能是
// 响应头之后的 200 毫秒,两段相加才等于上游耗时。
func TestRequestTimingFirstByteStartsAtUpstreamHeader(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	headerAt := time.Now().Add(-2 * time.Second)
	timing.MarkUpstreamHeaderReceived(headerAt)
	timing.MarkFirstByte(headerAt.Add(200 * time.Millisecond))

	snapshot := timing.Snapshot()
	assert.EqualValues(t, 200, snapshotInt64(t, snapshot, TimingKeyUpstreamFirstByteMs),
		"the first byte segment must not include the upstream header wait")
}

// 响应头时刻不可得时(不走 doRequest 的上游通道),首包段保持 0,绝不编造。
func TestRequestTimingFirstByteStaysZeroWithoutUpstreamHeader(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	timing.MarkFirstByte(time.Now())

	assert.EqualValues(t, 0, snapshotInt64(t, timing.Snapshot(), TimingKeyUpstreamFirstByteMs),
		"without an upstream header moment there is no first byte segment")
}

func TestRequestTimingNonStreamKeepsFirstByteAndStreamAtZero(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	timing.MarkUpstreamHeaderReceived(time.Now())
	timing.SetUpstreamHeaderWait(500 * time.Millisecond)

	snapshot := timing.Snapshot()
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyUpstreamFirstByteMs))
	assert.EqualValues(t, 0, snapshotInt64(t, snapshot, TimingKeyUpstreamStreamMs))
	assert.EqualValues(t, 500, snapshotInt64(t, snapshot, TimingKeyUpstreamHeaderMs))
}

// 非流式路径的响应体读取耗时单独成段:上游提前发出响应头后,整段生成时间都
// 落在这里,没有这一段就无法定位"响应头已到但内容还没来"的等待。
func TestRequestTimingUpstreamBodyDuration(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	timing.SetUpstreamBodyDuration(1020 * time.Millisecond)
	assert.EqualValues(t, 1020, snapshotInt64(t, timing.Snapshot(), TimingKeyUpstreamBodyMs))

	timing.SetUpstreamBodyDuration(30 * time.Millisecond)
	assert.EqualValues(t, 30, snapshotInt64(t, timing.Snapshot(), TimingKeyUpstreamBodyMs),
		"the last body read overwrites the previous one")
}

// slowReader 在第一次读取前睡一段时间,用来制造"响应体迟迟不来"的形态。
type slowReader struct {
	delay time.Duration
	data  []byte
	read  bool
}

func (r *slowReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		time.Sleep(r.delay)
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// failingReader 立刻返回错误,用来验证读失败同样留下响应体耗时。
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("upstream body broke")
}

// ReadUpstreamBody 把响应体读取耗时写进采集载体;读失败同样写入,因为读失败
// 本身就说明卡在响应体阶段。
func TestReadUpstreamBodyRecordsDuration(t *testing.T) {
	t.Parallel()

	c := newTimingTestContext()
	payload := []byte(`{"id":"chatcmpl-1"}`)
	body, err := ReadUpstreamBody(c, &slowReader{delay: 30 * time.Millisecond, data: payload})
	require.NoError(t, err)
	assert.Equal(t, payload, body)

	timing := RequestTimingFrom(c)
	require.NotNil(t, timing)
	assert.GreaterOrEqual(t, timing.UpstreamBodyMs.Load(), int64(30),
		"the response body read must be measured separately from the header wait")
}

func TestReadUpstreamBodyRecordsDurationOnReadFailure(t *testing.T) {
	t.Parallel()

	c := newTimingTestContext()
	body, err := ReadUpstreamBody(c, failingReader{})
	require.Error(t, err)
	assert.Empty(t, body)

	timing := RequestTimingFrom(c)
	require.NotNil(t, timing)
	assert.GreaterOrEqual(t, timing.UpstreamBodyMs.Load(), int64(0),
		"a failed body read still leaves a response body duration")
}

// 采集载体通过 gin 上下文共享:取不到时创建并回写,之后各处拿到的是同一个实例;
// 只读查找在载体不存在时返回 nil(非 relay 路径不写 timing 键)。
func TestRequestTimingCarrierLivesInGinContext(t *testing.T) {
	t.Parallel()

	c := newTimingTestContext()
	assert.Nil(t, RequestTimingFrom(c), "a context without a carrier reports none")

	timing := RequestTimingOf(c)
	require.NotNil(t, timing)
	assert.Same(t, timing, RequestTimingOf(c))
	assert.Same(t, timing, RequestTimingFrom(c))
}

// 场景 8:扫描 goroutine 写首包时刻、主 goroutine 读该时刻 —— 并发安全,
// `go test -race` 无 data race 报告。
func TestRequestTimingConcurrentFirstByteAndSnapshot(t *testing.T) {
	t.Parallel()

	timing := &RequestTiming{}
	headerAt := time.Now()
	timing.MarkUpstreamHeaderReceived(headerAt)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		timing.MarkFirstByte(headerAt.Add(120 * time.Millisecond))
		timing.SetUpstreamStreamDuration(3 * time.Second)
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			_ = timing.Snapshot()
		}
	}()
	wg.Wait()

	snapshot := timing.Snapshot()
	assert.EqualValues(t, 120, snapshotInt64(t, snapshot, TimingKeyUpstreamFirstByteMs))
	assert.EqualValues(t, 3000, snapshotInt64(t, snapshot, TimingKeyUpstreamStreamMs))
}
