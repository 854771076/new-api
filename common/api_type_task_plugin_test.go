package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPluginChannelHasNoOrdinaryAPIType(t *testing.T) {
	apiType, ok := ChannelType2APIType(constant.ChannelTypeTaskPlugin)
	assert.Equal(t, -1, apiType)
	assert.False(t, ok)
}

func TestAPIMartChannelTypeMapping(t *testing.T) {
	assert.Equal(t, 64, constant.ChannelTypeAPIMart)
	assert.Equal(t, 65, constant.ChannelTypeDummy)
	assert.Equal(t, 38, constant.APITypeAPIMart)
	assert.Equal(t, 39, constant.APITypeDummy)
	assert.Equal(t, "https://api.apimart.ai", constant.GetChannelBaseURL(constant.ChannelTypeAPIMart))

	apiType, ok := ChannelType2APIType(constant.ChannelTypeAPIMart)
	require.True(t, ok)
	assert.Equal(t, constant.APITypeAPIMart, apiType)

	endpointTypes := GetEndpointTypesByChannelType(constant.ChannelTypeAPIMart, "gpt-4o")
	assert.Contains(t, endpointTypes, constant.EndpointTypeOpenAI)
	assert.Contains(t, endpointTypes, constant.EndpointTypeOpenAIResponse)
}
