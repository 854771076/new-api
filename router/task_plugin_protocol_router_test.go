package router

import (
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostProtocolRegistryDrivesProtocolRoutesOnce(t *testing.T) {
	engine := gin.New()
	SetTaskPluginProtocolRouter(engine)

	expected := []string{
		"POST /v1/responses",
		"GET /v1/responses/:response_id",
		"POST /v1/videos",
		"GET /v1/videos/:task_id",
		"GET /v1/videos/:task_id/content",
		"HEAD /v1/videos/:task_id/content",
		"POST /v1/audio/transcriptions/tasks",
		"GET /v1/audio/transcriptions/tasks/:task_id",
		"POST /v1/images/generations",
		"POST /v1/images/edits",
	}
	actual := make([]string, 0, len(engine.Routes()))
	for _, route := range engine.Routes() {
		actual = append(actual, fmt.Sprintf("%s %s", route.Method, route.Path))
	}
	sort.Strings(expected)
	sort.Strings(actual)
	assert.Equal(t, expected, actual)
}

func TestTaskPluginProtocolHandlersResolveAudioTaskOperations(t *testing.T) {
	for _, operation := range []string{"create", "retrieve"} {
		handlers, err := taskPluginProtocolHandlers("openai_audio", operation)
		require.NoError(t, err, operation)
		require.NotEmpty(t, handlers, operation)
	}
	_, err := taskPluginProtocolHandlers("openai_audio", "content")
	require.ErrorContains(t, err, "has no handler")
}

// The asynchronous audio task endpoints add a fourth path segment, so they never
// collide with the synchronous POST /v1/audio/transcriptions route.
func TestOpenAIAudioTaskRoutesDoNotIntersectSynchronousTranscription(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions/tasks", "/v1/audio/transcriptions/tasks/:task_id"} {
		assert.False(t, routePatternsIntersect(path, "/v1/audio/transcriptions"), path)
	}

	engine := gin.New()
	engine.POST("/v1/audio/transcriptions", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	assert.NotPanics(t, func() { SetTaskPluginProtocolRouter(engine) })
}
