package common

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// minimalWAV builds a valid 16-bit PCM mono WAV with the given number of sample frames.
func minimalWAV(sampleRate uint32, frames int) []byte {
	const numChannels = 1
	const bitsPerSample = 16
	dataSize := frames * numChannels * (bitsPerSample / 8)
	buf := &bytes.Buffer{}
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(buf, binary.LittleEndian, sampleRate)
	byteRate := sampleRate * numChannels * (bitsPerSample / 8)
	binary.Write(buf, binary.LittleEndian, byteRate)
	binary.Write(buf, binary.LittleEndian, uint16(numChannels*(bitsPerSample/8)))
	binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

func TestGetAudioDurationCaseInsensitiveExt(t *testing.T) {
	wav := minimalWAV(8000, 8000) // 1 second

	for _, ext := range []string{".wav", ".WAV", ".Wav"} {
		d, err := GetAudioDuration(context.Background(), bytes.NewReader(wav), ext)
		if err != nil {
			t.Fatalf("ext %q: unexpected error: %v", ext, err)
		}
		if d < 0.99 || d > 1.01 {
			t.Fatalf("ext %q: expected ~1s duration, got %v", ext, d)
		}
	}
}

func TestGetAudioDurationUppercaseNotUnsupported(t *testing.T) {
	_, err := GetAudioDuration(context.Background(), bytes.NewReader([]byte("not audio")), ".MP3")
	if err != nil && strings.Contains(err.Error(), "unsupported audio format") {
		t.Fatalf("uppercase extension rejected as unsupported: %v", err)
	}
}

// TestSupportedAudioDurationExtensionsAllParse 扩展名表与解析分支必须同步：
// 表里列出的格式若没有解析分支，宿主会把可测的音频当成不可测，按上限预扣。
func TestSupportedAudioDurationExtensionsAllParse(t *testing.T) {
	wav := minimalWAV(8000, 800)
	for _, ext := range SupportedAudioDurationExtensions() {
		_, err := GetAudioDuration(context.Background(), bytes.NewReader(wav), ext)
		if err == nil {
			continue
		}
		assert.NotContains(t, err.Error(), "unsupported audio format",
			"extension %s is listed as measurable but has no parser", ext)
	}
}

func TestAudioExtensionForMediaType(t *testing.T) {
	for mediaType, want := range map[string]string{
		"audio/mpeg":               ".mp3",
		"AUDIO/WAV":                ".wav",
		"audio/ogg; codecs=vorbis": ".ogg",
		"audio/opus":               ".opus",
	} {
		assert.Equal(t, want, AudioExtensionForMediaType(mediaType), mediaType)
	}
	for _, mediaType := range []string{"", "video/webm", "application/octet-stream"} {
		assert.Empty(t, AudioExtensionForMediaType(mediaType), mediaType)
	}
}
