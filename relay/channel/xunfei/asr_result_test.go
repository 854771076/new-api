package xunfei

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 讯飞 IFASR 轮询接口（/v2/getResult）响应样例。
//
// 上游把转写结果放在 content.orderResult 里，而该字段本身是一个 JSON 字符串；
// 字符串内部每个分段单元的 json_1best 又是一个 JSON 字符串。
// 样例之间的差异只在识别结果引号前的反斜杠层数：
//   - asrResponseOverEscaped：两层反斜杠（上游过度转义），需要一次还原才能解码；
//   - asrResponseSingleEscaped：一层反斜杠（官方文档示例形态），还原步骤对其为空操作；
//   - asrResponseStillMalformed：三层反斜杠，只还原一次仍然畸形。
const (
	asrResponseOverEscaped = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028,"language":"cn"},"orderResult":"{\"lattice\":[{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"为\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":\\\\\"880\\\\\",\\\\\"ed\\\\\":\\\\\"1680\\\\\",\\\\\"rl\\\\\":\\\\\"1\\\\\"}}\"},{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"喂\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10},{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"你好\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"},{\\\\\"w\\\\\":\\\\\"您好\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10},{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"。\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":\\\\\"2390\\\\\",\\\\\"ed\\\\\":\\\\\"3640\\\\\",\\\\\"rl\\\\\":\\\\\"1\\\\\"}}\"},{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"你\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":\\\\\"5000\\\\\",\\\\\"ed\\\\\":\\\\\"6000\\\\\",\\\\\"rl\\\\\":\\\\\"2\\\\\"}}\"},{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"好\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":\\\\\"7000\\\\\",\\\\\"ed\\\\\":\\\\\"8000\\\\\",\\\\\"rl\\\\\":\\\\\"2\\\\\"}}\"}]}"}}`

	asrResponseSingleEscaped = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028,"language":"cn"},"orderResult":"{\"lattice\":[{\"json_1best\":\"{\\\"st\\\":{\\\"pa\\\":\\\"0\\\",\\\"rt\\\":[{\\\"ws\\\":[{\\\"cw\\\":[{\\\"w\\\":\\\"为\\\",\\\"wp\\\":\\\"n\\\",\\\"wc\\\":\\\"1.0000\\\"}],\\\"wb\\\":0,\\\"we\\\":10}]}],\\\"bg\\\":\\\"880\\\",\\\"ed\\\":\\\"1680\\\",\\\"rl\\\":\\\"1\\\"}}\"},{\"json_1best\":\"{\\\"st\\\":{\\\"pa\\\":\\\"0\\\",\\\"rt\\\":[{\\\"ws\\\":[{\\\"cw\\\":[{\\\"w\\\":\\\"喂\\\",\\\"wp\\\":\\\"n\\\",\\\"wc\\\":\\\"1.0000\\\"}],\\\"wb\\\":0,\\\"we\\\":10},{\\\"cw\\\":[{\\\"w\\\":\\\"你好\\\",\\\"wp\\\":\\\"n\\\",\\\"wc\\\":\\\"1.0000\\\"},{\\\"w\\\":\\\"您好\\\",\\\"wp\\\":\\\"n\\\",\\\"wc\\\":\\\"1.0000\\\"}],\\\"wb\\\":0,\\\"we\\\":10},{\\\"cw\\\":[{\\\"w\\\":\\\"。\\\",\\\"wp\\\":\\\"n\\\",\\\"wc\\\":\\\"1.0000\\\"}],\\\"wb\\\":0,\\\"we\\\":10}]}],\\\"bg\\\":\\\"2390\\\",\\\"ed\\\":\\\"3640\\\",\\\"rl\\\":\\\"1\\\"}}\"},{\"json_1best\":\"{\\\"st\\\":{\\\"pa\\\":\\\"0\\\",\\\"rt\\\":[{\\\"ws\\\":[{\\\"cw\\\":[{\\\"w\\\":\\\"你\\\",\\\"wp\\\":\\\"n\\\",\\\"wc\\\":\\\"1.0000\\\"}],\\\"wb\\\":0,\\\"we\\\":10}]}],\\\"bg\\\":\\\"5000\\\",\\\"ed\\\":\\\"6000\\\",\\\"rl\\\":\\\"2\\\"}}\"},{\"json_1best\":\"{\\\"st\\\":{\\\"pa\\\":\\\"0\\\",\\\"rt\\\":[{\\\"ws\\\":[{\\\"cw\\\":[{\\\"w\\\":\\\"好\\\",\\\"wp\\\":\\\"n\\\",\\\"wc\\\":\\\"1.0000\\\"}],\\\"wb\\\":0,\\\"we\\\":10}]}],\\\"bg\\\":\\\"7000\\\",\\\"ed\\\":\\\"8000\\\",\\\"rl\\\":\\\"2\\\"}}\"}]}"}}`

	asrResponseStillMalformed = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028,"language":"cn"},"orderResult":"{\"lattice\":[{\"json_1best\":\"{\\\\\\\"st\\\\\\\":{\\\\\\\"pa\\\\\\\":\\\\\\\"0\\\\\\\",\\\\\\\"rt\\\\\\\":[{\\\\\\\"ws\\\\\\\":[{\\\\\\\"cw\\\\\\\":[{\\\\\\\"w\\\\\\\":\\\\\\\"为\\\\\\\",\\\\\\\"wp\\\\\\\":\\\\\\\"n\\\\\\\",\\\\\\\"wc\\\\\\\":\\\\\\\"1.0000\\\\\\\"}],\\\\\\\"wb\\\\\\\":0,\\\\\\\"we\\\\\\\":10}]}],\\\\\\\"bg\\\\\\\":\\\\\\\"880\\\\\\\",\\\\\\\"ed\\\\\\\":\\\\\\\"1680\\\\\\\",\\\\\\\"rl\\\\\\\":\\\\\\\"1\\\\\\\"}}\"}]}"}}`

	asrResponseLatticeEmptyFallback = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028,"language":"cn"},"orderResult":"{\"lattice\":[],\"lattice2\":[{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"你\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":\\\\\"5000\\\\\",\\\\\"ed\\\\\":\\\\\"6000\\\\\",\\\\\"rl\\\\\":\\\\\"2\\\\\"}}\"}]}"}}`

	asrResponseBothLatticesEmpty = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028,"language":"cn"},"orderResult":"{\"lattice\":[],\"lattice2\":[]}"}}`

	asrResponseNoLanguage = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028},"orderResult":"{\"lattice\":[{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"为\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":\\\\\"880\\\\\",\\\\\"ed\\\\\":\\\\\"1680\\\\\",\\\\\"rl\\\\\":\\\\\"1\\\\\"}}\"}]}"}}`

	asrResponseNumericTimes = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028,"language":"cn"},"orderResult":"{\"lattice\":[{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"数\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":100,\\\\\"ed\\\\\":200,\\\\\"rl\\\\\":\\\\\"1\\\\\"}}\"}]}"}}`

	asrResponseNoSpeaker = `{"code":"000000","descInfo":"success","content":{"orderInfo":{"failType":0,"status":4,"orderId":"DKHJQ20250909161126126","originalDuration":98028,"language":"cn"},"orderResult":"{\"lattice\":[{\"json_1best\":\"{\\\\\"st\\\\\":{\\\\\"pa\\\\\":\\\\\"0\\\\\",\\\\\"rt\\\\\":[{\\\\\"ws\\\\\":[{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"无\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10},{\\\\\"cw\\\\\":[{\\\\\"w\\\\\":\\\\\"人\\\\\",\\\\\"wp\\\\\":\\\\\"n\\\\\",\\\\\"wc\\\\\":\\\\\"1.0000\\\\\"}],\\\\\"wb\\\\\":0,\\\\\"we\\\\\":10}]}],\\\\\"bg\\\\\":\\\\\"10\\\\\",\\\\\"ed\\\\\":\\\\\"20\\\\\"}}\"}]}"}}`
)

func TestParseASRResultParsesNestedOverEscapedPayload(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseOverEscaped), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	// 四个分段单元，两个说话人；同一说话人的单元合并为一段并取时间并集。
	assert.Equal(t, []ASRSegment{
		{Index: 0, Speaker: "1", StartMs: 880, EndMs: 3640, Text: "为喂你好。"},
		{Index: 1, Speaker: "2", StartMs: 5000, EndMs: 8000, Text: "你好"},
	}, result.Segments)
	assert.Equal(t, "为喂你好。你好", result.Text)
}

func TestParseASRResultParsesNestedSingleEscapedPayload(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseSingleEscaped), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []ASRSegment{
		{Index: 0, Speaker: "1", StartMs: 880, EndMs: 3640, Text: "为喂你好。"},
		{Index: 1, Speaker: "2", StartMs: 5000, EndMs: 8000, Text: "你好"},
	}, result.Segments)
	assert.Equal(t, "为喂你好。你好", result.Text)
}

// 还原只做一次：引号前有三层反斜杠时，单次还原后仍无法解码，按畸形输入处理。
func TestParseASRResultRestoresOverEscapingOnlyOnce(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseStillMalformed), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.Segments)
	assert.Empty(t, result.Text)
	// 订单信息仍然可用。
	assert.Equal(t, int64(98028), result.OriginalDurationMs)
}

func TestParseASRResultFallsBackToLattice2(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseLatticeEmptyFallback), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []ASRSegment{
		{Index: 0, Speaker: "2", StartMs: 5000, EndMs: 6000, Text: "你"},
	}, result.Segments)
	assert.Equal(t, "你", result.Text)
}

func TestParseASRResultReturnsEmptyResultWhenBothLatticesEmpty(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseBothLatticesEmpty), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.Segments)
	assert.Empty(t, result.Text)
}

func TestParseASRResultExtractsDurationAndLanguage(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseOverEscaped), ASRResultTypeAnalysis)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int64(98028), result.OriginalDurationMs)
	assert.Equal(t, "cn", result.Language)
	assert.Equal(t, ASRResultTypeAnalysis, result.ResultType)
}

func TestParseASRResultLeavesLanguageEmptyWhenUpstreamOmitsIt(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseNoLanguage), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int64(98028), result.OriginalDurationMs)
	assert.Empty(t, result.Language)
}

func TestParseASRResultCarriesOrderStatus(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseOverEscaped), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "000000", result.Code)
	assert.Equal(t, "DKHJQ20250909161126126", result.Order.OrderId)
	assert.Equal(t, 4, result.Order.Status)
	assert.Equal(t, 0, result.Order.FailType)
}

func TestParseASRResultAcceptsNumericTimestamps(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseNumericTimes), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []ASRSegment{
		{Index: 0, Speaker: "1", StartMs: 100, EndMs: 200, Text: "数"},
	}, result.Segments)
}

func TestParseASRResultKeepsEmptySpeakerWhenUpstreamOmitsIt(t *testing.T) {
	result, err := ParseASRResult([]byte(asrResponseNoSpeaker), ASRResultTypeTransfer)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []ASRSegment{
		{Index: 0, Speaker: "", StartMs: 10, EndMs: 20, Text: "无人"},
	}, result.Segments)
}

func TestParseASRResultRejectsResponseThatIsNotExpectedJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "空响应体", body: ``},
		{name: "不是 JSON", body: `not a json body`},
		{name: "JSON 但不是对象", body: `[]`},
		{name: "JSON 数组元素", body: `[{"code":"000000"}]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseASRResult([]byte(tc.body), ASRResultTypeTransfer)
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}

func TestParseASRResultReturnsEmptyResultForUnusablePayload(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "没有 content 字段",
			body: `{"code":"000000","descInfo":"success"}`,
		},
		{
			name: "转写载荷为空字符串",
			body: `{"code":"000000","content":{"orderResult":""}}`,
		},
		{
			name: "转写载荷不是 JSON",
			body: `{"code":"000000","content":{"orderResult":"not a json payload"}}`,
		},
		{
			name: "分段单元的识别结果不是 JSON",
			body: `{"code":"000000","content":{"orderResult":"{\"lattice\":[{\"json_1best\":\"broken\"}]}"}}`,
		},
		{
			name: "分段单元缺少识别结果",
			body: `{"code":"000000","content":{"orderResult":"{\"lattice\":[{}]}"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseASRResult([]byte(tc.body), ASRResultTypeTransfer)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Empty(t, result.Segments)
			assert.Empty(t, result.Text)
		})
	}
}
