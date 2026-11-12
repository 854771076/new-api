package plugins_test

import (
	"regexp"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newApimartPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("apimart")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "apimart"})
	require.NoError(t, err)
	return plugin
}

// apimartCall runs one plugin hook and hands back its raw result, so a test
// can assert on the failure of a hook as well.
func apimartCall(t *testing.T, plugin *jsplugin.LoadedPlugin, export string, members []string, args ...any) (any, error) {
	t.Helper()
	var value any
	var err error
	if len(members) == 0 {
		value, err = plugin.Engine.Call(t.Context(), export, args...)
	} else {
		value, err = plugin.Engine.CallPath(t.Context(), export, members, args...)
	}
	if err != nil {
		return nil, err
	}
	return value, nil
}

// apimartVideoBody is the client body of one APIMart video task, with every
// documented field filled in.
func apimartVideoBody() map[string]any {
	return map[string]any{
		"model":          "seedance-1.0-pro",
		"prompt":         "a cat walks on the beach",
		"negative_prompt": "blurry",
		"mode":           "pro",
		"duration":       8,
		"aspect_ratio":   "16:9",
		"image_urls":     []any{"https://cdn.example/a.png", "https://cdn.example/b.png"},
		"watermark":      false,
		"audio":          true,
		"multi_shot":     true,
		"shot_type":      "customize",
		"multi_prompt": []any{
			map[string]any{"index": 1, "prompt": "first shot", "duration": 4},
			map[string]any{"index": 2, "prompt": "second shot", "duration": 4},
		},
		"element_list": []any{
			map[string]any{"name": "cat", "description": "a striped cat", "element_input_urls": []any{"https://cdn.example/c.png", "https://cdn.example/d.png"}},
		},
	}
}

// The Responses protocol serves every APIMart model, bills one duration fact
// and echoes the alias the client sent.
func TestApimartResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "apimart",
		model:     "seedance-1.0-pro",
		requestBody: map[string]any{
			"model": "seedance-1.0-pro",
			"input": "a cat walking on the beach",
		},
		wantAction: "text_to_video",
		wantRequest: map[string]any{
			"model":    "seedance-1.0-pro",
			"prompt":   "a cat walking on the beach",
			"duration": float64(5),
		},
		wantUsageKeys:       []string{"seconds"},
		wantSubmitUsageKeys: []string{"seconds"},
		wantVendorName:      "apimart",
	})

	t.Run("decodes the alias the client sent", func(t *testing.T) {
		plugin := newApimartPlugin(t)
		value, err := apimartCall(t, plugin, "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{
			"model": "my-apimart-alias", "upstreamModel": "seedance-1.0-pro",
			"body": map[string]any{"kind": "json", "value": map[string]any{"model": "my-apimart-alias", "input": "a cat"}},
		})
		require.NoError(t, err)
		decoded := decodePluginValue(t, value)
		assert.Equal(t, "submit", decoded["kind"])
		assert.Equal(t, "my-apimart-alias", decoded["model"])
	})
}

// The submit request carries the documented fields upstream and refuses a
// request the upstream would reject.
func TestApimartSubmitRequestValidation(t *testing.T) {
	plugin := newApimartPlugin(t)
	const base = "https://api.apimart.ai"

	t.Run("passes the documented fields upstream", func(t *testing.T) {
		intent, err := apimartCall(t, plugin, "native", []string{"createGeneration"}, map[string]any{
			"body": map[string]any{"kind": "json", "value": apimartVideoBody()},
		})
		require.NoError(t, err)
		decoded := decodePluginValue(t, intent)
		assert.Equal(t, "image_to_video", decoded["action"])

		descriptor, err := apimartCall(t, plugin, "buildSubmitRequest", nil, map[string]any{
			"baseUrl": base, "apiKey": "key", "model": "my-alias", "upstreamModel": "seedance-1.0-pro",
			"requestBody": decoded["requestBody"],
		})
		require.NoError(t, err)
		sent := decodePluginValue(t, descriptor)
		assert.Equal(t, base+"/v1/videos/generations", sent["url"])
		assert.Equal(t, "POST", sent["method"])
		assert.Equal(t, "image_to_video", sent["action"])
		headers := decodePluginValue(t, sent["headers"])
		assert.Equal(t, "Bearer key", headers["Authorization"])

		upstream := decodePluginValue(t, sent["body"])
		assert.Equal(t, "seedance-1.0-pro", upstream["model"])
		assert.Equal(t, "a cat walks on the beach", upstream["prompt"])
		assert.Equal(t, float64(8), upstream["duration"])
		assert.Equal(t, "blurry", upstream["negative_prompt"])
		assert.Equal(t, "pro", upstream["mode"])
		assert.Equal(t, "16:9", upstream["aspect_ratio"])
		assert.Equal(t, []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}, upstream["image_urls"])
		assert.Equal(t, false, upstream["watermark"])
		assert.Equal(t, true, upstream["audio"])
		assert.Equal(t, true, upstream["multi_shot"])
		assert.Equal(t, "customize", upstream["shot_type"])
		assert.Equal(t, []any{
			map[string]any{"index": float64(1), "prompt": "first shot", "duration": float64(4)},
			map[string]any{"index": float64(2), "prompt": "second shot", "duration": float64(4)},
		}, upstream["multi_prompt"])
		assert.Equal(t, []any{
			map[string]any{"name": "cat", "description": "a striped cat", "element_input_urls": []any{"https://cdn.example/c.png", "https://cdn.example/d.png"}},
		}, upstream["element_list"])
	})

	for _, tc := range []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{"duration below the range", func(body map[string]any) { body["duration"] = 2 }, "duration must be an integer between 3 and 15"},
		{"duration above the range", func(body map[string]any) { body["duration"] = 16 }, "duration must be an integer between 3 and 15"},
		{"negative duration", func(body map[string]any) { body["duration"] = -1 }, "duration must be an integer between 3 and 15"},
		{"fractional duration", func(body map[string]any) { body["duration"] = 2.5 }, "duration must be an integer between 3 and 15"},
		{"duration that is not a number", func(body map[string]any) { body["duration"] = "soon" }, "duration must be an integer between 3 and 15"},
		{"shots that do not start at one", func(body map[string]any) {
			body["multi_prompt"] = []any{map[string]any{"index": 2, "prompt": "second shot", "duration": 8}}
		}, "multi_prompt index must start at 1 and stay consecutive"},
		{"shots that skip an index", func(body map[string]any) {
			body["multi_prompt"] = []any{
				map[string]any{"index": 1, "prompt": "first shot", "duration": 4},
				map[string]any{"index": 3, "prompt": "third shot", "duration": 4},
			}
		}, "multi_prompt index must start at 1 and stay consecutive"},
		{"shots that do not add up", func(body map[string]any) {
			body["multi_prompt"] = []any{
				map[string]any{"index": 1, "prompt": "first shot", "duration": 3},
				map[string]any{"index": 2, "prompt": "second shot", "duration": 3},
			}
		}, "multi_prompt durations must add up to the duration of the task"},
		{"more than two reference images", func(body map[string]any) {
			body["image_urls"] = []any{"https://cdn.example/a.png", "https://cdn.example/b.png", "https://cdn.example/c.png"}
		}, "image_urls must not hold more than 2 images"},
		{"more than three subjects", func(body map[string]any) {
			body["element_list"] = []any{
				map[string]any{"name": "cat", "element_input_urls": []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}},
				map[string]any{"name": "dog", "element_input_urls": []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}},
				map[string]any{"name": "bird", "element_input_urls": []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}},
				map[string]any{"name": "fish", "element_input_urls": []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}},
			}
		}, "element_list must not hold more than 3 subjects"},
		{"a subject with one image", func(body map[string]any) {
			body["element_list"] = []any{
				map[string]any{"name": "cat", "element_input_urls": []any{"https://cdn.example/a.png"}},
			}
		}, "element_list[0].element_input_urls must hold 2 to 4 images"},
		{"an unknown quality", func(body map[string]any) { body["mode"] = "ultra" }, "mode must be one of std, pro, 4k"},
		{"an unknown frame shape", func(body map[string]any) { body["aspect_ratio"] = "4:3" }, "aspect_ratio must be one of 16:9, 9:16, 1:1"},
		{"no prompt", func(body map[string]any) { body["prompt"] = " " }, "prompt is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := apimartVideoBody()
			tc.mutate(body)
			_, err := apimartCall(t, plugin, "buildSubmitRequest", nil, map[string]any{
				"baseUrl": base, "apiKey": "key", "model": "seedance-1.0-pro", "requestBody": body,
			})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// The submit reply carries the task id in its first data entry, and an API
// error is reported with the upstream message.
func TestApimartSubmitResponseParsing(t *testing.T) {
	plugin := newApimartPlugin(t)
	ctx := map[string]any{
		"model": "seedance-1.0-pro", "upstreamModel": "seedance-1.0-pro",
		"requestBody": map[string]any{"prompt": "a cat", "duration": 8, "model": "seedance-1.0-pro"},
	}

	t.Run("takes the task id from the first data entry", func(t *testing.T) {
		value, err := apimartCall(t, plugin, "parseSubmitResponse", nil, ctx, map[string]any{
			"statusCode": 200, "headers": map[string]any{},
			"body": map[string]any{"data": []any{map[string]any{"task_id": "task_123"}}},
		})
		require.NoError(t, err)
		parsed := decodePluginValue(t, value)
		assert.Equal(t, "task_123", parsed["taskId"])
		assert.Equal(t, map[string]any{"data": []any{map[string]any{"task_id": "task_123"}}}, parsed["taskData"])
		assert.Equal(t, float64(8), decodePluginValue(t, parsed["state"])["seconds"])
	})

	t.Run("reports the api error", func(t *testing.T) {
		_, err := apimartCall(t, plugin, "parseSubmitResponse", nil, ctx, map[string]any{
			"statusCode": 400, "headers": map[string]any{},
			"body": map[string]any{"error": map[string]any{"code": "invalid_request", "message": "prompt is required"}},
		})
		require.ErrorContains(t, err, "prompt is required")
	})

	t.Run("rejects a reply without a task id", func(t *testing.T) {
		_, err := apimartCall(t, plugin, "parseSubmitResponse", nil, ctx, map[string]any{
			"statusCode": 200, "headers": map[string]any{}, "body": map[string]any{"data": []any{}},
		})
		require.ErrorContains(t, err, "APIMart did not return a task id")
	})
}

// The poll reply decides the task state; the stale submit snapshot in the
// query context never does.
func TestApimartQueryStatusMapping(t *testing.T) {
	plugin := newApimartPlugin(t)

	for _, tc := range []struct {
		status       string
		wantStatus   string
		wantProgress string
		wantReason   string
	}{
		{"pending", "QUEUED", "0%", ""},
		{"processing", "IN_PROGRESS", "50%", ""},
		{"completed", "SUCCESS", "100%", ""},
		{"failed", "FAILURE", "100%", "video generation failed"},
		{"cancelled", "FAILURE", "100%", "video generation failed"},
		{"paused", "UNKNOWN", "0%", "unrecognized status: paused"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			value, err := apimartCall(t, plugin, "parseTaskResult", nil,
				map[string]any{"taskId": "task_123", "data": map[string]any{"data": map[string]any{"status": "completed"}}},
				map[string]any{"code": 200, "data": map[string]any{"status": tc.status}},
				map[string]any{"status": 200})
			require.NoError(t, err)
			parsed := decodePluginValue(t, value)
			assert.Equal(t, tc.wantStatus, parsed["status"])
			assert.Equal(t, tc.wantProgress, parsed["progress"])
			assert.Equal(t, tc.wantReason, parsed["reason"])
			assert.Equal(t, float64(200), parsed["code"])
		})
	}

	t.Run("reports the task error", func(t *testing.T) {
		_, err := apimartCall(t, plugin, "parseTaskResult", nil, map[string]any{},
			map[string]any{"data": map[string]any{"status": "failed", "error": map[string]any{"code": "content_policy", "message": "the prompt was rejected"}}},
			map[string]any{"status": 200})
		require.ErrorContains(t, err, "the prompt was rejected")
	})
}

// The finished videos are proxied by the gateway under its own artifact
// addresses.
func TestApimartArtifactProxy(t *testing.T) {
	plugin := newApimartPlugin(t)
	pollBody := map[string]any{
		"code": 200,
		"data": map[string]any{
			"status": "completed",
			"result": map[string]any{
				"videos": []any{
					map[string]any{"url": []any{"https://cdn.example/a.mp4"}},
					map[string]any{"url": []any{"https://cdn.example/b.mp4", "https://cdn.example/b-mirror.mp4"}},
				},
			},
		},
	}

	value, err := apimartCall(t, plugin, "listArtifacts", nil, map[string]any{
		"taskId": "task_123", "status": "SUCCESS", "action": "text_to_video", "data": pollBody, "state": nil,
	})
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var artifacts []any
	require.NoError(t, common.Unmarshal(encoded, &artifacts))
	require.Len(t, artifacts, 2)

	pattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)
	keys := make([]string, 0, len(artifacts))
	seen := map[string]bool{}
	for _, item := range artifacts {
		artifact := decodePluginValue(t, item)
		key, ok := artifact["key"].(string)
		require.True(t, ok)
		assert.Regexp(t, pattern, key)
		assert.False(t, seen[key], "artifact keys must be unique")
		seen[key] = true
		keys = append(keys, key)
		assert.Equal(t, "video", artifact["type"])
	}
	assert.Equal(t, []string{"video-0", "video-1"}, keys)

	content, err := apimartCall(t, plugin, "buildContentRequest", nil, map[string]any{
		"artifactKey": "video-1", "data": pollBody, "clientRequest": map[string]any{"method": "GET"},
	})
	require.NoError(t, err)
	descriptor := decodePluginValue(t, content)
	assert.Equal(t, "https://cdn.example/b.mp4", descriptor["url"])
	assert.Equal(t, "GET", descriptor["method"])
	assert.Equal(t, true, descriptor["credentialless"])
}

// The billed duration is the one the gateway sent upstream; the upstream's own
// duration is never read.
func TestApimartUsageSeconds(t *testing.T) {
	plugin := newApimartPlugin(t)

	t.Run("bills the duration the gateway sent", func(t *testing.T) {
		value, err := apimartCall(t, plugin, "extractUsage", nil, map[string]any{
			"model": "seedance-1.0-pro", "upstreamModel": "seedance-1.0-pro", "action": "text_to_video",
			"requestBody": map[string]any{"model": "seedance-1.0-pro", "prompt": "a cat", "duration": 12, "actual_time": 99},
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"seconds": float64(12)}, decodePluginValue(t, value))
	})

	t.Run("falls back to the default duration", func(t *testing.T) {
		value, err := apimartCall(t, plugin, "extractUsage", nil, map[string]any{
			"model": "seedance-1.0-pro", "upstreamModel": "seedance-1.0-pro",
			"requestBody": map[string]any{"model": "seedance-1.0-pro", "prompt": "a cat"},
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"seconds": float64(5)}, decodePluginValue(t, value))
	})

	t.Run("takes the seconds of the round that finished", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			first     map[string]any
			second    map[string]any
			wantFacts map[string]any
		}{
			{"the plugin state of the result wins", map[string]any{"state": map[string]any{"seconds": 5}}, map[string]any{"plugin_state": map[string]any{"seconds": 9}}, map[string]any{"seconds": float64(9)}},
			{"the persisted state of the task", map[string]any{"state": map[string]any{"seconds": 5}}, map[string]any{}, map[string]any{"seconds": float64(5)}},
			{"the state of the task row", map[string]any{"plugin_state": map[string]any{"seconds": 7}}, map[string]any{}, map[string]any{"seconds": float64(7)}},
			{"no state at all", map[string]any{}, map[string]any{}, map[string]any{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
					value, err := apimartCall(t, plugin, "extractUsageOnComplete", nil, tc.first, tc.second,
						map[string]any{"data": map[string]any{"actual_time": 42}})
					require.NoError(t, err)
					facts := decodePluginValue(t, value)
					if len(tc.wantFacts) == 0 {
						assert.Empty(t, facts)
						return
					}
					assert.Equal(t, tc.wantFacts, facts)
			})
		}
	})
}

// A New API gateway that hosts this plugin serves it under its own prefix.
func TestApimartNewAPIUpstreamPrefix(t *testing.T) {
	plugin := newApimartPlugin(t)
	const base = "https://api.apimart.ai"
	assert.True(t, plugin.Meta.SupportsUpstream(jsplugin.UpstreamKindNewAPI))

	for _, tc := range []struct {
		name       string
		upstream   map[string]any
		wantSubmit string
		wantQuery  string
	}{
		{"vendor", map[string]any{"kind": "vendor"}, base + "/v1/videos/generations", base + "/v1/tasks/task_123"},
		{"new api gateway", map[string]any{"kind": "new_api"}, base + "/apimart/v1/videos/generations", base + "/apimart/v1/tasks/task_123"},
		{"no upstream signal", nil, base + "/v1/videos/generations", base + "/v1/tasks/task_123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			submitCtx := map[string]any{
				"baseUrl": base, "apiKey": "key", "model": "seedance-1.0-pro",
				"requestBody": map[string]any{"prompt": "a cat", "duration": 5},
			}
			queryCtx := map[string]any{"baseUrl": base, "apiKey": "key", "taskId": "task_123"}
			if tc.upstream != nil {
				submitCtx["upstream"] = tc.upstream
				queryCtx["upstream"] = tc.upstream
			}

			submit, err := apimartCall(t, plugin, "buildSubmitRequest", nil, submitCtx)
			require.NoError(t, err)
			assert.Equal(t, tc.wantSubmit, decodePluginValue(t, submit)["url"])

			query, err := apimartCall(t, plugin, "buildQueryRequest", nil, queryCtx)
			require.NoError(t, err)
			assert.Equal(t, tc.wantQuery, decodePluginValue(t, query)["url"])
		})
	}
}
