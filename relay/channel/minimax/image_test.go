package minimax

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiniMaxImageEndpointSelection(t *testing.T) {
	t.Parallel()

	baseURL := "https://api.minimax.chat"
	tests := []struct {
		name          string
		originModel   string
		upstreamModel string
		wantURL       string
	}{
		{
			name:          "h3 image uses the new endpoint",
			originModel:   "MiniMax-H3-Image",
			upstreamModel: "MiniMax-H3-Image",
			wantURL:       baseURL + "/v1/images/generations",
		},
		{
			name:          "h3 image highspeed uses the new endpoint",
			originModel:   "MiniMax-H3-Image-Highspeed",
			upstreamModel: "MiniMax-H3-Image-Highspeed",
			wantURL:       baseURL + "/v1/images/generations",
		},
		{
			name:          "legacy model keeps the legacy endpoint",
			originModel:   "image-01",
			upstreamModel: "image-01",
			wantURL:       baseURL + "/v1/image_generation",
		},
		{
			name:          "mapped client name follows the upstream model",
			originModel:   "my-image-model",
			upstreamModel: "MiniMax-H3-Image",
			wantURL:       baseURL + "/v1/images/generations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode:       relayconstant.RelayModeImagesGenerations,
				OriginModelName: tt.originModel,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    baseURL,
					UpstreamModelName: tt.upstreamModel,
				},
			}

			got, err := GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, got)
		})
	}

	// 新端点只收 n 1-10，越界以 400 拒绝，转换阶段就失败，请求不会发往上游
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "my-image-model",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "MiniMax-H3-Image",
		},
	}
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())

	payload, err := adaptor.ConvertImageRequest(c, info, dto.ImageRequest{
		Model:  "my-image-model",
		Prompt: "a red fox in snowfall",
		N:      uintPtr(11),
	})
	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "10")
	assert.Nil(t, payload)

	// 上界内的请求正常翻译
	payload, err = adaptor.ConvertImageRequest(c, info, dto.ImageRequest{
		Model:  "my-image-model",
		Prompt: "a red fox in snowfall",
		N:      uintPtr(10),
	})
	require.NoError(t, err)
	require.NotNil(t, payload)
}

func TestMiniMaxImageNewEndpointResponse(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	adaptor := &Adaptor{}

	newInfo := func() *relaycommon.RelayInfo {
		info := &relaycommon.RelayInfo{
			RelayMode:       relayconstant.RelayModeImagesGenerations,
			OriginModelName: "MiniMax-H3-Image",
			StartTime:       time.Unix(1700000000, 0),
			ChannelMeta: &relaycommon.ChannelMeta{
				UpstreamModelName: "MiniMax-H3-Image",
			},
		}
		info.PriceData.UsePrice = true
		return info
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := newInfo()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: ioNopCloser(`{"data":[{"url":"https://example.com/h3-1.png","size":"2048x2048","revised_prompt":"a red fox"},` +
			`{"url":"https://example.com/h3-2.png"},{"b64_json":"aGVsbG8="}]}`),
	}

	usage, apiErr := adaptor.DoResponse(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var payload struct {
		Data []dto.ImageData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.Data, 3)
	assert.Equal(t, "https://example.com/h3-1.png", payload.Data[0].Url)
	assert.Equal(t, "a red fox", payload.Data[0].RevisedPrompt)
	assert.Equal(t, "https://example.com/h3-2.png", payload.Data[1].Url)
	assert.Equal(t, "aGVsbG8=", payload.Data[2].B64Json)

	// 张数按 max(有 url 的条目数, 有 b64_json 的条目数) 回写，不是 len(data)
	assert.Equal(t, float64(2), info.PriceData.OtherRatios()["n"])

	// 某一项带 error 时整次请求报错并带上游 message
	errorRecorder := httptest.NewRecorder()
	errorContext, _ := gin.CreateTestContext(errorRecorder)
	errorInfo := newInfo()
	errorResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       ioNopCloser(`{"data":[{"error":{"code":1004,"message":"prompt exceeds the length limit"}}]}`),
	}

	_, apiErr = adaptor.DoResponse(errorContext, errorResp, errorInfo)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "prompt exceeds the length limit")
	// 非幂等的上游错误不能被网关自动重投到下一个渠道
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.False(t, types.IsChannelError(apiErr))
}

func TestMiniMaxImageLegacyEndpointUnchanged(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "image-01",
		StartTime:       time.Unix(1700000000, 0),
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "image-01",
		},
	}
	info.PriceData.UsePrice = true

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       ioNopCloser(`{"data":{"image_urls":["https://example.com/legacy.png"],"image_base64":["aGVsbG8="]}}`),
	}

	adaptor := &Adaptor{}
	usage, apiErr := adaptor.DoResponse(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var payload struct {
		Data []dto.ImageData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.Data, 2)
	assert.Equal(t, "https://example.com/legacy.png", payload.Data[0].Url)
	assert.Empty(t, payload.Data[0].B64Json)
	assert.Equal(t, "aGVsbG8=", payload.Data[1].B64Json)
	assert.Empty(t, payload.Data[1].Url)

	// 旧端点的两个数组并进同一个数组，计费张数按 max 规则推导
	assert.Equal(t, float64(1), info.PriceData.OtherRatios()["n"])
}

// TestMiniMaxImageRegression 是 MiniMax 图像链的条目数护栏：
// 旧端点 /v1/image_generation 的 data.image_urls[] 与 data.image_base64[] 并进同一条 data[]，
// 条目数等于两个列表长度之和；新端点 /v1/images/generations 的 data[] 一条对一条，
// 条目数等于上游给的条数。本用例只数条目，不重复断言计费张数。
func TestMiniMaxImageRegression(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	adaptor := &Adaptor{}

	tests := []struct {
		name          string
		upstreamModel string
		body          string
		wantTotal     int
		wantURLs      int
		wantB64s      int
	}{
		{
			name:          "legacy endpoint merges both lists into one array",
			upstreamModel: "image-01",
			body: `{"data":{"image_urls":["https://example.com/l1.png","https://example.com/l2.png","https://example.com/l3.png"],` +
				`"image_base64":["aGVsbG8=","d29ybGQ="]}}`,
			wantTotal: 5,
			wantURLs:  3,
			wantB64s:  2,
		},
		{
			name:          "new endpoint keeps one entry per upstream item",
			upstreamModel: "MiniMax-H3-Image",
			body: `{"data":[` +
				`{"url":"https://example.com/n1.png"},` +
				`{"url":"https://example.com/n2.png","b64_json":"aGVsbG8="},` +
				`{"revised_prompt":"a red fox"}]}`,
			wantTotal: 3,
			wantURLs:  2,
			wantB64s:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			info := &relaycommon.RelayInfo{
				RelayMode:       relayconstant.RelayModeImagesGenerations,
				OriginModelName: tt.upstreamModel,
				StartTime:       time.Unix(1700000000, 0),
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: tt.upstreamModel,
				},
			}

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       ioNopCloser(tt.body),
			}

			_, apiErr := adaptor.DoResponse(c, resp, info)
			require.Nil(t, apiErr)

			var payload struct {
				Data []dto.ImageData `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
			require.Len(t, payload.Data, tt.wantTotal)

			var urls, b64s int
			for _, item := range payload.Data {
				if item.Url != "" {
					urls++
				}
				if item.B64Json != "" {
					b64s++
				}
			}
			assert.Equal(t, tt.wantURLs, urls)
			assert.Equal(t, tt.wantB64s, b64s)
		})
	}
}
