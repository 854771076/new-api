package plugins

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunningHubRequestsUseChannelBaseURL(t *testing.T) {
	source, err := Source("runninghub")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "runninghub"})
	require.NoError(t, err)

	for _, baseURL := range []string{
		"https://www.runninghub.cn",
		"https://www.runninghub.ai/",
		"https://proxy.example:8443/runninghub///",
	} {
		for _, model := range plugin.Meta.Models {
			t.Run(baseURL+"/"+model, func(t *testing.T) {
				base := strings.TrimRight(baseURL, "/")
				uploads := 0
				ctx := jsplugin.WithUploader(t.Context(), func(_ context.Context, request jsplugin.UploadRequest) (jsplugin.UploadResponse, error) {
					uploads++
					assert.Equal(t, base+"/openapi/v2/media/upload/binary", request.URL)
					assert.NoError(t, jsplugin.ValidateRequestURL(request.URL, baseURL, plugin.Meta.AllowedHosts))
					assert.NoError(t, jsplugin.ValidateRequestHeaders(request.Headers))
					assert.Equal(t, "Bearer vendor-key", request.Headers["Authorization"])
					assert.Equal(t, "request_file:image", request.FileRef)
					return jsplugin.UploadResponse{Status: 200, Body: []byte(`{"code":200,"data":{"fileName":"uploaded.png"}}`)}, nil
				})
				input := map[string]any{
					"baseUrl": baseURL, "apiKey": "vendor-key", "model": model,
					"requestBody": map[string]any{"prompt": "a cat"},
				}
				if model == "minimax-h3-reference-to-video" {
					input["files"] = []any{map[string]any{"ref": "request_file:image", "filename": "image.png", "mimeType": "image/png"}}
				}
				for _, hook := range []struct{ name, path string }{
					{"buildSubmitRequest", "/task/openapi/create"},
					{"buildQueryRequest", "/openapi/v2/query"},
				} {
					input["taskId"] = "vendor-task"
					value, callErr := plugin.Engine.Call(ctx, hook.name, input)
					require.NoError(t, callErr)
					encoded, marshalErr := common.Marshal(value)
					require.NoError(t, marshalErr)
					var request struct {
						URL     string            `json:"url"`
						Headers map[string]string `json:"headers"`
						Body    map[string]any    `json:"body"`
					}
					require.NoError(t, common.Unmarshal(encoded, &request))
					assert.Equal(t, base+hook.path, request.URL, hook.name)
					assert.NoError(t, jsplugin.ValidateRequestURL(request.URL, baseURL, plugin.Meta.AllowedHosts), hook.name)
					assert.NoError(t, jsplugin.ValidateRequestHeaders(request.Headers), hook.name)
					assert.Equal(t, "Bearer vendor-key", request.Headers["Authorization"])
					if hook.name == "buildSubmitRequest" {
						assert.Equal(t, "1", request.Body["workflowId"])
						var workflow map[string]any
						require.NoError(t, common.UnmarshalJsonStr(request.Body["workflow"].(string), &workflow))
						if model == "minimax-h3-reference-to-video" {
							assert.Equal(t, "a cat", workflow["25"].(map[string]any)["inputs"].(map[string]any)["value"])
							assert.Equal(t, "uploaded.png", workflow["100"].(map[string]any)["inputs"].(map[string]any)["image"])
						} else {
							assert.Equal(t, "a cat", workflow["5"].(map[string]any)["inputs"].(map[string]any)["text"])
						}
					}
				}
				if model == "minimax-h3-reference-to-video" {
					assert.Equal(t, 1, uploads)
				} else {
					assert.Zero(t, uploads)
				}
			})
		}
	}
}

func TestRunningHubCompletionBilling(t *testing.T) {
	source, err := Source("runninghub")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "runninghub"})
	require.NoError(t, err)
	const expression = `tier("RunningHub", (u("points") ?? u("coins") ?? (u("money") != nil ? 0 : 15)) * 0.002 + (u("money") ?? 0) * 0.2)`
	require.NoError(t, billing_setting.SmokeTestTaskExpr(expression, plugin.Meta.UsageSchema))
	snapshot := &billingexpr.BillingSnapshot{
		ExprString: expression, ExprVersion: billingexpr.ExprVersion(expression),
		TaskUsageBilling: true, GroupRatio: 1, QuotaPerUnit: 500000,
	}
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://www.runninghub.cn"}})
	for _, tc := range []struct {
		name, body string
		facts      map[string]any
		quota      int
	}{
		{"POINT actual receipt", `{"status":"SUCCESS","usage":{"settleMode":"POINT","chargedPoint":"6.12","consumeCoins":null},"taskUsageList":[{"usage":{"chargedPoint":"6.12"}}]}`, map[string]any{"points": 6.12}, 6120},
		{"POINT takes precedence", `{"status":"SUCCESS","usage":{"settleMode":"POINT","chargedPoint":10.37,"consumeCoins":15}}`, map[string]any{"points": 10.37}, 10370},
		{"wrapped zero points", `{"data":{"status":"SUCCESS","usage":{"settleMode":"POINT","chargedPoint":"0"}}}`, map[string]any{"points": float64(0)}, 0},
		{"legacy coins", `{"status":"SUCCESS","usage":{"consumeCoins":"7.5"}}`, map[string]any{"coins": 7.5}, 7500},
		{"legacy alias", `{"status":"SUCCESS","usage":{"consumeCoins":null,"consume_coins":2}}`, map[string]any{"coins": float64(2)}, 2000},
		{"wallet runtime plus API costs", `{"status":"SUCCESS","usage":{"consumeMoney":"0.1","thirdPartyConsumeMoney":"0.2","consumeCoins":null}}`, map[string]any{"money": 0.30000000000000004}, 30000},
		{"coins plus API wallet costs", `{"status":"SUCCESS","usage":{"consumeCoins":"2","thirdPartyConsumeMoney":"0.1"}}`, map[string]any{"coins": float64(2), "money": 0.1}, 12000},
		{"third party only", `{"status":"SUCCESS","usage":{"thirdPartyConsumeMoney":"0.1"}}`, map[string]any{"money": 0.1}, 10000},
		{"free wallet task", `{"status":"SUCCESS","usage":{"consumeMoney":0}}`, map[string]any{"money": float64(0)}, 0},
		{"POINT excludes legacy wallet", `{"status":"SUCCESS","usage":{"settleMode":"POINT","chargedPoint":"6.12","consumeMoney":1,"thirdPartyConsumeMoney":1}}`, map[string]any{"points": 6.12}, 6120},
		{"invalid wallet cannot partially settle", `{"status":"SUCCESS","usage":{"consumeCoins":2,"consumeMoney":"invalid"}}`, nil, 15000},
		{"wallet sum overflow retains reservation", `{"status":"SUCCESS","usage":{"consumeMoney":1e308,"thirdPartyConsumeMoney":1e308}}`, nil, 15000},
		{"missing usage retains reservation", `{"status":"SUCCESS"}`, nil, 15000},
		{"unidentified points retain reservation", `{"status":"SUCCESS","usage":{"chargedPoint":6.12}}`, nil, 15000},
		{"missing points cannot use coins", `{"status":"SUCCESS","usage":{"settleMode":"POINT","consumeCoins":15}}`, nil, 15000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := adaptor.ParseTaskResult(&model.Task{TaskID: "task_test"}, &http.Response{StatusCode: http.StatusOK}, []byte(tc.body))
			require.NoError(t, err)
			assert.Equal(t, "SUCCESS", result.Status)
			assert.Equal(t, tc.facts, result.UsageFacts)
			settled, _, err := service.EvaluateTaskCompletionUsage(snapshot, result.UsageFacts)
			require.NoError(t, err)
			assert.Equal(t, tc.quota, settled.ActualQuotaAfterGroup)
		})
	}
	for _, raw := range []any{nil, "", " ", "NaN", "Infinity", "-1", -1, true, []any{1}, map[string]any{"value": 1}} {
		for _, field := range []string{"consumeCoins", "chargedPoint", "consumeMoney", "thirdPartyConsumeMoney"} {
			usage := map[string]any{field: raw}
			if field == "chargedPoint" {
				usage["settleMode"] = "POINT"
			}
			value, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", nil, nil, map[string]any{"usage": usage})
			require.NoError(t, err)
			assert.Nil(t, value, "invalid usage: %#v (%s)", raw, field)
		}
	}
}
