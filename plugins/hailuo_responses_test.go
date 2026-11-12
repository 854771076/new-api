package plugins_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHailuoResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "hailuo",
		model:     "MiniMax-Hailuo-2.3",
		requestBody: map[string]any{
			"model": "MiniMax-Hailuo-2.3",
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "ocean at sunset"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds": 10,
			"size":    "1920x1080",
		},
		wantAction: "image_to_video",
		wantRequest: map[string]any{
			"model":    "MiniMax-Hailuo-2.3",
			"prompt":   "ocean at sunset",
			"images":   []any{"https://cdn.example/frame.png"},
			"duration": float64(10),
			"size":     "1920x1080",
			"metadata": map[string]any{"first_frame_image": "https://cdn.example/frame.png"},
		},
		wantUsageKeys:  []string{"input_images", "input_video_seconds", "resolution", "seconds"},
		wantVendorName: "hailuo",
		// The long-text speech models come in at the plugin's own route.
		nativeOnlyModels: []string{"speech-2.8-hd", "speech-2.8-turbo", "speech-2.6-hd", "speech-2.6-turbo"},
	})
}

// Every model selects only the dimensions it can actually bill, while
// submission still reports the union so prices saved against the previous
// schema keep evaluating.
func TestHailuoUsageProfilesPerModel(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	families := []struct {
		models      []string
		fields      []string
		resolutions []string
	}{
		{
			models:      []string{"MiniMax-H3"},
			fields:      []string{"input_images", "input_video_seconds", "resolution", "seconds"},
			resolutions: []string{"480P", "768P", "2K"},
		},
		{
			models:      []string{"MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"},
			fields:      []string{"input_images", "input_video_seconds", "resolution", "seconds"},
			resolutions: []string{"480P", "768P"},
		},
		{
			models:      []string{"MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast"},
			fields:      []string{"resolution", "seconds"},
			resolutions: []string{"768P", "1080P"},
		},
		{
			models:      []string{"MiniMax-Hailuo-02"},
			fields:      []string{"resolution", "seconds"},
			resolutions: []string{"512P", "768P", "1080P"},
		},
		{
			models: []string{"T2V-01-Director", "T2V-01", "I2V-01-Director", "I2V-01-live", "I2V-01", "S2V-01"},
			fields: []string{"seconds"},
		},
		{
			models: []string{"speech-2.8-hd"},
			fields: []string{"characters"},
		},
		{
			models: []string{"speech-2.8-turbo"},
			fields: []string{"characters"},
		},
		{
			models: []string{"speech-2.6-hd"},
			fields: []string{"characters"},
		},
		{
			models: []string{"speech-2.6-turbo"},
			fields: []string{"characters"},
		},
	}
	profiled := make([]string, 0, len(plugin.Meta.Models))
	for _, family := range families {
		for _, name := range family.models {
			profiled = append(profiled, name)
			t.Run(name, func(t *testing.T) {
				schema, examples := plugin.Meta.UsageForModel(name)
				assert.Equal(t, family.fields, slices.Sorted(maps.Keys(schema)))
				assert.Equal(t, family.resolutions, schema["resolution"].Enum)
				assert.NotEmpty(t, examples)
			})
		}
	}
	assert.ElementsMatch(t, plugin.Meta.Models, profiled, "every declared model must select a usage profile")

	t.Run("submission keeps facts outside the selected profile billable", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: "https://api.minimax.example", UpstreamModelName: "MiniMax-Hailuo-2.3"},
			OriginModelName: "MiniMax-Hailuo-2.3",
			TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
		}
		adaptor := taskplugin.New(plugin)
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Set("task_request", map[string]any{"model": "MiniMax-Hailuo-2.3", "prompt": "p", "duration": 10, "resolution": "768P"})
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

		facts, err := adaptor.ExtractUsageFactsValidated(c, info)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"seconds": float64(10), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}, facts)
	})
}

// MiniMax serves v1 videos only through the signed download_url of
// /v1/files/retrieve; that link is signed for GET, so HEAD clients get GET too.
func TestHailuoArtifactContentProxy(t *testing.T) {
	service.InitHttpClient()
	const signedURL = "https://bucket.oss-cn-wulanchabu.aliyuncs.com/video.mp4?Expires=1&OSSAccessKeyId=key&Signature=sig"
	retrieveBodies := map[string]string{
		"file/with space": `{"file":{"file_id":1,"download_url":"` + signedURL + `"},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		"login":           `{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`,
		"missing":         `{"file":{"file_id":3},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		"html":            `<html>gateway error</html>`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/files/retrieve", r.URL.Path)
		assert.Equal(t, "Bearer test-ak", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(retrieveBodies[r.URL.Query().Get("file_id")]))
	}))
	defer upstream.Close()
	adaptor := taskplugin.New(loadHailuoPlugin(t))
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: upstream.URL},
	})

	for _, tc := range []struct {
		fileID  string
		wantErr string
	}{
		{fileID: "file/with space"},
		{fileID: "login", wantErr: "MiniMax file retrieve failed: login fail"},
		{fileID: "missing", wantErr: "artifact_not_found"},
		{fileID: "html", wantErr: "MiniMax file retrieve returned an unexpected response: HTTP 200"},
	} {
		t.Run(tc.fileID, func(t *testing.T) {
			data, err := common.Marshal(map[string]any{"file_id": tc.fileID})
			require.NoError(t, err)
			task := &model.Task{TaskID: "task-public", Status: model.TaskStatusSuccess, Data: data}
			artifacts, err := adaptor.ListArtifacts(task)
			require.NoError(t, err)
			assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)

			descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, &channel.TaskContentRequest{URL: signedURL, Method: http.MethodGet, Credentialless: true}, descriptor)
		})
	}
}

func loadHailuoPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("hailuo")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
	require.NoError(t, err)
	return plugin
}

func callHailuoHook(t *testing.T, plugin *jsplugin.LoadedPlugin, hook string, args ...any) map[string]any {
	t.Helper()
	value, err := plugin.Engine.Call(t.Context(), hook, args...)
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	return decoded
}

func hailuoH3SubmitContext(requestBody map[string]any) map[string]any {
	return map[string]any{
		"requestBody":   requestBody,
		"model":         "MiniMax-H3",
		"upstreamModel": "MiniMax-H3",
		"baseUrl":       "https://api.minimax.example",
		"apiKey":        "test-ak",
	}
}

// MiniMax-H3 submits to /v2/video_generation with a multimodal content array
// instead of the flat /v1 frame fields.
func TestHailuoH3BuildSubmitRequest(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name       string
		request    map[string]any
		wantBody   string
		wantAction string
	}{
		{
			name:       "text to video defaults duration ratio and resolution",
			request:    map[string]any{"prompt": "a boy playing basketball"},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"a boy playing basketball"}],"resolution":"768P","duration":5,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name:       "2K resolution from size",
			request:    map[string]any{"prompt": "p", "duration": 15, "size": "2K"},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"2K","duration":15,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name:    "first and last frame from metadata",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"first_frame_image": "first.png", "last_frame_image": "last.png"}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"first_frame","image_url":{"url":"first.png"}},
				{"type":"image_url","role":"last_frame","image_url":{"url":"last.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name:    "frames from the images array",
			request: map[string]any{"prompt": "p", "images": []any{"first.png", "last.png"}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"first_frame","image_url":{"url":"first.png"}},
				{"type":"image_url","role":"last_frame","image_url":{"url":"last.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "reference video and audio",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{
				"reference_video": "ref.mp4",
				"reference_audio": []any{"a.mp3", "b.mp3"},
			}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"video_url","role":"reference_video","video_url":{"url":"ref.mp4"}},
				{"type":"audio_url","role":"reference_audio","audio_url":{"url":"a.mp3"}},
				{"type":"audio_url","role":"reference_audio","audio_url":{"url":"b.mp3"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "content passthrough prepends the prompt when no text item exists",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "img.png"}},
			}}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"reference_image","image_url":{"url":"img.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "content passthrough keeps an existing text item",
			request: map[string]any{"prompt": "ignored", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "kept"},
			}}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"kept"}],"resolution":"768P","duration":5,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name: "explicit ratio and watermark; no callback with MiniMax's task ids",
			request: map[string]any{"prompt": "p", "duration": 4, "metadata": map[string]any{
				"ratio":          "9:16",
				"callback_url":   "https://example.com/cb",
				"aigc_watermark": true,
			}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"768P","duration":4,"ratio":"9:16","aigc_watermark":true}`,
			wantAction: "text_to_video",
		},
		{
			name:    "audio alone is a reference that defaults to adaptive",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"reference_audio": "voice.mp3"}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"audio_url","role":"reference_audio","audio_url":{"url":"voice.mp3"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "text_to_video",
		},
		// MiniMax checks the ratio and the roles of the content.
		{
			name:       "a ratio MiniMax does not list",
			request:    map[string]any{"prompt": "p", "metadata": map[string]any{"ratio": "16:10"}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"768P","duration":5,"ratio":"16:10"}`,
			wantAction: "text_to_video",
		},
		{
			name:       "adaptive for text alone",
			request:    map[string]any{"prompt": "p", "metadata": map[string]any{"ratio": "adaptive"}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "text_to_video",
		},
		{
			name: "a middle frame",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "middle_frame", "image_url": map[string]any{"url": "u"}},
			}}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"middle_frame","image_url":{"url":"u"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoH3SubmitContext(testCase.request))
			assert.Equal(t, "https://api.minimax.example/v2/video_generation", descriptor["url"])
			assert.Equal(t, "POST", descriptor["method"])
			assert.Equal(t, testCase.wantAction, descriptor["action"])
			body, err := common.Marshal(descriptor["body"])
			require.NoError(t, err)
			assert.JSONEq(t, testCase.wantBody, string(body))
		})
	}
}

// Every MiniMax-H3 request bound is rejected before the upstream call, so an
// out-of-range duration can never reach quota calculation as a billing fact.
func TestHailuoH3RejectsOutOfContractRequests(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	tenReferenceImages := make([]any, 0, 10)
	for range 10 {
		tenReferenceImages = append(tenReferenceImages, map[string]any{
			"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "u"},
		})
	}
	testCases := []struct {
		name    string
		request map[string]any
		wantErr string
	}{
		{"duration below the minimum", map[string]any{"prompt": "p", "duration": 3}, "duration must be an integer between 4 and 15"},
		{"duration above the maximum", map[string]any{"prompt": "p", "duration": 16}, "duration must be an integer between 4 and 15"},
		{"fractional duration", map[string]any{"prompt": "p", "duration": 5.5}, "duration must be an integer between 4 and 15"},
		{"unsupported resolution", map[string]any{"prompt": "p", "size": "1080P"}, "resolution must be 480P, 768P or 2K"},
		{"too many frame images", map[string]any{"prompt": "p", "images": []any{"a.png", "b.png", "c.png"}}, "at most 2 frame images"},
		{"media without text", map[string]any{"images": []any{"a.png"}}, "requires a non-empty text item"},
		{"content is not an array", map[string]any{"prompt": "p", "metadata": map[string]any{"content": "nope"}}, "metadata.content must be an array"},
		{
			name: "multiple first frame roles",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "u1"}},
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "u2"}},
			}}},
			wantErr: "at most one first_frame image",
		},
		{
			name: "passthrough mixes frame and reference media",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "frame"}},
				map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "reference"}},
			}}},
			wantErr: "cannot mix frame images with reference media",
		},
		{
			name: "assembled content mixes frame and reference media",
			request: map[string]any{"prompt": "p", "images": []any{"frame"}, "metadata": map[string]any{
				"reference_video": "reference.mp4",
			}},
			wantErr: "cannot mix frame images with reference media",
		},
		{
			name: "too many reference videos",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u1"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u2"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u3"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u4"}},
			}}},
			wantErr: "at most 3 reference videos",
		},
		{
			name:    "too many reference images",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": tenReferenceImages}},
			wantErr: "at most 9 reference images",
		},
		{
			name:    "too many reference audios",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"reference_audio": []any{"a.mp3", "b.mp3", "c.mp3", "d.mp3"}}},
			wantErr: "at most 3 reference audios",
		},
		{"empty input", map[string]any{"prompt": "  "}, "requires a prompt or a media input"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", hailuoH3SubmitContext(testCase.request))
			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

// The /v1 models keep the flat request shape and endpoint.
func TestHailuoLegacySubmitRequestUnchanged(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	ctx := hailuoH3SubmitContext(map[string]any{"prompt": "p", "duration": 10, "size": "768P"})
	ctx["model"] = "MiniMax-Hailuo-2.3"
	ctx["upstreamModel"] = "MiniMax-Hailuo-2.3"
	descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)
	assert.Equal(t, "https://api.minimax.example/v1/video_generation", descriptor["url"])
	body, err := common.Marshal(descriptor["body"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"MiniMax-Hailuo-2.3","prompt":"p","duration":10,"resolution":"768P"}`, string(body))
}

func TestHailuoQueryRequestEndpointByModel(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name string
		ctx  map[string]any
		want string
	}{
		{
			name: "H3 uses the v2 path parameter",
			ctx:  map[string]any{"taskId": "task/1", "upstreamModel": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/task%2F1",
		},
		{
			name: "an unmapped task falls back to the origin model",
			ctx:  map[string]any{"taskId": "t1", "model": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/t1",
		},
		{
			name: "legacy models keep the v1 query parameter",
			ctx:  map[string]any{"taskId": "t1", "upstreamModel": "MiniMax-Hailuo-2.3"},
			want: "https://api.minimax.example/v1/query/video_generation?task_id=t1",
		},
		{
			name: "a channel-mapped alias resolves through the upstream model",
			ctx:  map[string]any{"taskId": "t1", "model": "h3", "upstreamModel": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/t1",
		},
		{
			name: "a New API gateway serves H3 on the prefixed v2 route",
			ctx:  map[string]any{"taskId": "t1", "upstreamModel": "MiniMax-H3", "upstream": map[string]any{"kind": "new_api"}},
			want: "https://api.minimax.example/hailuo/v2/query/video_generation/t1",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.ctx["baseUrl"] = "https://api.minimax.example"
			testCase.ctx["apiKey"] = "test-ak"
			descriptor := callHailuoHook(t, plugin, "buildQueryRequest", testCase.ctx)
			assert.Equal(t, testCase.want, descriptor["url"])
			assert.Equal(t, "GET", descriptor["method"])
		})
	}
}

func TestHailuoParseTaskResult(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name       string
		body       string
		wantStatus string
		wantURL    string
		wantReason string
	}{
		{"H3 queued", `{"task":{"id":"1","status":"queued"}}`, "QUEUED", "", ""},
		{"H3 running", `{"task":{"id":"1","status":"running"}}`, "IN_PROGRESS", "", ""},
		{"H3 succeeded", `{"task":{"id":"1","status":"succeeded","content":{"url":"https://cdn.example/h3.mp4"}}}`, "SUCCESS", "https://cdn.example/h3.mp4", ""},
		{"H3 failed", `{"task":{"id":"1","status":"failed","error":{"code":"1026","message":"sensitive content"}}}`, "FAILURE", "", "sensitive content"},
		{"H3 cancelled", `{"task":{"id":"1","status":"cancelled"}}`, "FAILURE", "", "task cancelled"},
		{"H3 permanent query error", `{"type":"error","error":{"type":"authorized_error","message":"login failed","http_code":"401"}}`, "FAILURE", "", "login failed"},
		{"legacy success", `{"task_id":"1","status":"Success","file_id":"f1","base_resp":{"status_code":0}}`, "SUCCESS", "", ""},
		{"legacy processing", `{"task_id":"1","status":"Processing","base_resp":{"status_code":0}}`, "IN_PROGRESS", "", ""},
		{"H3 unrecognized", `{"task":{"id":"1","status":"weird"}}`, "UNKNOWN", "", "unrecognized status: weird"},
		{"legacy unrecognized", `{"task_id":"1","status":"Weird","base_resp":{"status_code":0}}`, "UNKNOWN", "", "unrecognized status: Weird"},
		{"legacy base_resp failure", `{"task_id":"1","status":"Success","base_resp":{"status_code":1001,"status_msg":"upstream down"}}`, "FAILURE", "", "upstream down"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var body any
			require.NoError(t, common.UnmarshalJsonStr(testCase.body, &body))
			result := callHailuoHook(t, plugin, "parseTaskResult", map[string]any{}, body)
			assert.Equal(t, testCase.wantStatus, result["status"])
			assert.Equal(t, testCase.wantURL, common.Interface2String(result["url"]))
			assert.Equal(t, testCase.wantReason, common.Interface2String(result["reason"]))
		})
	}
	t.Run("H3 retryable query error", func(t *testing.T) {
		var body any
		require.NoError(t, common.UnmarshalJsonStr(
			`{"type":"error","error":{"type":"rate_limit_error","message":"retry later","http_code":"429"}}`, &body))
		_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, body)
		require.ErrorContains(t, err, "retry later")
	})
}

func TestHailuoExtractUsageFacts(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	nineReferenceImages := make([]any, 0, 9)
	for range 9 {
		nineReferenceImages = append(nineReferenceImages, map[string]any{
			"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "image"},
		})
	}
	testCases := []struct {
		name    string
		model   string
		request map[string]any
		want    map[string]any
	}{
		{"H3 defaults", "MiniMax-H3", map[string]any{"prompt": "p"}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
		{"H3 2K", "MiniMax-H3", map[string]any{"prompt": "p", "duration": 12, "size": "2K"}, map[string]any{
			"seconds": float64(12), "resolution": "2K", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
		{"H3 reference images", "MiniMax-H3", map[string]any{"prompt": "p", "metadata": map[string]any{"content": nineReferenceImages}}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(9), "input_video_seconds": float64(0),
		}},
		{"H3 reference video reserves total duration limit", "MiniMax-H3", map[string]any{"prompt": "p", "metadata": map[string]any{
			"reference_video": []any{"one.mp4", "two.mp4", "three.mp4"},
		}}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(15),
		}},
		{"legacy model", "MiniMax-Hailuo-2.3", map[string]any{"prompt": "p", "duration": 10}, map[string]any{
			"seconds": float64(10), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := hailuoH3SubmitContext(testCase.request)
			ctx["model"] = testCase.model
			ctx["upstreamModel"] = testCase.model
			assert.Equal(t, testCase.want, callHailuoHook(t, plugin, "extractUsage", ctx))
		})
	}
}

func TestHailuoH3CompletionUsageFacts(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name string
		body string
		want map[string]any
	}{
		{
			name: "actual usage replaces submission estimates",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":5,"input_seconds":7.5,"input_image_count":6}}}`,
			want: map[string]any{"seconds": float64(5), "resolution": "2K", "input_images": float64(6), "input_video_seconds": float64(7.5)},
		},
		{
			name: "zero actual usage is retained for settlement",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P","usage":{"output_seconds":4,"input_seconds":0,"input_image_count":0}}}`,
			want: map[string]any{"seconds": float64(4), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "zero output cannot erase the submission reservation",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P","usage":{"output_seconds":0,"input_seconds":0,"input_image_count":0}}}`,
			want: map[string]any{"resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "missing usage leaves submission estimates untouched",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P"}}`,
			want: map[string]any{"resolution": "768P"},
		},
		{
			name: "out of contract usage cannot become a billing multiplier",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":16,"input_seconds":16,"input_image_count":10}}}`,
			want: map[string]any{"resolution": "2K"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var body any
			require.NoError(t, common.UnmarshalJsonStr(testCase.body, &body))
			assert.Equal(t, testCase.want, callHailuoHook(t, plugin, "extractUsageOnComplete", nil, nil, body))
		})
	}

	t.Run("polling adaptor carries actual facts into task settlement", func(t *testing.T) {
		adaptor := taskplugin.New(plugin)
		result, err := adaptor.ParseTaskResult(&model.Task{}, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, []byte(
			`{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":5,"input_seconds":7.5,"input_image_count":6}}}`,
		))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"seconds": float64(5), "resolution": "2K", "input_images": float64(6), "input_video_seconds": float64(7.5),
		}, result.UsageFacts)
	})
}

// The /v2 result is a public CDN URL, so its artifact is proxied without
// channel credentials instead of through the /v1 file download endpoint.
func TestHailuoH3ArtifactContentProxy(t *testing.T) {
	source, err := builtinplugins.Source("hailuo")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
	require.NoError(t, err)
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: "https://api.minimax.example"},
	})
	task := &model.Task{
		TaskID: "task-public",
		Status: model.TaskStatusSuccess,
		Data:   []byte(`{"task":{"id":"1","status":"succeeded","content":{"url":"https://cdn.example/h3.mp4"}}}`),
	}

	artifacts, err := adaptor.ListArtifacts(task)
	require.NoError(t, err)
	assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)

	descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	require.NotNil(t, descriptor)
	assert.Equal(t, "https://cdn.example/h3.mp4", descriptor.URL)
	assert.True(t, descriptor.Credentialless)

	pending := &model.Task{TaskID: "task-public", Status: model.TaskStatusInProgress, Data: task.Data}
	pendingArtifacts, err := adaptor.ListArtifacts(pending)
	require.NoError(t, err)
	assert.Empty(t, pendingArtifacts)
}

// The /v2 create response carries only task_id, without the /v1 base_resp envelope.
func TestHailuoParseSubmitResponse(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	t.Run("H3 create has no envelope", func(t *testing.T) {
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"task_id": "h3-1"}})
		assert.Equal(t, "h3-1", parsed["taskId"])
	})
	t.Run("H3 rejection reports the envelope message", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"base_resp": map[string]any{"status_code": 2013, "status_msg": "invalid params"}}})
		require.ErrorContains(t, err, "invalid params")
	})
	t.Run("H3 rejection reports the v2 error message", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"type": "error", "error": map[string]any{
				"type": "bad_request_error", "message": "content requires text", "http_code": "400",
			}}})
		require.ErrorContains(t, err, "content requires text")
	})
	t.Run("legacy create", func(t *testing.T) {
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"},
			map[string]any{"body": map[string]any{"task_id": "v1-1", "base_resp": map[string]any{"status_code": 0}}})
		assert.Equal(t, "v1-1", parsed["taskId"])
	})
	t.Run("legacy response without an envelope is rejected", func(t *testing.T) {
		ctx := map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"}
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx,
			map[string]any{"body": map[string]any{"task_id": "v1-1"}})
		require.ErrorContains(t, err, "hailuo submit failed")
	})
	t.Run("legacy error", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"},
			map[string]any{"body": map[string]any{"base_resp": map[string]any{"status_code": 1026, "status_msg": "sensitive"}}})
		require.ErrorContains(t, err, "sensitive")
	})
}

// Without an H3 exemption the shared /v1 combo table would reject every H3
// duration on the OpenAI Video path before the request is ever built.
func TestHailuoH3PassesOpenAIVideoDecode(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"},
		map[string]any{
			"body":          map[string]any{"kind": "json", "value": map[string]any{"model": "MiniMax-H3", "prompt": "p", "seconds": 12, "size": "2K"}},
			"model":         "MiniMax-H3",
			"upstreamModel": "MiniMax-H3",
		})
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var intent map[string]any
	require.NoError(t, common.Unmarshal(encoded, &intent))
	assert.Equal(t, "submit", intent["kind"])
	requestBody, ok := intent["requestBody"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(12), requestBody["duration"])
}

// A New API channel reaches this plugin on another gateway. The downstream
// driver addresses the gateway's /hailuo routes, whose decoders give the
// gateway's driver the same request and whose presenters give the downstream
// driver the same task state, so MiniMax receives one body and both gateways
// settle on the same facts.
func TestHailuoThroughNewAPIGateway(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	call := func(export string, members []string, args ...any) map[string]any {
		t.Helper()
		value, err := plugin.Engine.CallPath(t.Context(), export, members, args...)
		require.NoError(t, err)
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(encoded, &decoded))
		return decoded
	}

	for _, tc := range []struct {
		name        string
		model       string
		requestBody map[string]any
		route       string
		decoder     string
		vendorPath  string
	}{
		{
			name:  "v1 image to video",
			model: "MiniMax-Hailuo-02",
			requestBody: map[string]any{"model": "MiniMax-Hailuo-02", "prompt": "p", "duration": 10, "size": "512P", "metadata": map[string]any{
				"first_frame_image": "https://cdn.example/first.png", "prompt_optimizer": false,
			}},
			route:      "/hailuo/v1/video_generation",
			decoder:    "createTask",
			vendorPath: "/v1/video_generation",
		},
		{
			name:  "H3 reference video",
			model: "MiniMax-H3",
			requestBody: map[string]any{"model": "MiniMax-H3", "prompt": "p", "duration": 8, "size": "2K", "metadata": map[string]any{
				"reference_video": "https://cdn.example/ref.mp4", "ratio": "9:16", "aigc_watermark": true,
			}},
			route:      "/hailuo/v2/video_generation",
			decoder:    "createH3Task",
			vendorPath: "/v2/video_generation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downstream := map[string]any{"requestBody": tc.requestBody, "model": tc.model, "upstreamModel": tc.model,
				"baseUrl": "https://gateway.example", "apiKey": "sk-gateway", "upstream": map[string]any{"kind": "new_api"}}
			sent := call("buildSubmitRequest", nil, downstream)
			assert.Equal(t, "https://gateway.example"+tc.route, sent["url"])

			intent := call("native", []string{tc.decoder}, map[string]any{"body": map[string]any{"kind": "json", "value": sent["body"]}})
			assert.Equal(t, tc.model, intent["model"])
			assert.Equal(t, sent["action"], intent["action"])
			upstream := map[string]any{"requestBody": intent["requestBody"], "model": tc.model, "upstreamModel": tc.model,
				"baseUrl": "https://api.minimax.example", "apiKey": "vendor-key", "upstream": map[string]any{"kind": "vendor"}}
			forwarded := call("buildSubmitRequest", nil, upstream)
			assert.Equal(t, "https://api.minimax.example"+tc.vendorPath, forwarded["url"])
			assert.Equal(t, sent["body"], forwarded["body"])
			assert.Equal(t, call("extractUsage", nil, downstream), call("extractUsage", nil, upstream))
		})
	}

	t.Run("create responses", func(t *testing.T) {
		for model, data := range map[string]map[string]any{
			"MiniMax-Hailuo-02": {"task_id": "task_pub", "base_resp": map[string]any{"status_code": 0, "status_msg": "success"}},
			"MiniMax-H3":        {"task_id": "task_pub"},
		} {
			created := call("native", []string{"taskCreated"}, map[string]any{}, map[string]any{"task_id": "task_pub", "status": "SUBMITTED", "data": data})
			parsed := call("parseSubmitResponse", nil, map[string]any{"upstreamModel": model}, map[string]any{"body": created})
			assert.Equal(t, "task_pub", parsed["taskId"], model)
		}
	})

	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeNewAPI, ApiKey: "sk-gateway", ChannelBaseUrl: "https://gateway.example",
	}})
	success := map[string]any{"status_code": 0, "status_msg": "success"}
	for _, tc := range []struct {
		name        string
		presenter   string
		task        map[string]any
		wantStatus  string
		wantReason  string
		wantContent *channel.TaskContentRequest
	}{
		{
			name: "v1 before the first poll", presenter: "taskStatus", wantStatus: "IN_PROGRESS",
			task: map[string]any{"status": "SUBMITTED", "data": map[string]any{"task_id": "task_pub", "base_resp": success}},
		},
		{
			name: "v1 succeeded", presenter: "taskStatus", wantStatus: "SUCCESS",
			task: map[string]any{"status": "SUCCESS", "data": map[string]any{
				"task_id": "task_pub", "status": "Success", "file_id": "f1", "video_width": 1920, "video_height": 1080, "base_resp": success,
			}},
			wantContent: &channel.TaskContentRequest{
				URL:     "https://gateway.example/v1/tasks/task_pub/artifacts/video/content",
				Method:  http.MethodHead,
				Headers: map[string]string{"Authorization": "Bearer sk-gateway"},
			},
		},
		{
			name: "v1 failed by the gateway poller", presenter: "taskStatus", wantStatus: "FAILURE", wantReason: "task failed",
			task: map[string]any{"status": "FAILURE", "fail_reason": "task timed out", "data": map[string]any{"task_id": "task_pub", "status": "Processing", "base_resp": success}},
		},
		{
			name: "H3 before the first poll", presenter: "h3TaskStatus", wantStatus: "QUEUED",
			task: map[string]any{"status": "SUBMITTED", "data": map[string]any{"task_id": "task_pub"}},
		},
		{
			name: "H3 succeeded", presenter: "h3TaskStatus", wantStatus: "SUCCESS",
			task: map[string]any{"status": "SUCCESS", "data": map[string]any{"task": map[string]any{
				"id": "task_pub", "status": "succeeded", "resolution": "2K", "content": map[string]any{"url": "https://cdn.example/h3.mp4"},
				"usage": map[string]any{"output_seconds": 8, "input_seconds": 7.5, "input_image_count": 0},
			}}},
			wantContent: &channel.TaskContentRequest{URL: "https://cdn.example/h3.mp4", Method: http.MethodHead, Credentialless: true},
		},
		{
			name: "H3 failed by the gateway poller", presenter: "h3TaskStatus", wantStatus: "FAILURE", wantReason: "task timed out",
			task: map[string]any{"status": "FAILURE", "fail_reason": "task timed out", "data": map[string]any{"task": map[string]any{"id": "task_pub", "status": "running"}}},
		},
		{
			name: "H3 failed by MiniMax", presenter: "h3TaskStatus", wantStatus: "FAILURE", wantReason: "sensitive content",
			task: map[string]any{"status": "FAILURE", "fail_reason": "sensitive content", "data": map[string]any{"task": map[string]any{
				"id": "task_pub", "status": "failed", "error": map[string]any{"code": "1026", "message": "sensitive content"},
			}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.task["task_id"] = "task_pub"
			rendered := call("native", []string{tc.presenter}, map[string]any{}, tc.task)
			result := call("parseTaskResult", nil, map[string]any{}, rendered, map[string]any{"status": http.StatusOK})
			assert.Equal(t, tc.wantStatus, result["status"])
			assert.Equal(t, tc.wantReason, common.Interface2String(result["reason"]))
			if tc.wantContent == nil {
				return
			}
			facts := call("extractUsageOnComplete", nil, nil, nil, rendered)
			assert.NotEmpty(t, facts)
			assert.Equal(t, call("extractUsageOnComplete", nil, nil, nil, tc.task["data"]), facts)

			data, err := common.Marshal(rendered)
			require.NoError(t, err)
			task := &model.Task{TaskID: "task_down", Status: model.TaskStatusSuccess, Data: data, PrivateData: model.TaskPrivateData{UpstreamTaskID: "task_pub"}}
			artifacts, err := adaptor.ListArtifacts(task)
			require.NoError(t, err)
			assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
			descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
			require.NoError(t, err)
			assert.Equal(t, tc.wantContent, descriptor)
		})
	}

	// The gateway refuses a native body only when it cannot name the model,
	// price the request or provide what it asks for.
	t.Run("native bodies the gateway refuses before billing", func(t *testing.T) {
		text := []any{map[string]any{"type": "text", "text": "p"}}
		for _, tc := range []struct {
			decoder string
			body    map[string]any
			wantErr string
		}{
			{"createTask", map[string]any{"prompt": "p"}, "model is required"},
			{"createTask", map[string]any{"model": "MiniMax-Hailuo-02", "prompt": "p", "duration": 8}, "MiniMax-Hailuo-02 duration must be 6 or 10"},
			{"createTask", map[string]any{"model": "MiniMax-Hailuo-2.3-Fast", "prompt": "p"}, "supports image-to-video only"},
			{"createTask", map[string]any{"model": "MiniMax-Hailuo-02", "prompt": "p", "callback_url": "https://client.example/hook"}, "callback_url is not supported"},
			{"createH3Task", map[string]any{"model": "MiniMax-H3", "prompt": "p"}, "content must be an array"},
			{"createH3Task", map[string]any{"model": "MiniMax-H3", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "u"}}}}, "requires a non-empty text item"},
			{"createH3Task", map[string]any{"model": "MiniMax-H3", "content": text, "duration": 16}, "duration must be an integer between 4 and 15"},
			{"createH3Task", map[string]any{"model": "MiniMax-H3", "content": text, "duration": 5, "resolution": "768P", "ratio": "16:9", "callback_url": "https://client.example/hook"}, "callback_url is not supported"},
		} {
			value, err := plugin.Engine.CallPath(t.Context(), "native", []string{tc.decoder}, map[string]any{"body": map[string]any{"kind": "json", "value": tc.body}})
			if err == nil {
				intent := decodePluginValue(t, value)
				_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"requestBody": intent["requestBody"], "model": tc.body["model"],
					"upstreamModel": tc.body["model"], "baseUrl": "https://api.minimax.example", "apiKey": "vendor-key"})
			}
			require.ErrorContains(t, err, tc.wantErr)
		}
	})

	// A native body reaches MiniMax as the client sent it, for MiniMax to
	// check. The reservation reads its duration and resolution, takes the
	// model's defaults for those left out and the dearest the model renders
	// for the rest; H3 reserves its longest duration and 2K when left out.
	t.Run("native bodies are sent as is", func(t *testing.T) {
		text := []any{map[string]any{"type": "text", "text": "p"}}
		for _, tc := range []struct {
			name      string
			decoder   string
			body      map[string]any
			upstream  string
			wantURL   string
			wantFacts map[string]any
		}{
			{
				name: "H3 audio reference", decoder: "createH3Task", wantURL: "/v2/video_generation",
				body: map[string]any{"model": "MiniMax-H3", "duration": 6, "resolution": "2K", "content": []any{
					map[string]any{"type": "text", "text": "sing"},
					map[string]any{"type": "audio_url", "role": "reference_audio", "audio_url": map[string]any{"url": "https://cdn.example/voice.mp3"}},
				}},
				wantFacts: map[string]any{"seconds": float64(6), "resolution": "2K", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
			{
				name: "H3 without duration, resolution or ratio", decoder: "createH3Task", wantURL: "/v2/video_generation",
				body:      map[string]any{"model": "MiniMax-H3", "content": text},
				wantFacts: map[string]any{"seconds": float64(15), "resolution": "2K", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
			{
				name: "H3 with a lowercase resolution", decoder: "createH3Task", wantURL: "/v2/video_generation",
				body:      map[string]any{"model": "MiniMax-H3", "content": text, "duration": 5, "resolution": "768p"},
				wantFacts: map[string]any{"seconds": float64(5), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
			{
				name: "H3 with a middle frame", decoder: "createH3Task", wantURL: "/v2/video_generation",
				body: map[string]any{"model": "MiniMax-H3", "duration": 6, "resolution": "768P", "content": []any{
					map[string]any{"type": "text", "text": "p"},
					map[string]any{"type": "image_url", "role": "middle_frame", "image_url": map[string]any{"url": "https://cdn.example/m.png"}},
				}},
				wantFacts: map[string]any{"seconds": float64(6), "resolution": "768P", "input_images": float64(1), "input_video_seconds": float64(0)},
			},
			{
				name: "v1 ten seconds in 1080P", decoder: "createTask", wantURL: "/v1/video_generation",
				body:      map[string]any{"model": "MiniMax-Hailuo-2.3", "prompt": "p", "duration": 10, "resolution": "1080P"},
				wantFacts: map[string]any{"seconds": float64(10), "resolution": "1080P", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
			{
				name: "v1 resolution the model does not list", decoder: "createTask", wantURL: "/v1/video_generation",
				body:      map[string]any{"model": "MiniMax-Hailuo-2.3", "prompt": "p", "resolution": "720P"},
				wantFacts: map[string]any{"seconds": float64(6), "resolution": "1080P", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
			{
				name: "v1 first and last frames on 2.3", decoder: "createTask", wantURL: "/v1/video_generation",
				body:      map[string]any{"model": "MiniMax-Hailuo-2.3", "first_frame_image": "a.png", "last_frame_image": "b.png"},
				wantFacts: map[string]any{"seconds": float64(6), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
			{
				name: "v1 first and last frames in 512P", decoder: "createTask", wantURL: "/v1/video_generation",
				body:      map[string]any{"model": "MiniMax-Hailuo-02", "first_frame_image": "a.png", "last_frame_image": "b.png", "resolution": "512P"},
				wantFacts: map[string]any{"seconds": float64(6), "resolution": "1080P", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
			{
				name: "v1 alias", decoder: "createTask", wantURL: "/v1/video_generation", upstream: "MiniMax-Hailuo-02",
				body:      map[string]any{"model": "my-hailuo", "prompt": "p"},
				wantFacts: map[string]any{"seconds": float64(6), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				intent := call("native", []string{tc.decoder}, map[string]any{"body": map[string]any{"kind": "json", "value": tc.body}})
				upstream := tc.body["model"]
				if tc.upstream != "" {
					upstream = tc.upstream
				}
				driver := map[string]any{"requestBody": intent["requestBody"], "model": tc.body["model"], "upstreamModel": upstream, "baseUrl": "https://api.minimax.example", "apiKey": "vendor-key"}
				sent := call("buildSubmitRequest", nil, driver)
				assert.Equal(t, "https://api.minimax.example"+tc.wantURL, sent["url"])
				wantBody := decodePluginValue(t, tc.body)
				wantBody["model"] = upstream
				assert.Equal(t, wantBody, sent["body"])
				assert.Equal(t, tc.wantFacts, call("extractUsage", nil, driver))
			})
		}
	})

	// A /v1/videos client cannot pass a body off as a native one.
	t.Run("a vendorBody field is not a native body", func(t *testing.T) {
		intent := call("protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "MiniMax-Hailuo-02", "body": map[string]any{"kind": "json",
			"value": map[string]any{"model": "MiniMax-Hailuo-02", "prompt": "p", "vendorBody": map[string]any{"model": "MiniMax-Hailuo-02", "prompt": "x", "callback_url": "https://client.example/hook"}}}})
		sent := call("buildSubmitRequest", nil, map[string]any{"requestBody": intent["requestBody"], "model": "MiniMax-Hailuo-02", "upstreamModel": "MiniMax-Hailuo-02",
			"baseUrl": "https://api.minimax.example", "apiKey": "vendor-key"})
		assert.Equal(t, map[string]any{"model": "MiniMax-Hailuo-02", "prompt": "p", "duration": float64(6), "resolution": "768P"}, sent["body"])
	})

	t.Run("native replies keep each version's envelope", func(t *testing.T) {
		created := call("native", []string{"h3TaskStatus"}, map[string]any{}, map[string]any{"task_id": "task_pub", "status": "SUBMITTED", "data": map[string]any{"task_id": "task_pub"}})
		assert.Equal(t, map[string]any{"task": map[string]any{"id": "task_pub", "status": "queued"}}, created, "the first /v2 query carries no create fields")
		v2 := call("native", []string{"error"}, map[string]any{"path": "/v2/query/video_generation/task_x"}, map[string]any{"httpStatus": 429, "message": "Too many requests", "requestId": "r1"})
		assert.Equal(t, map[string]any{"type": "error", "error": map[string]any{"type": "rate_limit_error", "message": "Too many requests", "http_code": "429"}, "request_id": "r1"}, v2)
		v1 := call("native", []string{"error"}, map[string]any{"path": "/v1/video_generation"}, map[string]any{"httpStatus": 400, "message": "model is required"})
		assert.Equal(t, map[string]any{"__status": float64(200), "__body": map[string]any{"base_resp": map[string]any{"status_code": float64(2013), "status_msg": "model is required"}}}, v1)
	})
}

// MiniMax's v1 query reports the video's file_id and /v1/files/retrieve
// resolves it. Through the gateway the file id is the task id, and the
// download_url is the gateway's address for the task's video.
func TestHailuoNativeFileIDIsTheTaskAndRetrieveReturnsTheGatewayDownload(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	call := func(member string, args ...any) (map[string]any, error) {
		value, err := plugin.Engine.CallPath(t.Context(), "native", []string{member}, args...)
		if err != nil {
			return nil, err
		}
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(encoded, &decoded))
		return decoded, nil
	}
	success := map[string]any{"status_code": 0, "status_msg": "success"}
	v1 := map[string]any{"task_id": "task_pub", "status": "SUCCESS", "finished_at": 1760000000, "data": map[string]any{
		"task_id": "private-task", "status": "Success", "file_id": "private-file", "video_width": 1280, "video_height": 720, "base_resp": success,
	}}
	video := map[string]any{"key": "video", "type": "video", "url": "https://gateway.example/v1/tasks/task_pub/artifacts/video/content?access=signed"}

	status, err := call("taskStatus", map[string]any{}, v1)
	require.NoError(t, err)
	encoded, err := common.Marshal(status)
	require.NoError(t, err)
	assert.JSONEq(t, `{"task_id":"task_pub","status":"Success","file_id":"task_pub","video_width":1280,"video_height":720,"base_resp":{"status_code":0,"status_msg":"success"}}`, string(encoded))
	running, err := call("taskStatus", map[string]any{}, map[string]any{"task_id": "task_pub", "status": "IN_PROGRESS", "data": map[string]any{"task_id": "private-task", "status": "Processing", "base_resp": success}})
	require.NoError(t, err)
	assert.NotContains(t, running, "file_id")

	retrieved, err := call("fileRetrieve", map[string]any{"artifacts": map[string]any{"video": video}}, v1)
	require.NoError(t, err)
	encoded, err = common.Marshal(retrieved)
	require.NoError(t, err)
	assert.JSONEq(t, `{"file":{"file_id":"task_pub","filename":"task_pub.mp4","purpose":"video_generation","created_at":1760000000,"download_url":"https://gateway.example/v1/tasks/task_pub/artifacts/video/content?access=signed"},"base_resp":{"status_code":0,"status_msg":"success"}}`, string(encoded))
	_, err = call("fileRetrieve", map[string]any{"artifacts": map[string]any{}}, v1)
	require.ErrorContains(t, err, "artifact_not_found")
	// An H3 task has no v1 file to retrieve.
	h3 := map[string]any{"task_id": "task_h3", "status": "SUCCESS", "data": map[string]any{"task": map[string]any{"id": "private", "status": "succeeded", "content": map[string]any{"url": "https://cdn.example/h3.mp4"}}}}
	_, err = call("fileRetrieve", map[string]any{"artifacts": map[string]any{"video": video}}, h3)
	require.ErrorContains(t, err, "artifact_not_found")
	h3Status, err := call("h3TaskStatus", map[string]any{}, h3)
	require.NoError(t, err)
	assert.NotContains(t, h3Status, "file_id")
}

// The H3 family is one list of four names in three places: the declared
// models, the /v2 route, and the usage profile that prices them. The three
// new names fold to nothing else in the gateway: a case-folded duplicate
// fails the routing generation and the gateway then refuses to start.
func TestHailuoH3FamilyModels(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	family := []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"}
	assert.Subset(t, plugin.Meta.Models, family)

	var routeModels []string
	for _, route := range plugin.Meta.Routes {
		if route.Path == "/v2/video_generation" {
			routeModels = route.Models
		}
	}
	assert.Equal(t, family, routeModels)

	// The family is split across usage profiles because only MiniMax-H3
	// renders 2K; every model still belongs to exactly one profile.
	for _, model := range family {
		owners := 0
		for index := range plugin.Meta.UsageProfiles {
			if slices.Contains(plugin.Meta.UsageProfiles[index].Models, model) {
				owners++
			}
		}
		assert.Equal(t, 1, owners, "model %q must belong to exactly one usage profile", model)
	}

	generation := jsplugin.DefaultRegistry.Generation()
	require.NotNil(t, generation)
	for _, other := range generation.Plugins() {
		key, meta := other.Meta.Key, other.Meta
		if key == "hailuo" {
			continue
		}
		for _, model := range meta.Models {
			for _, own := range family {
				assert.NotEqual(t, jsplugin.ASCIIFold(own), jsplugin.ASCIIFold(model), "%s model %q folds onto the H3 family name %q", key, model, own)
			}
		}
	}
}

// Every H3 model renders its own seconds; a request outside the model's range
// is refused with the model and the range it renders.
func TestHailuoH3FamilyDurationBounds(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	for _, testCase := range []struct {
		model string
		min   int
		max   int
	}{
		{"MiniMax-H3", 4, 15},
		{"MiniMax-H3-Highspeed", 4, 15},
		{"MiniMax-H3-Lite", 4, 15},
		{"MiniMax-H3-Max", 5, 15},
	} {
		t.Run(testCase.model, func(t *testing.T) {
			descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoSubmitContext(testCase.model, map[string]any{"prompt": "p"}))
			body, ok := descriptor["body"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, float64(5), body["duration"], "the host protocols default to five seconds")

			for _, seconds := range []int{testCase.min, testCase.max} {
				descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoSubmitContext(testCase.model, map[string]any{"prompt": "p", "duration": seconds}))
				body, ok := descriptor["body"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, float64(seconds), body["duration"])
			}
			for _, seconds := range []int{testCase.min - 1, testCase.max + 1} {
				_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", hailuoSubmitContext(testCase.model, map[string]any{"prompt": "p", "duration": seconds}))
				require.ErrorContains(t, err, testCase.model+" duration must be an integer between "+strconv.Itoa(testCase.min)+" and "+strconv.Itoa(testCase.max)+" seconds")
			}
		})
	}
}

// Each H3 model renders its own resolutions; a request outside the model's set
// is refused with the model and the values it renders.
func TestHailuoH3FamilyResolutionBounds(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	for _, testCase := range []struct {
		model       string
		resolutions []string
	}{
		{"MiniMax-H3", []string{"480P", "768P", "2K"}},
		{"MiniMax-H3-Highspeed", []string{"480P", "768P"}},
		{"MiniMax-H3-Lite", []string{"480P", "768P"}},
		{"MiniMax-H3-Max", []string{"480P", "768P"}},
	} {
		t.Run(testCase.model, func(t *testing.T) {
			descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoSubmitContext(testCase.model, map[string]any{"prompt": "p"}))
			body, ok := descriptor["body"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "768P", body["resolution"], "the host protocols default to 768P")

			for _, resolution := range testCase.resolutions {
				descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoSubmitContext(testCase.model, map[string]any{"prompt": "p", "size": resolution}))
				body, ok := descriptor["body"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, resolution, body["resolution"])
			}
			for _, resolution := range []string{"480P", "768P", "2K", "1080P"} {
				if slices.Contains(testCase.resolutions, resolution) {
					continue
				}
				_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", hailuoSubmitContext(testCase.model, map[string]any{"prompt": "p", "size": resolution}))
				require.ErrorContains(t, err, testCase.model+" resolution must be "+hailuoResolutionList(testCase.resolutions))
			}
		})
	}
}

// The three H3 task types carry the finished video in the same place, so each
// of them yields the video artifact.
func TestHailuoH3FamilyTaskTypeArtifacts(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: "https://api.minimax.example"},
	})
	for _, taskType := range []string{"generation", "h3_context_ir", "regeneration"} {
		t.Run(taskType, func(t *testing.T) {
			const url = "https://cdn.example/h3.mp4"
			data, err := common.Marshal(map[string]any{"task": map[string]any{
				"id": "1", "status": "succeeded", "task_type": taskType, "content": map[string]any{"url": url},
			}})
			require.NoError(t, err)
			task := &model.Task{TaskID: "task-public", Status: model.TaskStatusSuccess, Data: data}

			artifacts, err := adaptor.ListArtifacts(task)
			require.NoError(t, err)
			assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)

			descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodGet})
			require.NoError(t, err)
			require.NotNil(t, descriptor)
			assert.Equal(t, url, descriptor.URL)
		})
	}
}

// The H3 profiles price each variant with the resolutions it renders, every
// variant belongs to exactly one profile, and every example is a complete
// vector over the schema of its own profile.
func TestHailuoH3UsageProfilesFollowResolutionLimits(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	family := []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"}
	profiled := make(map[string]int, len(family))
	for _, profile := range plugin.Meta.UsageProfiles {
		for _, model := range profile.Models {
			if slices.Contains(family, model) {
				profiled[model]++
			}
		}
	}
	for _, model := range family {
		assert.Equal(t, 1, profiled[model], model+" must belong to exactly one usage profile")
	}

	for _, model := range family {
		t.Run(model, func(t *testing.T) {
			schema, examples := plugin.Meta.UsageForModel(model)
			require.Contains(t, schema, "resolution")
			if model == "MiniMax-H3" {
				assert.Contains(t, schema["resolution"].Enum, "2K")
			} else {
				assert.NotContains(t, schema["resolution"].Enum, "2K")
			}
			require.NotEmpty(t, examples)
			for _, example := range examples {
				assert.ElementsMatch(t, slices.Collect(maps.Keys(schema)), slices.Collect(maps.Keys(example.Facts)), example.Label)
				if resolution, ok := example.Facts["resolution"].(string); ok {
					assert.Contains(t, schema["resolution"].Enum, resolution, example.Label)
				}
			}
		})
	}

	for _, profile := range plugin.Meta.UsageProfiles {
		if !slices.ContainsFunc(profile.Models, func(model string) bool { return slices.Contains(family, model) }) {
			continue
		}
		keys := slices.Collect(maps.Keys(profile.Schema))
		for _, example := range profile.Examples {
			assert.ElementsMatch(t, keys, slices.Collect(maps.Keys(example.Facts)), example.Label)
		}
	}
	assert.Contains(t, plugin.Meta.UsageSchema["resolution"].Enum, "480P")
}

// The submit context of one H3 model.
func hailuoSubmitContext(model string, requestBody map[string]any) map[string]any {
	ctx := hailuoH3SubmitContext(requestBody)
	ctx["model"] = model
	ctx["upstreamModel"] = model
	return ctx
}

// The allowed values as the plugin writes them: "480P or 768P".
func hailuoResolutionList(resolutions []string) string {
	if len(resolutions) < 2 {
		return strings.Join(resolutions, "")
	}
	return strings.Join(resolutions[:len(resolutions)-1], ", ") + " or " + resolutions[len(resolutions)-1]
}

// A member of the plugin's native object, as the host calls it.
func callHailuoNative(t *testing.T, plugin *jsplugin.LoadedPlugin, member string, args ...any) map[string]any {
	t.Helper()
	value, err := plugin.Engine.CallPath(t.Context(), "native", []string{member}, args...)
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	return decoded
}

// The long-text speech task comes in at the plugin's own route: the four
// speech models claim no host protocol, and their create, query and usage
// paths are MiniMax's own.
func TestHailuoAsyncSpeech(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	speechModels := []string{"speech-2.8-hd", "speech-2.8-turbo", "speech-2.6-hd", "speech-2.6-turbo"}
	videoModels := []string{
		"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max",
		"MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02",
		"T2V-01-Director", "T2V-01", "I2V-01-Director", "I2V-01-live", "I2V-01", "S2V-01",
	}

	t.Run("create request body mapping", func(t *testing.T) {
		decoded := callHailuoNative(t, plugin, "createSpeechTask", map[string]any{
			"body": map[string]any{
				"kind": "json",
				"value": map[string]any{
					"model":         "speech-2.8-hd",
					"text":          "a long text to speak",
					"voice_setting": map[string]any{"voice_id": "male-qn-qingse"},
				},
			},
		})
		assert.Equal(t, "submit", decoded["kind"])
		assert.Equal(t, "speech-2.8-hd", decoded["model"])
		assert.Equal(t, "text_to_speech", decoded["action"])
		requestBody, ok := decoded["requestBody"].(map[string]any)
		require.True(t, ok)

		descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", map[string]any{
			"requestBody":   requestBody,
			"model":         "speech-2.8-hd",
			"upstreamModel": "speech-2.8-hd",
			"baseUrl":       "https://api.minimax.example",
			"apiKey":        "test-ak",
		})
		assert.Equal(t, "https://api.minimax.example/v1/t2a_async_v2", descriptor["url"])
		assert.Equal(t, "POST", descriptor["method"])
		body, ok := descriptor["body"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "speech-2.8-hd", body["model"])
		assert.Equal(t, "a long text to speak", body["text"])
		assert.Equal(t, map[string]any{"voice_id": "male-qn-qingse"}, body["voice_setting"])

		// The characters of the text are reserved from the request's own text.
		facts := callHailuoHook(t, plugin, "extractUsage", map[string]any{
			"usagePurpose":  "facts",
			"requestBody":   requestBody,
			"model":         "speech-2.8-hd",
			"upstreamModel": "speech-2.8-hd",
		})
		require.NotNil(t, facts)
		require.Contains(t, facts, "characters")
		reserved, ok := facts["characters"].(float64)
		require.True(t, ok, "characters must be a number")
		assert.Equal(t, float64(len("a long text to speak")), reserved)
	})

	t.Run("create response parsing", func(t *testing.T) {
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "speech-2.8-hd"},
			map[string]any{"body": map[string]any{
				"task_id":          "task_123",
				"file_id":          987654,
				"task_token":       "token",
				"usage_characters": 2048,
				"base_resp":        map[string]any{"status_code": 0, "status_msg": "success"},
			}})
		assert.Equal(t, "task_123", parsed["taskId"])
		taskData, ok := parsed["taskData"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "task_123", taskData["task_id"])
		state, ok := parsed["state"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(2048), state["speechCharacters"])

		// The characters are reported when the task completes.
		completion := callHailuoHook(t, plugin, "extractUsageOnComplete",
			map[string]any{"model": "speech-2.8-hd", "state": state}, nil, nil)
		assert.Equal(t, map[string]any{"characters": float64(2048)}, completion)
	})

	t.Run("query status mapping", func(t *testing.T) {
		query := callHailuoHook(t, plugin, "buildQueryRequest", map[string]any{
			"taskId":        "task_123",
			"model":         "speech-2.8-hd",
			"upstreamModel": "speech-2.8-hd",
			"baseUrl":       "https://api.minimax.example",
			"apiKey":        "test-ak",
		})
		assert.Equal(t, "https://api.minimax.example/v1/query/t2a_async_query_v2?task_id=task_123", query["url"])
		assert.Equal(t, "GET", query["method"])

		statuses := []struct {
			status string
			want   string
		}{
			{"success", "SUCCESS"},
			{"failed", "FAILURE"},
			{"expired", "FAILURE"},
			{"processing", "IN_PROGRESS"},
			{"weird", "UNKNOWN"},
		}
		for _, testCase := range statuses {
			t.Run(testCase.status, func(t *testing.T) {
				result := callHailuoHook(t, plugin, "parseTaskResult", map[string]any{"model": "speech-2.8-hd"},
					map[string]any{
						"task_id":   "task_123",
						"status":    testCase.status,
						"file_id":   987654,
						"base_resp": map[string]any{"status_code": 0, "status_msg": "success"},
					})
				assert.Equal(t, testCase.want, result["status"])
			})
		}
	})

	t.Run("routes and native members", func(t *testing.T) {
		source, err := builtinplugins.Source("hailuo")
		require.NoError(t, err)
		registry := jsplugin.NewRegistry()
		_, err = registry.RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
		require.NoError(t, err)

		// The speech models claim no host protocol.
		for _, model := range speechModels {
			_, found := registry.Generation().LookupEndpoint(http.MethodPost, "/v1/responses", model)
			assert.False(t, found, model)
			_, found = registry.Generation().LookupEndpoint(http.MethodPost, "/v1/videos", model)
			assert.False(t, found, model)
		}
		// The video models keep the host protocols.
		for _, model := range videoModels {
			binding, found := registry.Generation().LookupEndpoint(http.MethodPost, "/v1/responses", model)
			require.True(t, found, model)
			assert.Equal(t, "openai_responses", binding.Protocol)
			binding, found = registry.Generation().LookupEndpoint(http.MethodPost, "/v1/videos", model)
			require.True(t, found, model)
			assert.Equal(t, "openai_video", binding.Protocol)
		}

		// The speech task comes in at the plugin's own route, whose decode and
		// render are two different members.
		bindings := registry.Generation().LookupDeclaredRoutes(http.MethodPost, "/hailuo/v1/audio/long_text")
		require.Len(t, bindings, 1)
		assert.ElementsMatch(t, speechModels, bindings[0].Route.Models)
		assert.Equal(t, "createSpeechTask", bindings[0].Route.Decode)
		assert.Equal(t, "speechTaskCreated", bindings[0].Route.Render)
		assert.NotEqual(t, bindings[0].Route.Decode, bindings[0].Route.Render)

		query := registry.Generation().LookupDeclaredRoutes(http.MethodGet, "/hailuo/v1/audio/long_text/:task_id")
		require.Len(t, query, 1)
		assert.Equal(t, "speechTaskStatus", query[0].Route.Render)
		assert.Empty(t, query[0].Route.Decode)
	})
}

// The speech task reserves the characters of the text it carries, so a
// billing expression of the model never meets a missing fact at submission.
func TestHailuoAsyncSpeechSubmitCharacters(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name string
		body map[string]any
		want float64
	}{
		{
			name: "text path",
			body: map[string]any{
				"model":         "speech-2.8-hd",
				"text":          "a long text to speak",
				"voice_setting": map[string]any{"voice_id": "male-qn-qingse"},
			},
			want: float64(len("a long text to speak")),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			intent := callHailuoNative(t, plugin, "createSpeechTask", map[string]any{
				"body": map[string]any{"kind": "json", "value": testCase.body},
			})
			requestBody, ok := intent["requestBody"].(map[string]any)
			require.True(t, ok)

			facts := callHailuoHook(t, plugin, "extractUsage", map[string]any{
				"usagePurpose":  "facts",
				"requestBody":   requestBody,
				"model":         "speech-2.8-hd",
				"upstreamModel": "speech-2.8-hd",
			})
			require.NotNil(t, facts)
			require.Contains(t, facts, "characters")
			characters, ok := facts["characters"].(float64)
			require.True(t, ok, "characters must be a number")
			assert.Equal(t, testCase.want, characters)
		})
	}
}

// The upstream bills the characters of the text it speaks as code points, so
// an astral character reserves one character and not two UTF-16 units.
func TestHailuoSpeechSubmitCountsCodePoints(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	text := "a long text to speak 😀"
	intent := callHailuoNative(t, plugin, "createSpeechTask", map[string]any{
		"body": map[string]any{"kind": "json", "value": map[string]any{
			"model":         "speech-2.8-hd",
			"text":          text,
			"voice_setting": map[string]any{"voice_id": "male-qn-qingse"},
		}},
	})
	requestBody, ok := intent["requestBody"].(map[string]any)
	require.True(t, ok)

	facts := callHailuoHook(t, plugin, "extractUsage", map[string]any{
		"usagePurpose":  "facts",
		"requestBody":   requestBody,
		"model":         "speech-2.8-hd",
		"upstreamModel": "speech-2.8-hd",
	})
	require.Contains(t, facts, "characters")
	characters, ok := facts["characters"].(float64)
	require.True(t, ok, "characters must be a number")
	assert.Equal(t, float64(len([]rune(text))), characters)
	assert.Equal(t, float64(len(utf16.Encode([]rune(text)))-1), characters, "an astral character is one character, not two UTF-16 units")
}

// A speech task that speaks a file has no text to measure, so the reservation
// reports no character count at all; the completion hook reports the count the
// upstream bills.
func TestHailuoSpeechFileRequestReservesNoCharacters(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	intent := callHailuoNative(t, plugin, "createSpeechTask", map[string]any{
		"body": map[string]any{"kind": "json", "value": map[string]any{
			"model":         "speech-2.8-hd",
			"text_file_id":  "file_123",
			"voice_setting": map[string]any{"voice_id": "male-qn-qingse"},
		}},
	})
	requestBody, ok := intent["requestBody"].(map[string]any)
	require.True(t, ok)

	facts := callHailuoHook(t, plugin, "extractUsage", map[string]any{
		"usagePurpose":  "facts",
		"requestBody":   requestBody,
		"model":         "speech-2.8-hd",
		"upstreamModel": "speech-2.8-hd",
	})
	require.NotNil(t, facts)
	assert.NotContains(t, facts, "characters")
}

// The content array is the only shape that carries a reference image or
// exceeds the frame limits: the assembled array yields first_frame and
// last_frame items alone.
func TestHailuoH3ContentRegression(t *testing.T) {
	plugin := loadHailuoPlugin(t)

	t.Run("content multimodal boundary", func(t *testing.T) {
		accepted := []struct {
			name       string
			content    []any
			wantAction string
			wantBody   string
		}{
			{
				name:       "a text item and one image keep the caller's content",
				content:    []any{map[string]any{"type": "text", "text": "p"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "first.png"}}},
				wantAction: "image_to_video",
				wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"},{"type":"image_url","image_url":{"url":"first.png"}}],"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			},
			{
				name:       "an image without a role and a last frame are one first and one last frame",
				content:    []any{map[string]any{"type": "text", "text": "p"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "first.png"}}, map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "last.png"}}},
				wantAction: "image_to_video",
				wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"},{"type":"image_url","image_url":{"url":"first.png"}},{"type":"image_url","role":"last_frame","image_url":{"url":"last.png"}}],"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			},
		}
		for _, testCase := range accepted {
			descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", hailuoH3SubmitContext(map[string]any{"metadata": map[string]any{"content": testCase.content}}))
			assert.Equal(t, testCase.wantAction, descriptor["action"], testCase.name)
			body, err := common.Marshal(descriptor["body"])
			require.NoError(t, err, testCase.name)
			assert.JSONEq(t, testCase.wantBody, string(body), testCase.name)
		}

		rejected := []struct {
			name    string
			content []any
			wantErr string
		}{
			{
				name:    "two images without a role are two first frames",
				content: []any{map[string]any{"type": "text", "text": "p"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "first.png"}}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "second.png"}}},
				wantErr: "accepts at most one first_frame image",
			},
			{
				name:    "two last frames exceed the one last frame limit",
				content: []any{map[string]any{"type": "text", "text": "p"}, map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "first.png"}}, map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "second.png"}}},
				wantErr: "accepts at most one last_frame image",
			},
			{
				name:    "an image without a role is a frame and cannot be mixed with a reference image",
				content: []any{map[string]any{"type": "text", "text": "p"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "first.png"}}, map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "reference.png"}}},
				wantErr: "cannot mix frame images with reference media",
			},
			{
				name:    "content without a text item and without a prompt is rejected",
				content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "first.png"}}},
				wantErr: "metadata.content requires a text item or a prompt",
			},
		}
		for _, testCase := range rejected {
			_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", hailuoH3SubmitContext(map[string]any{"metadata": map[string]any{"content": testCase.content}}))
			require.ErrorContains(t, err, testCase.wantErr, testCase.name)
		}
	})
}

func TestHailuoAsyncSpeechArtifacts(t *testing.T) {
	service.InitHttpClient()
	plugin := loadHailuoPlugin(t)

	const signedURL = "https://bucket.oss-cn-wulanchabu.aliyuncs.com/video.mp4?Expires=1&OSSAccessKeyId=key&Signature=sig"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-ak", r.Header.Get("Authorization"))
		if r.URL.Path != "/v1/files/retrieve" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"file":{"file_id":1,"download_url":"` + signedURL + `"},"base_resp":{"status_code":0,"status_msg":"success"}}`))
	}))
	defer upstream.Close()

	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: upstream.URL}})

	speechData, err := common.Marshal(map[string]any{
		"task_id": "task_123", "status": "success", "file_id": 987654,
		"base_resp": map[string]any{"status_code": 0, "status_msg": "success"},
	})
	require.NoError(t, err)
	speechTask := &model.Task{TaskID: "task-public", Status: model.TaskStatusSuccess, Action: "text_to_speech", Data: speechData}
	videoData, err := common.Marshal(map[string]any{"file_id": 1})
	require.NoError(t, err)
	videoTask := &model.Task{TaskID: "task-public", Status: model.TaskStatusSuccess, Action: "text_to_video", Data: videoData}

	t.Run("artifact proxy", func(t *testing.T) {
		artifacts, err := adaptor.ListArtifacts(speechTask)
		require.NoError(t, err)
		assert.Equal(t, []channel.TaskArtifact{{Key: "audio", Type: "audio"}}, artifacts)

		descriptor, err := adaptor.BuildContentRequest(t.Context(), speechTask, "audio", channel.TaskArtifactClientRequest{Method: http.MethodGet})
		require.NoError(t, err)
		assert.Equal(t, &channel.TaskContentRequest{
			URL:     upstream.URL + "/v1/files/retrieve_content?file_id=987654",
			Method:  http.MethodGet,
			Headers: map[string]string{"Authorization": "Bearer test-ak"},
		}, descriptor)

		videoArtifacts, err := adaptor.ListArtifacts(videoTask)
		require.NoError(t, err)
		assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, videoArtifacts)

		videoDescriptor, err := adaptor.BuildContentRequest(t.Context(), videoTask, "video", channel.TaskArtifactClientRequest{Method: http.MethodGet})
		require.NoError(t, err)
		assert.Equal(t, &channel.TaskContentRequest{URL: signedURL, Method: http.MethodGet, Credentialless: true}, videoDescriptor)

		gateway := taskplugin.New(plugin)
		gateway.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeNewAPI, ApiKey: "sk-gateway", ChannelBaseUrl: "https://gateway.example",
		}})
		gatewayTask := &model.Task{
			TaskID: "task-public", Status: model.TaskStatusSuccess, Action: "text_to_speech", Data: speechData,
			PrivateData: model.TaskPrivateData{UpstreamTaskID: "task_pub"},
		}
		gatewayDescriptor, err := gateway.BuildContentRequest(t.Context(), gatewayTask, "audio", channel.TaskArtifactClientRequest{Method: http.MethodGet})
		require.NoError(t, err)
		assert.Equal(t, &channel.TaskContentRequest{
			URL:     "https://gateway.example/v1/tasks/task_pub/artifacts/audio/content",
			Method:  http.MethodGet,
			Headers: map[string]string{"Authorization": "Bearer sk-gateway"},
		}, gatewayDescriptor)
	})

	t.Run("character fact", func(t *testing.T) {
		schema, _ := plugin.Meta.UsageForModel("speech-2.8-hd")
		require.Contains(t, schema, "characters")
		assert.Equal(t, "token", schema["characters"].Unit)

		facts := callHailuoHook(t, plugin, "extractUsageOnComplete",
			map[string]any{"model": "speech-2.8-hd", "data": map[string]any{"task_id": "task_123", "usage_characters": 2048}},
			nil, nil)
		assert.Equal(t, map[string]any{"characters": float64(2048)}, facts)

		missing := callHailuoHook(t, plugin, "extractUsageOnComplete",
			map[string]any{"model": "speech-2.8-hd", "data": map[string]any{"task_id": "task_123"}}, nil, nil)
		assert.Nil(t, missing)
	})
}

// The settlement hands the plugin the task and the result alone: the
// characters of a speech task travel with the result the host carries, so the
// billed count survives the way from the poll to the settlement.
func TestHailuoSpeechSettlementCharacters(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	task := map[string]any{"task_id": "task_123", "action": "text_to_speech"}

	t.Run("the plugin state of the poll carries the characters", func(t *testing.T) {
		facts := callHailuoHook(t, plugin, "extractUsageOnComplete", task, map[string]any{
			"task_id": "task_123", "status": "SUCCESS",
			"plugin_state": map[string]any{"speechCharacters": 42},
		})
		assert.Equal(t, map[string]any{"characters": float64(42)}, facts)
	})

	t.Run("the settled facts carry the characters", func(t *testing.T) {
		facts := callHailuoHook(t, plugin, "extractUsageOnComplete", task, map[string]any{
			"task_id": "task_123", "status": "SUCCESS",
			"usage_facts": map[string]any{"characters": 42},
		})
		assert.Equal(t, map[string]any{"characters": float64(42)}, facts)
	})

	t.Run("the settled facts may keep the count under its own name", func(t *testing.T) {
		facts := callHailuoHook(t, plugin, "extractUsageOnComplete", task, map[string]any{
			"task_id": "task_123", "status": "SUCCESS",
			"usage_facts": map[string]any{"speechCharacters": 42},
		})
		assert.Equal(t, map[string]any{"characters": float64(42)}, facts)
	})

	t.Run("a task without any characters reports no fact", func(t *testing.T) {
		facts := callHailuoHook(t, plugin, "extractUsageOnComplete", task, map[string]any{
			"task_id": "task_123", "status": "SUCCESS",
		})
		assert.Nil(t, facts)
	})
}

// A speech poll does not name the characters, so the count of the create reply
// travels with the task until the settlement reads it.
func TestHailuoSpeechPollCarriesCharacters(t *testing.T) {
	plugin := loadHailuoPlugin(t)

	t.Run("the poll reply names the characters", func(t *testing.T) {
		result := callHailuoHook(t, plugin, "parseTaskResult",
			map[string]any{"model": "speech-2.8-hd", "action": "text_to_speech"},
			map[string]any{"task_id": "task_123", "status": "success", "usage_characters": 2048,
				"base_resp": map[string]any{"status_code": 0}})
		assert.Equal(t, "SUCCESS", result["status"])
		state, ok := result["state"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(2048), state["speechCharacters"])
	})

	t.Run("the count of the create reply is kept when the poll names none", func(t *testing.T) {
		result := callHailuoHook(t, plugin, "parseTaskResult",
			map[string]any{"model": "speech-2.8-hd", "action": "text_to_speech",
				"state": map[string]any{"speechCharacters": 2048}},
			map[string]any{"task_id": "task_123", "status": "processing",
				"base_resp": map[string]any{"status_code": 0}})
		assert.Equal(t, "IN_PROGRESS", result["status"])
		state, ok := result["state"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(2048), state["speechCharacters"])
	})

	t.Run("a poll without any characters carries none", func(t *testing.T) {
		result := callHailuoHook(t, plugin, "parseTaskResult",
			map[string]any{"model": "speech-2.8-hd", "action": "text_to_speech"},
			map[string]any{"task_id": "task_123", "status": "success",
				"base_resp": map[string]any{"status_code": 0}})
		assert.Equal(t, "SUCCESS", result["status"])
		assert.NotContains(t, result, "state")
	})
}

// A channel that renames the model must not turn the speech polls into video
// polls: the action of the task tells the two apart.
func TestHailuoSpeechTaskIsToldApartByItsAction(t *testing.T) {
	plugin := loadHailuoPlugin(t)

	t.Run("a renamed speech model still polls as a speech task", func(t *testing.T) {
		result := callHailuoHook(t, plugin, "parseTaskResult",
			map[string]any{"model": "voice-alias", "upstreamModel": "voice-alias", "action": "text_to_speech"},
			map[string]any{"task_id": "task_123", "status": "success",
				"base_resp": map[string]any{"status_code": 0}})
		assert.Equal(t, "SUCCESS", result["status"])
	})

	t.Run("a renamed speech model still settles its characters", func(t *testing.T) {
		facts := callHailuoHook(t, plugin, "extractUsageOnComplete",
			map[string]any{"action": "text_to_speech", "model": "voice-alias"},
			map[string]any{"plugin_state": map[string]any{"speechCharacters": 42}})
		assert.Equal(t, map[string]any{"characters": float64(42)}, facts)
	})

	t.Run("a video task keeps its own facts", func(t *testing.T) {
		facts := callHailuoHook(t, plugin, "extractUsageOnComplete",
			map[string]any{"action": "text_to_video", "model": "MiniMax-Hailuo-2.3"},
			map[string]any{"plugin_state": map[string]any{"speechCharacters": 42}},
			map[string]any{"task_id": "1", "status": "Success", "video_width": 1920, "video_height": 1080,
				"base_resp": map[string]any{"status_code": 0}})
		assert.NotContains(t, facts, "characters")
	})
}

// The speech create reply reports a numeric task id, but the host takes the
// task id as a string.
func TestHailuoAsyncSpeechNumericTaskID(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "speech-2.8-hd"},
		map[string]any{"body": map[string]any{"task_id": int64(95157322514444)}})
	taskID, ok := parsed["taskId"].(string)
	require.True(t, ok, "the task id must be a string")
	assert.Equal(t, "95157322514444", taskID)
}

// MiniMax may report a speech status in any case; the table is read with the
// status normalized.
func TestHailuoAsyncSpeechStatusCaseInsensitive(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	result := callHailuoHook(t, plugin, "parseTaskResult", map[string]any{"model": "speech-2.8-hd"},
		map[string]any{"task_id": "task_123", "status": "Processing", "base_resp": map[string]any{"status_code": 0}})
	assert.Equal(t, "IN_PROGRESS", result["status"])
}

// A case variant of a speech model is accepted, and MiniMax receives the
// spelling the plugin declares.
func TestHailuoAsyncSpeechModelCaseInsensitive(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	intent := callHailuoNative(t, plugin, "createSpeechTask", map[string]any{
		"body": map[string]any{"kind": "json", "value": map[string]any{
			"model":         "Speech-2.8-HD",
			"text":          "a long text to speak",
			"voice_setting": map[string]any{"voice_id": "male-qn-qingse"},
		}},
	})
	assert.Equal(t, "speech-2.8-hd", intent["model"])
	requestBody, ok := intent["requestBody"].(map[string]any)
	require.True(t, ok)

	descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", map[string]any{
		"requestBody":   requestBody,
		"model":         "Speech-2.8-HD",
		"upstreamModel": "Speech-2.8-HD",
		"baseUrl":       "https://api.minimax.example",
		"apiKey":        "test-ak",
	})
	assert.Equal(t, "https://api.minimax.example/v1/t2a_async_v2", descriptor["url"])
	body, ok := descriptor["body"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "speech-2.8-hd", body["model"])
}
