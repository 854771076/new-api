package xunfei

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ASRResultType 讯飞 IFASR 轮询查询的结果类型。
type ASRResultType string

const (
	// ASRResultTypeTransfer 转写结果（默认结果类型）。
	ASRResultTypeTransfer ASRResultType = "transfer"
	// ASRResultTypeAnalysis 语音语种识别结果。
	ASRResultTypeAnalysis ASRResultType = "analysis"
)

// 讯飞 IFASR 订单流程状态。
const (
	// ASROrderStatusCreated 订单已创建。
	ASROrderStatusCreated = 0
	// ASROrderStatusProcessing 订单处理中。
	ASROrderStatusProcessing = 3
	// ASROrderStatusCompleted 订单已完成。
	ASROrderStatusCompleted = 4
	// ASROrderStatusFailed 订单失败。
	ASROrderStatusFailed = -1
)

// ASRSegment 转写结果的一个分段。
type ASRSegment struct {
	// Index 分段序号，从 0 开始。
	Index int
	// Speaker 说话人标识（上游 rl 字段）；上游未开启角色分离时为空字符串。
	Speaker string
	// StartMs 分段起始时间，单位毫秒。
	StartMs int64
	// EndMs 分段结束时间，单位毫秒。
	EndMs int64
	// Text 分段文本。
	Text string
}

// ASROrderInfo 订单信息，对应上游 content.orderInfo 对象。
type ASROrderInfo struct {
	// OrderId 订单号。
	OrderId string
	// Status 订单流程状态：0 已创建 / 3 处理中 / 4 已完成 / -1 失败。
	Status int
	// FailType 订单异常状态码，0 表示正常。
	FailType int
	// ExpireTimeMs 订单结果过期时间，单位毫秒；上游未返回时为 0。
	ExpireTimeMs int64
}

// ASRResult 讯飞 IFASR 转写订单的解析结果。
type ASRResult struct {
	// ResultType 本次查询使用的结果类型。
	ResultType ASRResultType
	// Code 上游业务返回码，成功为 "000000"。
	Code string
	// DescInfo 上游返回描述。
	DescInfo string
	// Text 全文转写文本，由各分段文本按顺序无分隔拼接。
	Text string
	// Segments 分段列表，说话人变化处切段。
	Segments []ASRSegment
	// OriginalDurationMs 原始音频时长，单位毫秒，来自上游订单信息，是计费的时长事实来源；上游未返回时为 0。
	OriginalDurationMs int64
	// Language 识别语种；上游仅在开启语种分析能力且以 analysis 结果类型查询时返回，缺失时为空字符串。
	Language string
	// Order 订单信息。
	Order ASROrderInfo
}

// ParseASRResult 解析讯飞 IFASR 轮询响应体（/v2/getResult 的响应），返回转写结果。
//
// 上游把转写结果放在 content.orderResult 里，而该字段本身是一个 JSON 字符串；
// 字符串内部每个分段单元的 json_1best 又是一个 JSON 字符串，因此需要逐层解码。
//
// 结果类型只决定上游是否返回语种字段：解析器在字段存在时取出，不存在时留空，不据结果类型臆造语种。
//
// 错误边界：响应体不是合法的 JSON 对象时返回错误；其余情况（转写载荷缺失、载荷畸形、
// 没有可用分段）一律返回空结果与 nil 错误，由上层据此判定。
func ParseASRResult(body []byte, resultType ASRResultType) (*ASRResult, error) {
	var envelope asrPollEnvelope
	if err := common.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("讯飞转写响应不是合法的 JSON: %w", err)
	}

	info := envelope.Content.OrderInfo
	result := &ASRResult{
		ResultType:         resultType,
		Code:               string(envelope.Code),
		DescInfo:           envelope.DescInfo,
		OriginalDurationMs: info.OriginalDuration,
		Language:           info.Language,
		Order: ASROrderInfo{
			OrderId:      info.OrderId,
			Status:       info.Status,
			FailType:     info.FailType,
			ExpireTimeMs: info.ExpireTime,
		},
	}

	orderResult, ok := decodeASROrderResult(envelope.Content.OrderResult)
	if !ok {
		return result, nil
	}
	// lattice 是做了顺滑的识别结果；缺失或为空时回退到未做顺滑的 lattice2，两者都不可用则没有结果。
	units := orderResult.Lattice
	if len(units) == 0 {
		units = orderResult.Lattice2
	}
	result.Segments = buildASRSegments(units)

	var builder strings.Builder
	for _, segment := range result.Segments {
		builder.WriteString(segment.Text)
	}
	result.Text = builder.String()
	return result, nil
}

// asrPollEnvelope 讯飞 IFASR 轮询接口（/v2/getResult）的响应结构。
//
// 业务码用宽松取值承接：官方成功响应的码是字符串 "000000"，失败响应的示例却是数字
// （如 100003），若按字符串解析会让整段响应解析失败，错误码根本到不了映射层。
type asrPollEnvelope struct {
	Code     asrBusinessCode `json:"code"`
	DescInfo string          `json:"descInfo"`
	Content  struct {
		OrderInfo asrOrderInfoPayload `json:"orderInfo"`
		// OrderResult 上游该字段是一个 JSON 字符串（内含 lattice 列表），这里保留原始 JSON 以便区分字符串与对象。
		OrderResult common.RawMessage `json:"orderResult"`
	} `json:"content"`
}

// asrOrderInfoPayload 上游 content.orderInfo 对象。
type asrOrderInfoPayload struct {
	OrderId          string `json:"orderId"`
	Status           int    `json:"status"`
	FailType         int    `json:"failType"`
	OriginalDuration int64  `json:"originalDuration"`
	ExpireTime       int64  `json:"expireTime"`
	Language         string `json:"language"`
}

// asrOrderResult 转写载荷的解码结果。
type asrOrderResult struct {
	Lattice  []asrLatticeUnit `json:"lattice"`
	Lattice2 []asrLatticeUnit `json:"lattice2"`
}

// asrLatticeUnit 一个分段单元，装着单个 vad 的识别结果。
//
// 上游两个版本给出的字段位置不同：
//   - lattice（已做顺滑）的 json_1best 是 JSON 字符串，起止时间与说话人在其中的 st 上；
//   - lattice2（未做顺滑）的 json_1best 直接是 JSON 对象，起止时间与说话人在单元层级。
//
// 因此 json_1best 用 asrJSONPayload 兼容两种形态，单元层级的时间与说话人也一并承接。
type asrLatticeUnit struct {
	Json1best asrJSONPayload `json:"json_1best"`
	// BG 单元层级的起始时间，单位毫秒；lattice 形态不带该字段。
	BG asrUpstreamValue `json:"bg"`
	// ED 单元层级的结束时间，单位毫秒；lattice 形态不带该字段。
	ED asrUpstreamValue `json:"ed"`
	// RL 单元层级的说话人标识；lattice 形态不带该字段。
	RL asrUpstreamValue `json:"rl"`
	// Begin 单元层级的起始时间，单位毫秒；上游较新的 lattice2 版本用这个字段名。
	Begin asrUpstreamValue `json:"begin"`
	// End 单元层级的结束时间，单位毫秒；上游较新的 lattice2 版本用这个字段名。
	End asrUpstreamValue `json:"end"`
	// Spk 单元层级的说话人标识；上游较新的 lattice2 版本用这个字段名。
	Spk asrUpstreamValue `json:"spk"`
}

// asrJSONPayload 是 json_1best 字段的载荷，兼容 JSON 字符串与 JSON 对象两种形态。
//
// 载荷保留原始字节，由 decodeASRSentence 统一解码：字符串形态先做一次过度转义还原，
// 对象形态直接使用。
type asrJSONPayload struct {
	raw []byte
}

// UnmarshalJSON 接受 JSON 字符串与 JSON 对象两种形态。
func (p *asrJSONPayload) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := common.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		p.raw = []byte(text)
		return nil
	}
	p.raw = append([]byte(nil), trimmed...)
	return nil
}

// asrSentence 分段单元的识别结果（json_1best 字段解码后的形态）。
//
// 起止时间与说话人标识在不同版本里位置不同：
//   - lattice（已做顺滑）：放在 st 内部；
//   - lattice2（未做顺滑）：放在 st 的同级。
//
// 两处都承接，由 buildASRSegments 按优先级取值。
type asrSentence struct {
	ST asrSentenceST `json:"st"`
	// BG 单句开始时间，单位毫秒；lattice2 形态放在 st 的同级。
	BG asrUpstreamValue `json:"bg"`
	// ED 单句结束时间，单位毫秒；lattice2 形态放在 st 的同级。
	ED asrUpstreamValue `json:"ed"`
	// RL 分离的角色编号；lattice2 形态放在 st 的同级。
	RL asrUpstreamValue `json:"rl"`
}

// asrSentenceST 单句的识别内容。
type asrSentenceST struct {
	// BG 单句开始时间，单位毫秒。
	BG asrUpstreamValue `json:"bg"`
	// ED 单句结束时间，单位毫秒。
	ED asrUpstreamValue `json:"ed"`
	// RL 分离的角色编号；未开启角色分离时上游不返回该字段。
	RL asrUpstreamValue `json:"rl"`
	RT []asrSentenceRT  `json:"rt"`
}

// asrSentenceRT 识别结果中的词序列分组。
type asrSentenceRT struct {
	WS []asrSentenceWS `json:"ws"`
}

// asrSentenceWS 一个词的候选识别结果。
type asrSentenceWS struct {
	CW []asrSentenceCW `json:"cw"`
}

// asrSentenceCW 词的候选字。
type asrSentenceCW struct {
	W string `json:"w"`
}

// asrUpstreamValue 上游以字符串承载的字段（起止时间毫秒、说话人标识）。
// 这里同时容忍数字形态，避免上游偶发返回数字时整段识别结果无法解析。
type asrUpstreamValue string

// asrBusinessCode 上游业务码，兼容字符串与数字两种形态。
//
// 官方成功响应的业务码是字符串 "000000"，失败响应的示例却是数字（如 100003）；
// 两种形态统一补零到 6 位，与错误码对照表的写法一致，才能落到同一套分类映射上。
type asrBusinessCode string

// asrBusinessCodeWidth 是上游业务码的固定宽度。
const asrBusinessCodeWidth = 6

// UnmarshalJSON 接受字符串与数字两种形态；纯数字形态补零到 6 位。
func (c *asrBusinessCode) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*c = ""
		return nil
	}
	text := string(trimmed)
	if trimmed[0] == '"' {
		var decoded string
		if err := common.Unmarshal(trimmed, &decoded); err != nil {
			return err
		}
		text = decoded
	}
	*c = asrBusinessCode(padASRBusinessCode(text))
	return nil
}

// padASRBusinessCode 把纯数字的业务码补零到 6 位；非数字形态原样保留。
func padASRBusinessCode(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len(trimmed) >= asrBusinessCodeWidth {
		return trimmed
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return trimmed
		}
	}
	return strings.Repeat("0", asrBusinessCodeWidth-len(trimmed)) + trimmed
}

// UnmarshalJSON 接受字符串与数字两种形态，其余形态按原始文本保留。
func (v *asrUpstreamValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := common.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		*v = asrUpstreamValue(text)
		return nil
	}
	*v = asrUpstreamValue(trimmed)
	return nil
}

// millis 解析毫秒值；ok 报告上游是否给出了该字段。
//
// 缺失与非数字形态都返回 ok=false，调用方据此区分「未取到」与「确实是 0」：
// 音频从 0ms 开始时上游给出的就是 "0"，0 不能当作「未取到」的哨兵。
func (v asrUpstreamValue) millis() (int64, bool) {
	text := strings.TrimSpace(string(v))
	if text == "" {
		return 0, false
	}
	millis, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false
	}
	return millis, true
}

// speaker 说话人标识；上游未开启角色分离时该字段缺失，返回空字符串。
func (v asrUpstreamValue) speaker() string {
	return string(v)
}

// decodeASROrderResult 解析 content.orderResult 的转写载荷。
//
// 上游该字段正常是一个 JSON 字符串：字符串先做一次过度转义还原（连续两个反斜杠 → 一个，
// 单次全局替换、不重复迭代），再做一次 JSON 解码得到 lattice 列表；上游直接给出 JSON 对象时直接解码。
// 载荷缺失或畸形时返回 false，由调用方按「无可用结果」处理。
func decodeASROrderResult(raw common.RawMessage) (asrOrderResult, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return asrOrderResult{}, false
	}
	if trimmed[0] == '"' {
		var payload string
		if err := common.Unmarshal(trimmed, &payload); err != nil {
			return asrOrderResult{}, false
		}
		payload = strings.ReplaceAll(payload, `\\`, `\`)
		var orderResult asrOrderResult
		if err := common.UnmarshalJsonStr(payload, &orderResult); err != nil {
			return asrOrderResult{}, false
		}
		return orderResult, true
	}
	var orderResult asrOrderResult
	if err := common.Unmarshal(trimmed, &orderResult); err != nil {
		return asrOrderResult{}, false
	}
	return orderResult, true
}

// decodeASRSentence 解析分段单元的识别结果（json_1best 的载荷）。
//
// 字符串形态先按原样解码；解码失败时再做一次过度转义还原后重试，覆盖上游重复转义的响应。
func decodeASRSentence(payload asrJSONPayload) (asrSentence, bool) {
	if len(payload.raw) == 0 {
		return asrSentence{}, false
	}
	var sentence asrSentence
	if err := common.Unmarshal(payload.raw, &sentence); err == nil {
		return sentence, true
	}
	reduced := strings.ReplaceAll(string(payload.raw), `\\`, `\`)
	if err := common.UnmarshalJsonStr(reduced, &sentence); err != nil {
		return asrSentence{}, false
	}
	return sentence, true
}

// buildASRSegments 由分段单元构建分段列表。
//
// 起止时间与说话人标识优先取单元层级（lattice2 形态），缺失时回落到识别结果里的 st（lattice 形态）。
// 相邻分段单元的说话人标识不同时切为新段；同一说话人内的时间范围取各单元的并集
// （起始取最小、结束取最新）。
//
// 是否取到起止时间由 asrUpstreamValue.millis 的 ok 标记，而不是用 0 当哨兵：音频常从
// 0ms 开始，0 是合法的起点，用 0 表示「未取到」会让并集起点被后续单元抬晚。
func buildASRSegments(units []asrLatticeUnit) []ASRSegment {
	segments := make([]ASRSegment, 0, len(units))
	// lastHasStart/lastHasEnd 记录当前末段是否已取到起止时间，供并集更新判断。
	lastHasStart, lastHasEnd := false, false
	for _, unit := range units {
		sentence, ok := decodeASRSentence(unit.Json1best)
		if !ok {
			continue
		}
		speaker := unit.RL.speaker()
		// 起点与终点分别回退：只丢掉其中一个是上游的常见形态，成对回退会让
		// 另一端停在默认的 0，产出 EndMs 为 0 的分段。
		start, hasStart := unit.BG.millis()
		end, hasEnd := unit.ED.millis()
		if !hasStart {
			// 上游较新的 lattice2 版本把时间写成 begin/end。
			start, hasStart = unit.Begin.millis()
		}
		if !hasEnd {
			end, hasEnd = unit.End.millis()
		}
		if !hasStart {
			start, hasStart = sentence.BG.millis()
		}
		if !hasEnd {
			end, hasEnd = sentence.ED.millis()
		}
		if !hasStart {
			start, hasStart = sentence.ST.BG.millis()
		}
		if !hasEnd {
			end, hasEnd = sentence.ST.ED.millis()
		}
		// rl 是上游的角色编号，语义上就是说话人；spk 只在没有 rl 的响应里才当说话人用。
		if speaker == "" {
			speaker = sentence.RL.speaker()
		}
		if speaker == "" {
			speaker = sentence.ST.RL.speaker()
		}
		if speaker == "" {
			speaker = unit.Spk.speaker()
		}
		if len(segments) > 0 && segments[len(segments)-1].Speaker == speaker {
			last := &segments[len(segments)-1]
			if hasStart && (!lastHasStart || start < last.StartMs) {
				last.StartMs = start
				lastHasStart = true
			}
			if hasEnd && (!lastHasEnd || end > last.EndMs) {
				last.EndMs = end
				lastHasEnd = true
			}
			last.Text += sentence.text()
			continue
		}
		segments = append(segments, ASRSegment{
			Speaker: speaker,
			StartMs: start,
			EndMs:   end,
			Text:    sentence.text(),
		})
		lastHasStart, lastHasEnd = hasStart, hasEnd
	}

	// 去掉没有文本的空段，并按最终顺序编号。
	result := segments[:0]
	for _, segment := range segments {
		if segment.Text == "" {
			continue
		}
		result = append(result, segment)
	}
	for i := range result {
		result[i].Index = i
	}
	return result
}

// text 拼接该句的识别文本：每个词取候选字数组的第一个。
func (s asrSentence) text() string {
	var builder strings.Builder
	for _, rt := range s.ST.RT {
		for _, ws := range rt.WS {
			if len(ws.CW) == 0 {
				continue
			}
			builder.WriteString(ws.CW[0].W)
		}
	}
	return builder.String()
}
