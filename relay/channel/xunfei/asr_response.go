package xunfei

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// 讯飞 IFASR 转写的响应构造与音频计费用量。
//
// 客户端拿到的是 OpenAI 兼容的转写响应，而不是讯飞的原始结构：默认返回 {"text": "..."}，
// verbose_json 额外给出时长与分段列表，text 返回纯文本。

const (
	// asrResponseFormatJSON 默认的转写响应格式，只返回转写文本。
	asrResponseFormatJSON = "json"
	// asrResponseFormatVerboseJSON 带时长与分段列表的转写响应格式。
	asrResponseFormatVerboseJSON = "verbose_json"
	// asrResponseFormatText 纯文本转写响应格式。
	asrResponseFormatText = "text"

	// asrSpeakerLabelFormat 是并入文本的说话人标签格式。
	asrSpeakerLabelFormat = "[说话人%d]"
)

// WriteASRResponse 按 response_format 写出讯飞转写响应。
//
// json（默认）返回 {"text": "..."}；verbose_json 额外返回时长（秒）与分段列表；
// text 返回纯文本，其 Content-Type 是 text/plain，因此不能复用共享的 JSON 写出路径。
func WriteASRResponse(c *gin.Context, result *ASRResult, responseFormat string) error {
	if responseFormat == asrResponseFormatText {
		c.Writer.Header().Set("Content-Type", "text/plain")
		_, err := c.Writer.WriteString(buildASRText(result))
		return err
	}

	var body []byte
	var err error
	if responseFormat == asrResponseFormatVerboseJSON {
		body, err = common.Marshal(buildASRVerboseResponse(result))
	} else {
		body, err = common.Marshal(dto.AudioResponse{Text: buildASRText(result)})
	}
	if err != nil {
		return err
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	_, err = c.Writer.Write(body)
	return err
}

// BuildASRUsage 产出音频结算用量：输入侧音频 token，并同步输入 token 计数与总量。
//
// 时长事实来源优先取上游回报的原始音频时长（客户端无法伪造），上游缺失时回落到本地
// 解析出的音频时长。音频 token 落在输入侧，音频结算入口据此选中；总量必须同步，
// 否则结算层会因总量为零把配额置零并记一条「可能是上游超时」的错误日志。
func BuildASRUsage(result *ASRResult, audio ASRAudio) *dto.Usage {
	usage, _ := buildASRUsage(result, audio)
	return usage
}

// buildASRUsage 计算转写用量，并给出时长换算过程中的饱和标记。
//
// 饱和标记由调用方交给结算层记录：记录函数与「首次钳制生效」的守卫都在 service 包内，
// 渠道包不得直接写结算层的标记字段。
func buildASRUsage(result *ASRResult, audio ASRAudio) (*dto.Usage, *common.QuotaClamp) {
	durationMs := result.OriginalDurationMs
	if durationMs == 0 {
		durationMs = audio.DurationMs
	}
	if durationMs < 0 {
		durationMs = 0
	}

	// 每分钟 1000 token，与仓库既有音频计费口径一致；饱和换算防止异常时长造成整数回绕。
	seconds := float64(durationMs) / 1000
	audioTokens, clamp := common.QuotaRoundChecked(math.Ceil(seconds) / 60.0 * 1000)

	return &dto.Usage{
		PromptTokens:        audioTokens,
		TotalTokens:         audioTokens,
		PromptTokensDetails: dto.InputTokenDetails{AudioTokens: audioTokens},
	}, clamp
}

// buildASRVerboseResponse 构造 verbose_json 格式的响应体。
//
// 时长由上游毫秒值除以 1000 换算成秒；语种直接取上游返回值，上游未返回时留空。
func buildASRVerboseResponse(result *ASRResult) dto.TranscriptionVerboseJSONResponse {
	segments := make([]dto.TranscriptionSegment, 0, len(result.Segments))
	for _, segment := range result.Segments {
		segments = append(segments, dto.TranscriptionSegment{
			Id:    segment.Index,
			Start: float64(segment.StartMs) / 1000,
			End:   float64(segment.EndMs) / 1000,
			Text:  segment.Text,
		})
	}
	return dto.TranscriptionVerboseJSONResponse{
		Text:     buildASRText(result),
		Language: result.Language,
		Duration: float64(result.OriginalDurationMs) / 1000,
		Segments: segments,
	}
}

// buildASRText 生成面向客户端的转写文本。
//
// 解析层给出的全文是各分段文本的无分隔拼接；上游开启角色分离时，说话人按出现顺序编号
// 并以标签形式并入文本，客户端不必自己解析时间戳即可分辨发言人。
func buildASRText(result *ASRResult) string {
	if len(result.Segments) == 0 {
		return result.Text
	}

	speakers := make(map[string]int)
	lines := make([]string, 0, len(result.Segments))
	labeled := false
	for _, segment := range result.Segments {
		text := segment.Text
		if segment.Speaker != "" {
			labeled = true
			index, ok := speakers[segment.Speaker]
			if !ok {
				index = len(speakers) + 1
				speakers[segment.Speaker] = index
			}
			text = fmt.Sprintf(asrSpeakerLabelFormat, index) + text
		}
		lines = append(lines, text)
	}
	if !labeled {
		// 没有说话人信息时保持解析层给出的无分隔全文。
		return result.Text
	}
	return strings.Join(lines, "\n")
}
