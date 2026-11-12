package minimax

import (
	"fmt"

	channelconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseUrl := info.ChannelBaseUrl
	if baseUrl == "" {
		baseUrl = channelconstant.GetChannelBaseURL(channelconstant.ChannelTypeMiniMax)
	}
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return fmt.Sprintf("%s/anthropic/v1/messages", info.ChannelBaseUrl), nil
	default:
		switch info.RelayMode {
		case constant.RelayModeChatCompletions:
			return fmt.Sprintf("%s/v1/text/chatcompletion_v2", baseUrl), nil
		case constant.RelayModeImagesGenerations:
			// 按上游模型名选端点：H3-Image 走新端点，其余走旧端点
			if isMiniMaxH3ImageModel(info.GetUpstreamModelName()) {
				return fmt.Sprintf("%s/v1/images/generations", baseUrl), nil
			}
			return fmt.Sprintf("%s/v1/image_generation", baseUrl), nil
		case constant.RelayModeAudioSpeech:
			return fmt.Sprintf("%s/v1/t2a_v2", baseUrl), nil
		case constant.RelayModeAudioVoiceClone:
			return fmt.Sprintf("%s/v1/voice_clone", baseUrl), nil
		case constant.RelayModeAudioVoiceDesign:
			return fmt.Sprintf("%s/v1/voice_design", baseUrl), nil
		case constant.RelayModeAudioMusic:
			return fmt.Sprintf("%s/v1/music_generation", baseUrl), nil
		default:
			return "", fmt.Errorf("unsupported relay mode: %d", info.RelayMode)
		}
	}
}
