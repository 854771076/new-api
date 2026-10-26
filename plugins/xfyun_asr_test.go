package plugins_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	taskjsplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel/xunfei"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	xfyunTestModel      = "ifasr-llm-v1"
	xfyunTestAppID      = "app-id"
	xfyunTestAPISecret  = "api-secret"
	xfyunTestAccessKey  = "access-key-id"
	xfyunTestChannelKey = xfyunTestAppID + "|" + xfyunTestAPISecret + "|" + xfyunTestAccessKey
	xfyunTestOrderID    = "order-0001"
	xfyunTestPublicTask = "task_public"
	xfyunTestUploadPath = "/v2/upload"
	xfyunTestResultPath = "/v2/getResult"
	xfyunTestFileName   = "录音 sample.wav"
)

type xfyunTestUnit struct {
	speaker string
	start   string
	end     string
	text    string
}

var xfyunTestUnits = []xfyunTestUnit{
	{speaker: "1", start: "0", end: "1200", text: "你好"},
	{speaker: "1", start: "1200", end: "3000", text: "世界"},
	{speaker: "2", start: "3000", end: "5000", text: "第二段"},
}

type xfyunUpstreamCall struct {
	rawQuery string
	header   http.Header
	body     []byte
}

// xfyunFakeUpstream serves the two IFASR endpoints and records exactly what the
// host sent, so request-shape assertions do not depend on the plugin's own view.
type xfyunFakeUpstream struct {
	server *httptest.Server

	mu         sync.Mutex
	resultBody string
	uploads    []xfyunUpstreamCall
	queries    []xfyunUpstreamCall
}

func newXfyunFakeUpstream(t *testing.T) *xfyunFakeUpstream {
	t.Helper()
	upstream := &xfyunFakeUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		call := xfyunUpstreamCall{rawQuery: r.URL.RawQuery, header: r.Header.Clone(), body: body}
		switch r.URL.Path {
		case xfyunTestUploadPath:
			upstream.mu.Lock()
			upstream.uploads = append(upstream.uploads, call)
			upstream.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"000000","descInfo":"success","content":{"orderId":"` + xfyunTestOrderID + `","taskEstimateTime":1000}}`))
		case xfyunTestResultPath:
			upstream.mu.Lock()
			upstream.queries = append(upstream.queries, call)
			response := upstream.resultBody
			upstream.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(response))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *xfyunFakeUpstream) setResult(t *testing.T, body string) {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.resultBody = body
}

func (u *xfyunFakeUpstream) upload(t *testing.T) xfyunUpstreamCall {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	require.Len(t, u.uploads, 1, "exactly one upload must be sent")
	return u.uploads[0]
}

func (u *xfyunFakeUpstream) lastQuery(t *testing.T) xfyunUpstreamCall {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	require.NotEmpty(t, u.queries, "a result query must be sent")
	return u.queries[len(u.queries)-1]
}

func xfyunJSONString(t *testing.T, value any) string {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func xfyunJSONMap(t *testing.T, value any) map[string]any {
	t.Helper()
	var decoded map[string]any
	require.NoError(t, common.Unmarshal([]byte(xfyunJSONString(t, value)), &decoded))
	return decoded
}

// xfyunOverEscaped reproduces the upstream's double backslash escaping: after
// the outer JSON decode the orderResult text still carries two backslashes where
// one belongs.
func xfyunOverEscaped(t *testing.T, orderResult map[string]any) string {
	t.Helper()
	return strings.ReplaceAll(xfyunJSONString(t, orderResult), `\`, `\\`)
}

func xfyunSentence(t *testing.T, unit xfyunTestUnit, inlineTiming bool) map[string]any {
	t.Helper()
	st := map[string]any{
		"rt": []any{map[string]any{"ws": []any{map[string]any{"cw": []any{map[string]any{"w": unit.text}}}}}},
	}
	if inlineTiming {
		st["bg"] = unit.start
		st["ed"] = unit.end
		st["rl"] = unit.speaker
	}
	return map[string]any{"st": st}
}

// xfyunLatticeSample is the smoothed shape: json_1best is a JSON string and the
// timing/speaker live inside its st object.
func xfyunLatticeSample(t *testing.T) map[string]any {
	t.Helper()
	units := make([]any, 0, len(xfyunTestUnits))
	for _, unit := range xfyunTestUnits {
		units = append(units, map[string]any{"json_1best": xfyunJSONString(t, xfyunSentence(t, unit, true))})
	}
	return map[string]any{"lattice": units}
}

// xfyunLattice2Sample is the unsmoothed shape written the way the Go parser
// reads it: json_1best is an object and timing/speaker sit on the unit.
func xfyunLattice2Sample(t *testing.T) map[string]any {
	t.Helper()
	units := make([]any, 0, len(xfyunTestUnits))
	for _, unit := range xfyunTestUnits {
		units = append(units, map[string]any{
			"bg":         unit.start,
			"ed":         unit.end,
			"rl":         unit.speaker,
			"json_1best": xfyunSentence(t, unit, false),
		})
	}
	return map[string]any{"lattice2": units}
}

// xfyunLattice2BeginSample uses the spelling recorded in the protocol facts:
// begin/end/spk on the unit and no timing inside json_1best.
func xfyunLattice2BeginSample(t *testing.T) map[string]any {
	t.Helper()
	units := make([]any, 0, len(xfyunTestUnits))
	for index, unit := range xfyunTestUnits {
		units = append(units, map[string]any{
			"lid":        strconv.Itoa(index),
			"begin":      unit.start,
			"end":        unit.end,
			"spk":        "段落-" + unit.speaker,
			"json_1best": xfyunSentence(t, unit, false),
		})
	}
	return map[string]any{"lattice2": units}
}

func xfyunResultResponse(t *testing.T, orderStatus int, orderResult string) string {
	t.Helper()
	return xfyunResultResponseWithDurations(t, orderStatus, orderResult, 8000, 8000)
}

// xfyunResultResponseWithDurations 允许分别指定 originalDuration 与 realDuration。
// 关闭时长校验时上游会把 originalDuration 变成一个默认值，真实长度只在 realDuration 里，
// 两者不等是真实上游的常见形态。
func xfyunResultResponseWithDurations(t *testing.T, orderStatus int, orderResult string, originalMs, realMs int) string {
	t.Helper()
	content := map[string]any{
		"orderInfo": map[string]any{
			"orderId":          xfyunTestOrderID,
			"failType":         0,
			"status":           orderStatus,
			"originalDuration": originalMs,
			"realDuration":     realMs,
		},
	}
	if orderResult != "" {
		content["orderResult"] = orderResult
	}
	return xfyunJSONString(t, map[string]any{"code": "000000", "descInfo": "success", "content": content})
}

func xfyunMultipartRequest(t *testing.T, content []byte) *gin.Context {
	t.Helper()
	var input bytes.Buffer
	writer := multipart.NewWriter(&input)
	require.NoError(t, writer.WriteField("model", xfyunTestModel))
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + xfyunTestFileName + `"`},
		"Content-Type":        {"audio/wav"},
	})
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions/tasks", bytes.NewReader(input.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return c
}

// xfyunTestClock is a deterministic clock for the plugin runtime: every read
// advances one second, so two requests can never share a timestamp and the
// test does not have to wait for the wall clock.
type xfyunTestClock struct {
	ticks int
}

func (c *xfyunTestClock) Now() time.Time {
	c.ticks++
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Duration(c.ticks) * time.Second)
}

func newXfyunTestPlugin(t *testing.T) (*pluginruntime.LoadedPlugin, *pluginruntime.Registry) {
	t.Helper()
	source, err := builtinplugins.Source("xfyun-asr")
	require.NoError(t, err)
	registry := pluginruntime.NewRegistry()
	plugin, err := registry.RegisterFactory(source, pluginruntime.Options{Key: "xfyun-asr", Now: (&xfyunTestClock{}).Now})
	require.NoError(t, err)
	return plugin, registry
}

func xfyunDecodeRequest(t *testing.T, plugin *pluginruntime.LoadedPlugin, body map[string]any) (map[string]any, error) {
	t.Helper()
	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{pluginruntime.ProtocolOpenAIAudio, "decodeRequest"}, map[string]any{
		"protocol":  pluginruntime.ProtocolOpenAIAudio,
		"operation": "create",
		"model":     xfyunTestModel,
		"stream":    false,
		"body":      body,
	})
	if err != nil {
		return nil, err
	}
	return xfyunJSONMap(t, value), nil
}

// xfyunMultipartBody builds the multipart body a capability-declaring host
// hands to the decoder: the file carries the billable duration the host
// measured.
func xfyunMultipartBody(content []byte) map[string]any {
	return map[string]any{
		"kind":   "multipart",
		"fields": map[string][]string{"model": {xfyunTestModel}},
		"files": []map[string]any{{
			"ref":             "request_file:file",
			"field":           "file",
			"filename":        xfyunTestFileName,
			"mimeType":        "audio/wav",
			"size":            len(content),
			"durationSeconds": 8,
		}},
	}
}

func xfyunParseTaskResultHook(t *testing.T, plugin *pluginruntime.LoadedPlugin, response string) map[string]any {
	t.Helper()
	var body any
	require.NoError(t, common.Unmarshal([]byte(response), &body))
	value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{
		"taskId":        xfyunTestOrderID,
		"publicTaskId":  xfyunTestPublicTask,
		"action":        "transcription",
		"model":         xfyunTestModel,
		"upstreamModel": xfyunTestModel,
		"baseUrl":       "https://office-api-ist-dx.iflyaisol.com",
		"apiKey":        xfyunTestChannelKey,
		"authHeader":    xfyunTestChannelKey,
	}, body, map[string]any{"status": http.StatusOK, "headers": map[string]any{}})
	require.NoError(t, err)
	return xfyunJSONMap(t, value)
}

func xfyunRenderHook(t *testing.T, plugin *pluginruntime.LoadedPlugin, task map[string]any) map[string]any {
	t.Helper()
	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{pluginruntime.ProtocolOpenAIAudio, "render"},
		map[string]any{"protocol": pluginruntime.ProtocolOpenAIAudio, "operation": "retrieve"}, task)
	require.NoError(t, err)
	return xfyunJSONMap(t, value)
}

func xfyunQueryParams(t *testing.T, call xfyunUpstreamCall) url.Values {
	t.Helper()
	params, err := url.ParseQuery(call.rawQuery)
	require.NoError(t, err)
	require.NotContains(t, params, "signature", "the signature must not appear in the query string")
	require.NotEmpty(t, call.header.Get("signature"), "the signature must travel in the request header")
	// Recompute with the production Go implementation: this proves the plugin
	// signed exactly the query string it sent, using the same IFASR encoding.
	assert.Equal(t, xunfei.XfyunASRSignature(params, xfyunTestAPISecret), call.header.Get("signature"))
	return params
}

func TestXfyunASRPluginManifest(t *testing.T) {
	plugin, registry := newXfyunTestPlugin(t)

	assert.Equal(t, "xfyun-asr", plugin.Meta.Key)
	assert.Equal(t, []string{xfyunTestModel}, plugin.Meta.Models)
	assert.Equal(t, "per_task", plugin.Meta.FetchMode)
	assert.Equal(t, "https://office-api-ist-dx.iflyaisol.com", plugin.Meta.BaseURL)
	assert.Equal(t, "api_key", plugin.Meta.Auth.Type)
	require.Len(t, plugin.Meta.Protocols, 1)
	assert.Equal(t, pluginruntime.ProtocolOpenAIAudio, plugin.Meta.Protocols[0].Name)
	assert.Contains(t, plugin.Meta.RequiredCapabilities, "media-duration@1",
		"billing by duration needs the host to measure the uploaded media")

	binding, found := registry.Generation().LookupEndpoint(http.MethodPost, "/v1/audio/transcriptions/tasks", xfyunTestModel)
	require.True(t, found, "the plugin must claim the audio create endpoint for its model")
	assert.Same(t, plugin, binding.Plugin)
	assert.Equal(t, pluginruntime.ProtocolOpenAIAudio, binding.Protocol)

	schema, found := plugin.Meta.UsageSchema["duration"]
	require.True(t, found)
	assert.Equal(t, "number", schema.Type)
	assert.Equal(t, "second", schema.Unit)
	assert.Equal(t, "Audio transcription unit price", schema.Description["en"])
	assert.Equal(t, "音频转写单价", schema.Description["zh"])

	require.NotEmpty(t, plugin.Meta.UsageExamples)
	assert.Equal(t, map[string]any{"duration": int64(60)}, plugin.Meta.UsageExamples[0].Facts)

	for _, hook := range []string{"buildSubmitRequest", "parseSubmitResponse", "buildQueryRequest", "parseTaskResult", "extractUsage", "extractUsageOnComplete"} {
		callable, err := plugin.Engine.HasExport(t.Context(), hook)
		require.NoError(t, err)
		assert.True(t, callable, hook)
	}
	for _, hook := range []string{"decodeRequest", "render"} {
		callable, err := plugin.Engine.HasCallablePath(t.Context(), "protocols", pluginruntime.ProtocolOpenAIAudio, hook)
		require.NoError(t, err)
		assert.True(t, callable, hook)
	}

	// Submission reserves the duration the host billed the uploaded file at.
	value, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{
		"model": xfyunTestModel, "upstreamModel": xfyunTestModel,
		"requestBody": map[string]any{"fileSize": 9, "durationSeconds": 8},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"duration": float64(8)}, xfyunJSONMap(t, value))

	_, err = plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{
		"model": xfyunTestModel, "upstreamModel": xfyunTestModel,
		"requestBody": map[string]any{"fileSize": 9},
	})
	require.ErrorContains(t, err, "billable duration",
		"a request without a host duration must not be priced as a free request")

	value, err = plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{
		"model": xfyunTestModel, "upstreamModel": xfyunTestModel, "usagePurpose": "billing_ratios",
		"requestBody": map[string]any{"fileSize": 9, "durationSeconds": 8},
	})
	require.NoError(t, err)
	assert.Nil(t, value, "the legacy per-call pricing path must not apply duration multipliers")

	value, err = plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{"taskId": xfyunTestOrderID}, map[string]any{"status": "SUCCESS"},
		map[string]any{"content": map[string]any{"orderInfo": map[string]any{"originalDuration": 8000}}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"duration": float64(8)}, xfyunJSONMap(t, value))

	value, err = plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{"taskId": xfyunTestOrderID}, map[string]any{"status": "SUCCESS"},
		map[string]any{"content": map[string]any{"orderInfo": map[string]any{"realDuration": 8000, "originalDuration": 60000}}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"duration": float64(8)}, xfyunJSONMap(t, value),
		"the measured duration replaces the degraded original duration")

	value, err = plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{"taskId": xfyunTestOrderID}, map[string]any{"status": "SUCCESS"},
		map[string]any{"content": map[string]any{"orderInfo": map[string]any{"originalDuration": 7200000}}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"duration": float64(7200)}, xfyunJSONMap(t, value),
		"the plugin reports the upstream duration as is; the host enforces its own ceiling")

	value, err = plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{"taskId": xfyunTestOrderID}, map[string]any{"status": "SUCCESS"},
		map[string]any{"content": map[string]any{"orderInfo": map[string]any{}}})
	require.NoError(t, err)
	assert.Nil(t, value, "no duration fact is produced when the upstream omits originalDuration")
}

func TestXfyunASRPluginRejectsInvalidRequests(t *testing.T) {
	plugin, _ := newXfyunTestPlugin(t)

	t.Run("json body", func(t *testing.T) {
		_, err := xfyunDecodeRequest(t, plugin, map[string]any{"kind": "json", "value": map[string]any{"model": xfyunTestModel}})
		require.ErrorContains(t, err, "multipart")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := xfyunDecodeRequest(t, plugin, map[string]any{
			"kind":   "multipart",
			"fields": map[string][]string{"model": {xfyunTestModel}},
			"files":  []map[string]any{},
		})
		require.ErrorContains(t, err, `"file"`)
	})

	t.Run("unexpected file field", func(t *testing.T) {
		body := xfyunMultipartBody([]byte("audio"))
		body["files"] = []map[string]any{{"ref": "request_file:audio", "field": "audio", "filename": "a.wav", "mimeType": "audio/wav", "size": 5}}
		_, err := xfyunDecodeRequest(t, plugin, body)
		require.ErrorContains(t, err, "unexpected file field")
	})

	t.Run("malformed channel key", func(t *testing.T) {
		decoded, err := xfyunDecodeRequest(t, plugin, xfyunMultipartBody([]byte("audio")))
		require.NoError(t, err)
		_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
			"baseUrl":     "https://office-api-ist-dx.iflyaisol.com",
			"apiKey":      "not-three-parts",
			"authHeader":  "not-three-parts",
			"requestBody": decoded["requestBody"],
		})
		require.ErrorContains(t, err, "appId|apiSecret|accessKeyId")
	})
}

// The host prices the uploaded audio while it parses the request; the decoder
// carries that duration into the request body so the billing hook can reserve
// it. A request the host did not price is rejected instead of being billed as
// a zero-second transcription.
func TestXfyunASRPluginCarriesMeasuredDuration(t *testing.T) {
	plugin, _ := newXfyunTestPlugin(t)

	body := xfyunMultipartBody([]byte("audio"))
	body["files"].([]map[string]any)[0]["durationSeconds"] = 7.5
	decoded, err := xfyunDecodeRequest(t, plugin, body)
	require.NoError(t, err)
	requestBody, ok := decoded["requestBody"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 7.5, requestBody["durationSeconds"])

	unpriced := xfyunMultipartBody([]byte("audio"))
	delete(unpriced["files"].([]map[string]any)[0], "durationSeconds")
	_, err = xfyunDecodeRequest(t, plugin, unpriced)
	require.ErrorContains(t, err, "billable duration")
}

func xfyunAssertSegmentsMatchGo(t *testing.T, goResult *xunfei.ASRResult, data map[string]any) {
	t.Helper()
	assert.Equal(t, goResult.Text, data["text"])
	assert.Equal(t, goResult.Language, data["language"])
	assert.Equal(t, float64(goResult.OriginalDurationMs), data["billedDuration"])
	assert.Equal(t, float64(goResult.OriginalDurationMs)/1000, data["duration_seconds"])
	segments := data["segments"].([]any)
	require.Len(t, segments, len(goResult.Segments))
	for index, expected := range goResult.Segments {
		actual := segments[index].(map[string]any)
		assert.Equal(t, float64(expected.Index), actual["id"], "segment %d id", index)
		assert.Equal(t, float64(expected.StartMs), actual["start"], "segment %d start", index)
		assert.Equal(t, float64(expected.EndMs), actual["end"], "segment %d end", index)
		assert.Equal(t, expected.Text, actual["text"], "segment %d text", index)
		assert.Equal(t, expected.Speaker, actual["speaker"], "segment %d speaker", index)
	}
}

func TestXfyunASRPluginMatchesGoParser(t *testing.T) {
	plugin, _ := newXfyunTestPlugin(t)

	t.Run("lattice", func(t *testing.T) {
		response := xfyunResultResponse(t, 4, xfyunOverEscaped(t, xfyunLatticeSample(t)))
		goResult, err := xunfei.ParseASRResult([]byte(response), xunfei.ASRResultTypeTransfer)
		require.NoError(t, err)
		pluginResult := xfyunParseTaskResultHook(t, plugin, response)
		assert.Equal(t, "SUCCESS", pluginResult["status"])
		xfyunAssertSegmentsMatchGo(t, goResult, pluginResult["data"].(map[string]any))
	})

	t.Run("lattice2", func(t *testing.T) {
		response := xfyunResultResponse(t, 4, xfyunOverEscaped(t, xfyunLattice2Sample(t)))
		goResult, err := xunfei.ParseASRResult([]byte(response), xunfei.ASRResultTypeTransfer)
		require.NoError(t, err)
		pluginResult := xfyunParseTaskResultHook(t, plugin, response)
		xfyunAssertSegmentsMatchGo(t, goResult, pluginResult["data"].(map[string]any))
	})

	t.Run("lattice2 begin and spk", func(t *testing.T) {
		response := xfyunResultResponse(t, 4, xfyunOverEscaped(t, xfyunLattice2BeginSample(t)))
		goResult, err := xunfei.ParseASRResult([]byte(response), xunfei.ASRResultTypeTransfer)
		require.NoError(t, err)
		pluginResult := xfyunParseTaskResultHook(t, plugin, response)
		data := pluginResult["data"].(map[string]any)
		// 两侧读同一批字段：Go 与插件对 begin/end/spk 形态给出同样的结果。
		xfyunAssertSegmentsMatchGo(t, goResult, data)
		segments := data["segments"].([]any)
		require.Len(t, segments, 2, "adjacent units of the same speaker merge into one segment")
		first := segments[0].(map[string]any)
		assert.Equal(t, float64(0), first["start"])
		assert.Equal(t, float64(3000), first["end"])
		assert.Equal(t, "段落-1", first["speaker"])
		assert.Equal(t, "你好世界", first["text"])
	})

	t.Run("business error code", func(t *testing.T) {
		pluginResult := xfyunParseTaskResultHook(t, plugin, `{"code":100001,"descInfo":"order not found","content":{}}`)
		assert.Equal(t, "FAILURE", pluginResult["status"])
		reason := pluginResult["reason"].(string)
		assert.Contains(t, reason, "100001")
		assert.Contains(t, reason, "order not found")
	})

	t.Run("not finished business code", func(t *testing.T) {
		pluginResult := xfyunParseTaskResultHook(t, plugin, `{"code":"100013","descInfo":"order not finished","content":{"orderInfo":{"orderId":"order-0001","status":0}}}`)
		assert.Equal(t, "IN_PROGRESS", pluginResult["status"])
	})

	t.Run("originalDuration diverging from realDuration bills the real length", func(t *testing.T) {
		// 关闭时长校验时上游把 originalDuration 变成一个默认值（实测 8 秒音频返回 60000），
		// 真实长度只在 realDuration 里；按 originalDuration 计费会把 8 秒算成 60 秒。
		response := xfyunResultResponseWithDurations(t, 4, xfyunOverEscaped(t, xfyunLatticeSample(t)), 60000, 7998)
		pluginResult := xfyunParseTaskResultHook(t, plugin, response)
		assert.Equal(t, "SUCCESS", pluginResult["status"])
		data := pluginResult["data"].(map[string]any)
		assert.Equal(t, float64(7998), data["billedDuration"])
		assert.Equal(t, 7998.0/1000, data["duration_seconds"])

		rendered := xfyunRenderHook(t, plugin, map[string]any{
			"task_id": xfyunTestPublicTask,
			"status":  "SUCCESS",
			"data":    xfyunJSONMap(t, mustUnmarshalJSON(t, []byte(response))),
		})
		assert.Equal(t, 7998.0/1000, rendered["duration"])
	})

	t.Run("missing result payload is an empty transcription", func(t *testing.T) {
		for name, orderResult := range map[string]string{"absent": "", "malformed": "{not json"} {
			t.Run(name, func(t *testing.T) {
				pluginResult := xfyunParseTaskResultHook(t, plugin, xfyunResultResponse(t, 4, orderResult))
				assert.Equal(t, "SUCCESS", pluginResult["status"])
				data := pluginResult["data"].(map[string]any)
				assert.Equal(t, "", data["text"])
				assert.Empty(t, data["segments"])
				assert.Equal(t, "", data["language"])
			})
		}
	})
}

func xfyunPoll(t *testing.T, upstream *xfyunFakeUpstream, adaptor *taskjsplugin.TaskAdaptor, task *model.Task, response string) ([]byte, *relaycommon.TaskInfo) {
	t.Helper()
	upstream.setResult(t, response)
	resp, err := adaptor.FetchTask(upstream.server.URL, xfyunTestChannelKey, task, "")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	result, err := adaptor.ParseTaskResult(task, resp, body)
	require.NoError(t, err)
	task.Data = body
	return body, result
}

func TestXfyunASRPluginEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	plugin, _ := newXfyunTestPlugin(t)
	upstream := newXfyunFakeUpstream(t)
	content := []byte{0x00, 0x01, 'R', 'I', 'F', 'F', 0xff, 0x7f, '\n'}
	upstream.setResult(t, xfyunResultResponse(t, 3, ""))

	c := xfyunMultipartRequest(t, content)
	decoded, err := xfyunDecodeRequest(t, plugin, xfyunMultipartBody(content))
	require.NoError(t, err)
	require.Equal(t, "submit", decoded["kind"])
	require.Equal(t, "transcription", decoded["action"])
	c.Set("task_request", decoded["requestBody"])
	c.Set("task_action", "transcription")

	adaptor := taskjsplugin.New(plugin)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.server.URL, ApiKey: xfyunTestChannelKey, UpstreamModelName: xfyunTestModel},
		OriginModelName: xfyunTestModel,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: xfyunTestPublicTask},
	}
	adaptor.Init(info)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, "transcription", info.Action)

	requestBody, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	response, err := adaptor.DoRequest(c, info, requestBody)
	require.NoError(t, err)
	parsed, taskErr := adaptor.ParseResponse(c, response, info)
	require.Nil(t, taskErr)
	require.NotNil(t, parsed)
	require.Equal(t, xfyunTestOrderID, parsed.UpstreamTaskID)

	upload := upstream.upload(t)
	assert.Equal(t, content, upload.body, "the upload body is the raw audio file, byte for byte")
	assert.Equal(t, "application/octet-stream", upload.header.Get("Content-Type"))
	uploadParams := xfyunQueryParams(t, upload)
	assert.Equal(t, xfyunTestAppID, uploadParams.Get("appId"))
	assert.Equal(t, xfyunTestAccessKey, uploadParams.Get("accessKeyId"))
	assert.Regexp(t, `^[A-Za-z0-9]{16}$`, uploadParams.Get("signatureRandom"))
	assert.Equal(t, strconv.Itoa(len(content)), uploadParams.Get("fileSize"))
	assert.Equal(t, xfyunTestFileName, uploadParams.Get("fileName"))
	assert.Equal(t, "autodialect", uploadParams.Get("language"))
	assert.Equal(t, "true", uploadParams.Get("durationCheckDisable"))
	assert.NotContains(t, uploadParams, "duration", "durationCheckDisable makes duration unnecessary")
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+0800$`, uploadParams.Get("dateTime"))
	require.Len(t, uploadParams, 8)

	task := &model.Task{
		TaskID:      xfyunTestPublicTask,
		Platform:    "xfyun-asr",
		Action:      "transcription",
		Status:      model.TaskStatusInProgress,
		Properties:  model.Properties{OriginModelName: xfyunTestModel, UpstreamModelName: xfyunTestModel},
		PrivateData: model.TaskPrivateData{UpstreamTaskID: parsed.UpstreamTaskID, PluginState: parsed.PluginState},
	}

	_, result := xfyunPoll(t, upstream, adaptor, task, xfyunResultResponse(t, 3, ""))
	assert.Equal(t, "IN_PROGRESS", result.Status)

	query := upstream.lastQuery(t)
	queryParams := xfyunQueryParams(t, query)
	assert.Equal(t, uploadParams.Get("signatureRandom"), queryParams.Get("signatureRandom"), "polling must reuse the upload nonce")
	assert.NotEqual(t, uploadParams.Get("dateTime"), queryParams.Get("dateTime"), "each request regenerates dateTime")
	assert.Equal(t, xfyunTestOrderID, queryParams.Get("orderId"))
	assert.Equal(t, "transfer", queryParams.Get("resultType"))
	require.Len(t, queryParams, 6)
	assert.Equal(t, "application/json", query.header.Get("Content-Type"))
	assert.JSONEq(t, `{}`, string(query.body))

	renderedPending := xfyunRenderHook(t, plugin, map[string]any{"task_id": xfyunTestPublicTask, "status": "IN_PROGRESS", "data": map[string]any{}})
	assert.Empty(t, renderedPending, "a non-terminal render must be an empty object")

	latticeBody, latticeResult := xfyunPoll(t, upstream, adaptor, task, xfyunResultResponse(t, 4, xfyunOverEscaped(t, xfyunLatticeSample(t))))
	assert.Equal(t, "SUCCESS", latticeResult.Status)
	require.NotNil(t, latticeResult.UsageFacts)
	assert.Equal(t, float64(8), latticeResult.UsageFacts["duration"], "settlement uses originalDuration in seconds")

	latticeText := "你好世界第二段"
	rendered := xfyunRenderHook(t, plugin, map[string]any{"task_id": xfyunTestPublicTask, "status": "SUCCESS", "data": xfyunJSONMap(t, mustUnmarshalJSON(t, latticeBody))})
	assert.Equal(t, "succeeded", rendered["status"])
	assert.Equal(t, latticeText, rendered["text"])
	assert.Equal(t, float64(8), rendered["duration"])
	assert.Equal(t, "", rendered["language"])
	renderedSegments := rendered["segments"].([]any)
	require.Len(t, renderedSegments, 2)
	assert.Equal(t, map[string]any{"id": float64(0), "start": float64(0), "end": float64(3), "text": "你好世界"}, renderedSegments[0])
	assert.Equal(t, map[string]any{"id": float64(1), "start": float64(3), "end": float64(5), "text": "第二段"}, renderedSegments[1])

	successTask := *task
	successTask.Status = model.TaskStatusSuccess
	successTask.Progress = "100%"
	successTask.FinishTime = 100
	renderedJSON, err := adaptor.ConvertToOpenAIAudioTask(&successTask)
	require.NoError(t, err)
	var renderedObject map[string]any
	require.NoError(t, common.Unmarshal(renderedJSON, &renderedObject))
	assert.Equal(t, xfyunTestPublicTask, renderedObject["id"])
	assert.Equal(t, "audio.transcription", renderedObject["object"])
	assert.Equal(t, "succeeded", renderedObject["status"])
	assert.Equal(t, xfyunTestModel, renderedObject["model"])
	assert.Equal(t, latticeText, renderedObject["text"])
	assert.Equal(t, float64(8), renderedObject["duration"])

	lattice2Body, lattice2Result := xfyunPoll(t, upstream, adaptor, task, xfyunResultResponse(t, 4, xfyunOverEscaped(t, xfyunLattice2BeginSample(t))))
	assert.Equal(t, "SUCCESS", lattice2Result.Status)
	renderedLattice2 := xfyunRenderHook(t, plugin, map[string]any{"task_id": xfyunTestPublicTask, "status": "SUCCESS", "data": xfyunJSONMap(t, mustUnmarshalJSON(t, lattice2Body))})
	assert.Equal(t, latticeText, renderedLattice2["text"], "lattice2 alone must yield the same transcription")
	segments := renderedLattice2["segments"].([]any)
	require.Len(t, segments, 2)
	first := segments[0].(map[string]any)
	assert.Equal(t, float64(0), first["start"])
	assert.Equal(t, float64(3), first["end"])

	failureBody, failureResult := xfyunPoll(t, upstream, adaptor, task, `{"code":100001,"descInfo":"order not found","content":{}}`)
	assert.Equal(t, "FAILURE", failureResult.Status, "a hard business error must not be treated as in progress")
	assert.Contains(t, failureResult.Reason, "100001")

	failureTask := *task
	failureTask.Status = model.TaskStatusFailure
	failureTask.FailReason = failureResult.Reason
	failureTask.Data = failureBody
	failureJSON, err := adaptor.ConvertToOpenAIAudioTask(&failureTask)
	require.NoError(t, err)
	var failureObject map[string]any
	require.NoError(t, common.Unmarshal(failureJSON, &failureObject))
	assert.Equal(t, "failed", failureObject["status"])
	failureError := failureObject["error"].(map[string]any)
	assert.Equal(t, "100001", failureError["code"])
	assert.Contains(t, failureError["message"], "100001")
}

func mustUnmarshalJSON(t *testing.T, body []byte) any {
	t.Helper()
	var value any
	require.NoError(t, common.Unmarshal(body, &value))
	return value
}
