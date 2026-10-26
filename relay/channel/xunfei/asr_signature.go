package xunfei

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"net/url"
	"slices"
	"strings"
	"time"
)

// 讯飞 IFASR（录音文件转写大模型）请求签名。
//
// 官方鉴权说明见 https://www.xfyun.cn/doc/spark/asr_llm/Ifasr_llm.html：
//   1. 排除待签名参数中的 "signature" 字段；
//   2. 其余参数按参数名自然排序（与 Java TreeMap 一致）；
//   3. 排序后的键值对分别做标准 URL 编码，拼成 encoded_key=encoded_value，用 & 连接成 baseString；
//   4. 以 accessKeySecret 为密钥对 baseString 做 HMAC-SHA1，Base64 编码后得到 signature；
//   5. signature 走 HTTP 请求头，不进查询串（上传与查询两个接口都必须带）。
//
// 注意：讯飞星火对话的签名是 HMAC-SHA256 + WebSocket Authorization 头，与本文件算法不同，不可复用。

const (
	// XfyunASRSignatureHeader 讯飞 IFASR 签名请求头名称。
	XfyunASRSignatureHeader = "signature"

	// xfyunASRSignatureRandomLength 官方要求的 signatureRandom 长度：16 位大小写字母 + 数字。
	xfyunASRSignatureRandomLength = 16

	// xfyunASRSignatureRandomAlphabet signatureRandom 的字符集。
	xfyunASRSignatureRandomAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// xfyunASRDateTimeLayout 官方要求的请求时间格式，时区偏移不带冒号（东八区为 +0800）。
	xfyunASRDateTimeLayout = "2006-01-02T15:04:05-0700"

	// xfyunASRDateTimeOffset 讯飞 IFASR 使用的东八区偏移。
	xfyunASRDateTimeOffset = 8 * time.Hour
)

// XfyunURLEncode 按 RFC 3986 的 unreserved 规则对字符串做百分号编码：
// 字母、数字以及 _ . - ~ 原样保留，其余字符按 UTF-8 字节编码为大写十六进制的 %XX。
//
// 讯飞签名基础串与最终发出的查询串必须共用本函数：官方 Python 示例对两者都使用
// urllib.parse.quote 且 safe 字符集为空，若签名用 %20 而查询串用 + 编码空格，
// 上游会返回 100009（签名校验不通过）。
func XfyunURLEncode(value string) string {
	const hexDigits = "0123456789ABCDEF"

	var builder strings.Builder
	builder.Grow(len(value))
	for i := range len(value) {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '_', c == '.', c == '-', c == '~':
			builder.WriteByte(c)
		default:
			builder.WriteByte('%')
			builder.WriteByte(hexDigits[c>>4])
			builder.WriteByte(hexDigits[c&0x0F])
		}
	}
	return builder.String()
}

// xfyunASRSignPairs 返回参与签名与查询串的已编码键值对：
// 排除名为 signature 的参数、过滤空值参数、按参数名字典序排序。
func xfyunASRSignPairs(params url.Values) []string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key == XfyunASRSignatureHeader {
			continue
		}
		if params.Get(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, XfyunURLEncode(key)+"="+XfyunURLEncode(params.Get(key)))
	}
	return pairs
}

// xfyunASRSignBaseString 构造签名基础串：已编码键值对按参数名字典序用 & 连接。
func xfyunASRSignBaseString(params url.Values) string {
	return strings.Join(xfyunASRSignPairs(params), "&")
}

// XfyunASRSignature 计算一次讯飞 IFASR 请求的签名：baseString 以 apiSecret 为密钥做 HMAC-SHA1 后 Base64 编码。
//
// params 是该次请求的全部查询参数，密钥 apiSecret 显式传入；本函数无 IO、无全局状态。
// 同一参数名若出现多次，只取第一个值参与签名。
func XfyunASRSignature(params url.Values, apiSecret string) string {
	mac := hmac.New(sha1.New, []byte(apiSecret))
	mac.Write([]byte(xfyunASRSignBaseString(params)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// XfyunASRQueryString 构造请求的查询串：与签名基础串共用同一套参数筛选与 URL 编码。
func XfyunASRQueryString(params url.Values) string {
	return xfyunASRSignBaseString(params)
}

// XfyunASRBuildRequest 构造一次讯飞 IFASR 请求的 URL 与签名头值。
//
// endpoint 是不带查询串的接口地址（如 https://office-api-ist-dx.iflyaisol.com/v2/upload）。
// 返回的 signature 由调用方放入 XfyunASRSignatureHeader 请求头，签名本身不会出现在查询串中。
func XfyunASRBuildRequest(endpoint string, params url.Values, apiSecret string) (requestURL string, signature string) {
	signature = XfyunASRSignature(params, apiSecret)
	query := XfyunASRQueryString(params)
	if query == "" {
		return endpoint, signature
	}
	return endpoint + "?" + query, signature
}

// XfyunASRDateTime 生成官方要求的请求时间：yyyy-MM-dd'T'HH:mm:ss±HHmm，时区偏移为东八区 +0800。
// 上传与查询两次请求各自调用一次即可得到不同的 dateTime。
func XfyunASRDateTime(t time.Time) string {
	return t.In(time.FixedZone("UTC+8", int(xfyunASRDateTimeOffset/time.Second))).Format(xfyunASRDateTimeLayout)
}

// XfyunASRSignatureRandom 生成 16 位大小写字母 + 数字的随机串作为 signatureRandom。
// 上传与查询两次请求必须复用同一个值，调用方负责保存并复用。
func XfyunASRSignatureRandom() string {
	const maxUnbiased = 256 - (256 % len(xfyunASRSignatureRandomAlphabet))

	result := make([]byte, 0, xfyunASRSignatureRandomLength)
	buf := make([]byte, xfyunASRSignatureRandomLength*2)
	for len(result) < xfyunASRSignatureRandomLength {
		// crypto/rand.Read 自 Go 1.24 起不再返回错误，失败时直接 panic。
		_, _ = rand.Read(buf)
		for _, b := range buf {
			// 丢弃超出均匀分布边界的字节，避免取模带来的偏置。
			if int(b) >= maxUnbiased {
				continue
			}
			result = append(result, xfyunASRSignatureRandomAlphabet[int(b)%len(xfyunASRSignatureRandomAlphabet)])
			if len(result) == xfyunASRSignatureRandomLength {
				break
			}
		}
	}
	return string(result)
}
