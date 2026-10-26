package controller

import (
	"bytes"
	_ "embed"
	"mime/multipart"
)

// channelTestAudioSampleWAV 是渠道后台测试使用的音频夹具：16kHz、16bit、单声道、2 秒。
//
// 夹具字节通过 go:embed 编进二进制，部署环境里没有仓库源码目录也能取用。
// 夹具由 scripts/tools/gen-audio-fixture 合成生成，不含任何真实录音。
//
//go:embed audiofixture/channel-test-sample.wav
var channelTestAudioSampleWAV []byte

// channelTestAudioFileName 是夹具在 multipart 表单里的文件名，上游据此识别音频格式。
const channelTestAudioFileName = "channel-test-sample.wav"

// channelTestAudioTranscriptionModel 是音频转写端点使用的固定转写模型名。
const channelTestAudioTranscriptionModel = "ifasr-llm-v1"

// buildChannelTestAudioMultipart 构造音频转写测试用的 multipart 请求体。
//
// 音频转写需要携带音频文件，无法用 JSON 请求体表达，因此单独走 multipart 管道。
func buildChannelTestAudioMultipart(model string) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("model", model); err != nil {
		return nil, "", err
	}
	part, err := writer.CreateFormFile("file", channelTestAudioFileName)
	if err != nil {
		return nil, "", err
	}
	if _, err = part.Write(channelTestAudioSampleWAV); err != nil {
		return nil, "", err
	}
	if err = writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}
