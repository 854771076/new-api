package constant

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPath2RelayMode(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{path: "/v1/alpha/search", want: RelayModeAlphaSearch},
		{path: "/v1/alpha/search?foo=1", want: RelayModeAlphaSearch},
		{path: "/v1/tools/search", want: RelayModeToolsSearch},
		{path: "/v1/tools/fetch", want: RelayModeToolsFetch},
		{path: "/v1/audio/voice/clone", want: RelayModeAudioVoiceClone},
		{path: "/v1/audio/voice/design", want: RelayModeAudioVoiceDesign},
		{path: "/v1/audio/music", want: RelayModeAudioMusic},
		{path: "/v1/audio/speech", want: RelayModeAudioSpeech},
		{path: "/v1/audio/transcriptions", want: RelayModeAudioTranscription},
		{path: "/v1/audio/translations", want: RelayModeAudioTranslation},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, Path2RelayMode(tt.path))
		})
	}
}
