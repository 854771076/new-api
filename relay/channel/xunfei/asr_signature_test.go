package xunfei

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 讯飞 IFASR 签名测试使用的固定凭据与固定向量。
// 固定向量的计算过程：baseString = 参数按名字典序排序后逐个拼接「URL编码(key)=URL编码(value)」，
// 以 & 连接，再以 apiSecret 为密钥做 HMAC-SHA1，Base64 编码。
// 向量由独立实现（Node crypto / OpenSSL dgst）交叉验证得出，与官方 Java/Python 示例算法一致。
const (
	testXfyunASRSecret = "test-access-key-secret"

	// 官方示例形态的完整上传参数集。
	testXfyunASRSampleBaseString = "accessKeyId=a296be0df88f701ec2e4882be7727568" +
		"&appId=37f3f2b5" +
		"&dateTime=2025-09-08T22%3A58%3A29%2B0800" +
		"&duration=56352" +
		"&fileName=lfasr_%E6%B6%89%E6%94%BF.wav" +
		"&fileSize=3136940" +
		"&language=autodialect" +
		"&signatureRandom=moI5WkopgjL1EL5Y"
	testXfyunASRSampleSignature = "DHIRXj5x8fHHXINrVoXKH3Zeu3I="
)

func testXfyunASRSampleParams() url.Values {
	return url.Values{
		"appId":           {"37f3f2b5"},
		"accessKeyId":     {"a296be0df88f701ec2e4882be7727568"},
		"dateTime":        {"2025-09-08T22:58:29+0800"},
		"signatureRandom": {"moI5WkopgjL1EL5Y"},
		"fileSize":        {"3136940"},
		"fileName":        {"lfasr_涉政.wav"},
		"duration":        {"56352"},
		"language":        {"autodialect"},
	}
}

func TestXfyunASRSignature(t *testing.T) {
	tests := []struct {
		name   string
		params url.Values
		secret string
		want   string
	}{
		{
			name:   "官方示例参数集",
			params: testXfyunASRSampleParams(),
			secret: testXfyunASRSecret,
			want:   testXfyunASRSampleSignature,
		},
		{
			name:   "空参数集合",
			params: url.Values{},
			secret: "secret",
			want:   "Ja9hdKD87MTTRmgKcrfOZEuaiOg=",
		},
		{
			name:   "空值参数不参与签名",
			params: url.Values{"appId": {"x"}, "empty": {""}},
			secret: "secret",
			want:   "daBzQ+3EuKahJ/2CqgkPFGEY2ZQ=",
		},
		{
			name:   "signature 参数不参与签名",
			params: url.Values{"appId": {"x"}, "signature": {"whatever"}},
			secret: "secret",
			want:   "daBzQ+3EuKahJ/2CqgkPFGEY2ZQ=",
		},
		{
			name:   "值含特殊字符时按完整 URL 编码参与签名",
			params: url.Values{"a b": {"c+d"}, "k/v": {"m=n"}, "中文": {"值"}},
			secret: "secret",
			want:   "4dBPHSbj22p9+sLL5b+PMhV4ClU=",
		},
		{
			name:   "参数集合相同则签名相同（与写入顺序无关）",
			params: url.Values{"dateTime": {"c"}, "appId": {"a"}, "accessKeyId": {"b"}},
			secret: "secret",
			want:   "L48kbp6gWf8Mi87Z+p8/5pN6IAA=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, XfyunASRSignature(tt.params, tt.secret))
		})
	}
}

func TestXfyunASRSignBaseString(t *testing.T) {
	tests := []struct {
		name   string
		params url.Values
		want   string
	}{
		{
			name:   "参数按名字典序排序",
			params: testXfyunASRSampleParams(),
			want:   testXfyunASRSampleBaseString,
		},
		{
			name:   "signature 与空值参数被过滤",
			params: url.Values{"signature": {"drop-me"}, "empty": {""}, "appId": {"x"}},
			want:   "appId=x",
		},
		{
			name:   "空参数集合得到空串",
			params: url.Values{},
			want:   "",
		},
		{
			name:   "键与值都做完整 URL 编码",
			params: url.Values{"a b": {"c+d"}, "k/v": {"m=n"}, "中文": {"值"}},
			want:   "a%20b=c%2Bd&k%2Fv=m%3Dn&%E4%B8%AD%E6%96%87=%E5%80%BC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, xfyunASRSignBaseString(tt.params))
		})
	}
}

func TestXfyunURLEncode(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "空串", value: "", want: ""},
		{name: "字母数字原样保留", value: "abcXYZ019", want: "abcXYZ019"},
		{name: "unreserved 字符原样保留", value: "a.b_c-d~e", want: "a.b_c-d~e"},
		{name: "空格编码为百分号二十", value: "a b", want: "a%20b"},
		{name: "加号编码", value: "c+d", want: "c%2Bd"},
		{name: "冒号编码", value: "22:58:29", want: "22%3A58%3A29"},
		{name: "斜杠编码（safe 字符集为空）", value: "k/v", want: "k%2Fv"},
		{name: "等号与与号编码", value: "m=n&o=p", want: "m%3Dn%26o%3Dp"},
		{name: "中文按 UTF-8 字节编码", value: "中文", want: "%E4%B8%AD%E6%96%87"},
		{name: "下划线与点号不编码", value: "lfasr_涉政.wav", want: "lfasr_%E6%B6%89%E6%94%BF.wav"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, XfyunURLEncode(tt.value))
		})
	}
}

// 签名串与最终查询串必须由同一套编码产生，否则空格等字符的两种编码形式会让上游验签失败（错误码 100009）。
func TestXfyunASRQueryStringMatchesSignBaseString(t *testing.T) {
	params := testXfyunASRSampleParams()
	params.Set("fileName", "a b+c/涉政.wav")

	assert.Equal(t, xfyunASRSignBaseString(params), XfyunASRQueryString(params))
	assert.Contains(t, XfyunASRQueryString(params), "fileName=a%20b%2Bc%2F%E6%B6%89%E6%94%BF.wav")
	assert.NotContains(t, XfyunASRQueryString(params), "+")
}

func TestXfyunASRDateTime(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)

	fixed := time.Date(2025, 9, 8, 22, 58, 29, 0, loc)
	assert.Equal(t, "2025-09-08T22:58:29+0800", XfyunASRDateTime(fixed))

	pattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+0800$`)
	assert.Regexp(t, pattern, XfyunASRDateTime(time.Now()))

	// UTC 时刻换算到东八区后偏移量仍为 +0800。
	utc := time.Date(2025, 9, 8, 14, 58, 29, 0, time.UTC)
	assert.Equal(t, "2025-09-08T22:58:29+0800", XfyunASRDateTime(utc))
}

func TestXfyunASRSignatureRandom(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9A-Za-z]{16}$`)

	first := XfyunASRSignatureRandom()
	second := XfyunASRSignatureRandom()

	assert.Len(t, first, 16)
	assert.Regexp(t, pattern, first)
	assert.Regexp(t, pattern, second)
	assert.NotEqual(t, first, second, "两次生成不应得到相同随机串")

	// 复用语义：同一 random 参与两次请求的签名计算，随机串本身出现在签名基础串中，
	// 而 dateTime 各自变化会让两次签名不同。
	uploadParams := url.Values{"signatureRandom": {first}, "dateTime": {"2025-09-08T22:58:29+0800"}}
	pollParams := url.Values{"signatureRandom": {first}, "dateTime": {"2025-09-08T22:59:30+0800"}}
	assert.Contains(t, xfyunASRSignBaseString(uploadParams), "signatureRandom="+first)
	assert.NotEqual(t, XfyunASRSignature(uploadParams, testXfyunASRSecret), XfyunASRSignature(pollParams, testXfyunASRSecret))
}

func TestXfyunASRBuildRequest(t *testing.T) {
	params := testXfyunASRSampleParams()
	endpoint := "https://office-api-ist-dx.iflyaisol.com/v2/upload"

	requestURL, signature := XfyunASRBuildRequest(endpoint, params, testXfyunASRSecret)

	assert.Equal(t, testXfyunASRSampleSignature, signature)
	assert.Equal(t, endpoint+"?"+testXfyunASRSampleBaseString, requestURL)

	// 签名只走请求头，不进查询串。
	assert.NotContains(t, requestURL, XfyunASRSignatureHeader+"=")
	assert.NotContains(t, requestURL, signature)

	parsed, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Empty(t, parsed.Query().Get("signature"))
	assert.Equal(t, "37f3f2b5", parsed.Query().Get("appId"))
	assert.Equal(t, "2025-09-08T22:58:29+0800", parsed.Query().Get("dateTime"))

	// 查询串里的每个参数片段都由统一编码函数产生。
	for key, values := range params {
		assert.True(t, strings.Contains(requestURL, XfyunURLEncode(key)+"="+XfyunURLEncode(values[0])),
			"查询串应包含 %s 的统一编码片段", key)
	}
}
