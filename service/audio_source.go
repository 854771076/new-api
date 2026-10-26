package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// audioSourceContextKey 是归一后的音频来源在 gin 上下文中的缓存键。
const audioSourceContextKey = "relay_audio_source"

// durationSupportedExts 是本地纯 Go 时长解析器可解析的音频扩展名。
// 与 common.GetAudioDuration 的解析器能力保持一致；不含 webm（该分支恒返回错误）。
var durationSupportedExts = []string{
	".mp3", ".wav", ".flac", ".m4a", ".mp4",
	".ogg", ".oga", ".opus",
	".aiff", ".aif", ".aifc", ".aac",
}

// audioMimeFormatMap 把常见的 MIME 子类型映射为上游可识别的音频格式。
var audioMimeFormatMap = map[string]string{
	"mpeg":     "mp3",
	"mp3":      "mp3",
	"x-mpeg":   "mp3",
	"wav":      "wav",
	"x-wav":    "wav",
	"wave":     "wav",
	"vnd.wave": "wav",
	"flac":     "flac",
	"x-flac":   "flac",
	"m4a":      "m4a",
	"x-m4a":    "m4a",
	"mp4":      "m4a",
	"x-mp4":    "m4a",
	"ogg":      "ogg",
	"opus":     "opus",
	"aiff":     "aiff",
	"x-aiff":   "aiff",
	"aac":      "aac",
	"aacp":     "aac",
	"webm":     "webm",
}

// AudioSource 是归一后的音频来源。
type AudioSource struct {
	Data       []byte // 音频原始字节
	Format     string // 音频格式, 小写不含点, 如 "wav" / "mp3"
	FileName   string // 带扩展名的文件名, 如 "audio.wav"
	DurationMs int64  // 音频时长(毫秒)
}

// audioSourceResult 缓存一次解析的结果，失败结果同样缓存，避免重复下载。
type audioSourceResult struct {
	source *AudioSource
	err    error
}

// ResolveAudioSource 从请求中解析音频来源并归一化。
// 优先级: multipart file > Base64 > URL。
// 同一请求内多次调用只解析一次，后续调用复用缓存结果，避免 URL 来源被重复下载。
func ResolveAudioSource(c *gin.Context, req *dto.AudioRequest) (*AudioSource, error) {
	if c != nil {
		if cached, ok := c.Get(audioSourceContextKey); ok {
			if result, ok := cached.(*audioSourceResult); ok && result != nil {
				return result.source, result.err
			}
		}
	}

	var source *AudioSource
	var err error

	// multipart 上传优先；请求不是 multipart 时该入口会对非表单请求体报错。
	isMultipart := c != nil && c.Request != nil &&
		strings.HasPrefix(strings.ToLower(c.Request.Header.Get("Content-Type")), "multipart/form-data")
	if isMultipart {
		var found bool
		source, found, err = resolveMultipartAudioSource(c)
		if err == nil && !found {
			source = nil // multipart 里没有 file 字段，回退到其它来源
		}
	}

	if source == nil && err == nil {
		switch {
		case req != nil && strings.TrimSpace(req.AudioBase64) != "":
			source, err = resolveBase64AudioSource(c, req)
		case req != nil && strings.TrimSpace(req.AudioURL) != "":
			source, err = resolveURLAudioSource(c, req.AudioURL)
		default:
			err = fmt.Errorf("未提供音频：请通过 multipart file 字段、audio_base64 或 audio_url 提交音频")
		}
	}

	if c != nil {
		c.Set(audioSourceContextKey, &audioSourceResult{source: source, err: err})
	}
	return source, err
}

// resolveMultipartAudioSource 解析 multipart 请求中的 file 字段。
// found 为 false 表示请求没有 file 字段，交由其它来源处理。
func resolveMultipartAudioSource(c *gin.Context) (*AudioSource, bool, error) {
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, false, fmt.Errorf("解析 multipart 表单失败: %w", err)
	}
	defer form.RemoveAll()

	headers := form.File["file"]
	if len(headers) == 0 {
		return nil, false, nil
	}
	fileHeader := headers[0]
	file, err := fileHeader.Open()
	if err != nil {
		return nil, false, fmt.Errorf("读取上传的音频文件失败: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, false, fmt.Errorf("读取上传的音频文件失败: %w", err)
	}

	// 讯飞据扩展名判定音频格式，上传文件名保留原始扩展名。
	format := strings.TrimPrefix(strings.ToLower(path.Ext(fileHeader.Filename)), ".")
	if format == "" {
		return nil, false, fmt.Errorf("上传的音频文件 %q 缺少扩展名，无法识别音频格式", fileHeader.Filename)
	}

	durationMs, err := audioDurationMs(c, data, format)
	if err != nil {
		return nil, false, err
	}

	// 浏览器可能带上路径前缀，只保留文件名部分。
	fileName := fileHeader.Filename
	if index := strings.LastIndexAny(fileName, `/\`); index >= 0 {
		fileName = fileName[index+1:]
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		fileName = "audio." + format
	}

	return &AudioSource{
		Data:       data,
		Format:     format,
		FileName:   fileName,
		DurationMs: durationMs,
	}, true, nil
}

// resolveBase64AudioSource 解析请求中的 Base64 音频。
// data URI 前缀里的 MIME 子类型用于推断格式，裸 Base64 需要由 audio_format 显式指定格式。
func resolveBase64AudioSource(c *gin.Context, req *dto.AudioRequest) (*AudioSource, error) {
	payload := strings.TrimSpace(req.AudioBase64)
	format := ""
	if strings.HasPrefix(strings.ToLower(payload), "data:") {
		separator := strings.Index(payload, ",")
		if separator < 0 {
			return nil, fmt.Errorf("Base64 音频的 data URI 前缀缺少分隔符")
		}
		format = audioFormatFromDataURI(payload[:separator])
		payload = payload[separator+1:]
	}
	if format == "" {
		format = normalizeAudioFormat(req.AudioFormat)
	}
	if format == "" {
		return nil, fmt.Errorf("未提供音频格式：裸 Base64 音频需要通过 audio_format 指定格式")
	}

	data, err := decodeAudioBase64(payload)
	if err != nil {
		return nil, err
	}

	durationMs, err := audioDurationMs(c, data, format)
	if err != nil {
		return nil, err
	}

	return &AudioSource{
		Data:       data,
		Format:     format,
		FileName:   "audio." + format,
		DurationMs: durationMs,
	}, nil
}

// resolveURLAudioSource 下载远程音频并归一化。
// 下载走既有带 SSRF 防护的入口，字节体积上限由本函数自行约束。
func resolveURLAudioSource(c *gin.Context, rawURL string) (*AudioSource, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("音频 URL 无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("音频 URL 只支持 http/https，当前为 %q", parsed.Scheme)
	}

	resp, err := DoDownloadRequest(parsed.String(), "audio transcription")
	if err != nil {
		return nil, fmt.Errorf("下载音频失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载音频失败：上游返回 HTTP %d", resp.StatusCode)
	}

	// 既有下载入口不限制响应体大小，这里自行加上限，避免大文件被整块读入内存。
	limitMB := constant.MaxFileDownloadMB
	if limitMB <= 0 {
		limitMB = 32
	}
	maxBytes := int64(limitMB) << 20
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("音频文件大小 %d 字节超过上限 %d 字节", resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取音频数据失败: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("音频文件大小超过上限 %d 字节", maxBytes)
	}

	// 格式推导：优先取 URL 路径扩展名，其次取响应 Content-Type 子类型。
	// URL 没有原始文件名需要保留，扩展名可以按别名归一（如 .mpeg -> mp3）。
	format := normalizeAudioFormat(path.Ext(parsed.Path))
	if format == "" {
		format = audioFormatFromContentType(resp.Header.Get("Content-Type"))
	}
	if format == "" {
		return nil, fmt.Errorf("无法推断音频格式：请使用带扩展名的音频 URL")
	}

	durationMs, err := audioDurationMs(c, data, format)
	if err != nil {
		return nil, err
	}

	return &AudioSource{
		Data:       data,
		Format:     format,
		FileName:   "audio." + format,
		DurationMs: durationMs,
	}, nil
}

// audioDurationMs 解析音频时长并换算为毫秒。
// 时长解析失败一律报错，不按 0 时长静默继续。
func audioDurationMs(c *gin.Context, data []byte, format string) (int64, error) {
	ext := "." + strings.ToLower(format)
	if !slices.Contains(durationSupportedExts, ext) {
		return 0, fmt.Errorf("音频格式 %q 不受支持：本地时长解析器仅支持 %s", format, strings.Join(durationSupportedExts, "、"))
	}

	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	seconds, err := common.GetAudioDuration(ctx, bytes.NewReader(data), ext)
	if err != nil {
		return 0, fmt.Errorf("解析音频时长失败: %w", err)
	}
	return int64(math.Round(seconds * 1000)), nil
}

// audioFormatFromDataURI 从 data URI 前缀（形如 data:audio/wav;base64）取 MIME 子类型推出音频格式。
func audioFormatFromDataURI(prefix string) string {
	meta := strings.TrimPrefix(strings.ToLower(prefix), "data:")
	if index := strings.Index(meta, ";"); index >= 0 {
		meta = meta[:index]
	}
	return normalizeAudioFormat(meta)
}

// audioFormatFromContentType 从响应的 Content-Type 子类型推出音频格式。
func audioFormatFromContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = contentType
	}
	return normalizeAudioFormat(mediaType)
}

// normalizeAudioFormat 把格式名或 MIME 子类型归一为小写不含点的音频格式。
func normalizeAudioFormat(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	if index := strings.Index(value, ";"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if index := strings.Index(value, "/"); index >= 0 {
		value = value[index+1:]
	}
	value = strings.TrimPrefix(value, ".")
	if mapped, ok := audioMimeFormatMap[value]; ok {
		return mapped
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// decodeAudioBase64 解码 Base64 载荷，容忍 URL-safe 变体与缺失的 padding。
func decodeAudioBase64(payload string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, payload)
	cleaned = strings.NewReplacer("-", "+", "_", "/").Replace(cleaned)

	switch len(cleaned) % 4 {
	case 1:
		return nil, fmt.Errorf("Base64 音频数据非法：长度不合法")
	case 2:
		cleaned += "=="
	case 3:
		cleaned += "="
	}

	data, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("Base64 音频数据解码失败: %w", err)
	}
	return data, nil
}
