package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogOtherScopesAndMerges(t *testing.T) {
	var other LogOther

	assert.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	other.MergePublic(map[string]any{
		"zero": 0,
	})
	assert.True(t, other.SetAdmin("use_channel", []string{"channel-a"}))
	other.MergeAdmin(map[string]any{
		"rejected": false,
	})
	assert.True(t, other.SetRoot("upstream_request_id", "upstream-private"))
	other.MergeRoot(map[string]any{
		"generation": 0,
	})
	assert.True(t, other.SetAudit("method", "POST"))
	other.MergeAudit(map[string]any{
		"success": false,
	})

	require.JSONEq(t, `{
		"request_path": "/v1/chat/completions",
		"zero": 0,
		"admin_info": {
			"use_channel": ["channel-a"],
			"rejected": false
		},
		"root_info": {
			"upstream_request_id": "upstream-private",
			"generation": 0
		},
		"audit_info": {
			"method": "POST",
			"success": false
		}
	}`, other.JSONString())
}

func TestLogOtherRejectsSensitivePublicFields(t *testing.T) {
	other := NewLogOther()

	for _, key := range []string{
		"admin_info",
		"root_info",
		"audit_info",
		"channel_id",
		"channel_name",
		"channel_type",
		"reject_reason",
	} {
		assert.False(t, other.SetPublic(key, "must-not-leak"), key)
	}
	other.MergePublic(map[string]any{
		"request_path": "/v1/responses",
		"channel_name": "still-must-not-leak",
		"admin_info":   map[string]any{"secret": true},
	})

	require.JSONEq(t, `{"request_path":"/v1/responses"}`, other.JSONString())
	require.JSONEq(t, `{}`, NewLogOther().JSONString())
}

func TestLogOtherJSONStringDoesNotMutateReceiver(t *testing.T) {
	other := NewLogOther()
	require.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	require.True(t, other.SetAdmin("rejected", false))

	before := other.Snapshot()
	first := other.JSONString()
	after := other.Snapshot()
	second := other.JSONString()

	require.Equal(t, before, after)
	require.Equal(t, first, second)
}

// 分段耗时属于内部运维信息，必须落在 admin_info：用户视图整段被裁掉，
// 管理员视图保留 admin_info.timing。
func TestLogOtherAdminTimingStaysAdminOnly(t *testing.T) {
	other := NewLogOther()
	require.True(t, other.SetAdmin("timing", map[string]any{
		"queue_wait_ms":             int64(120),
		"channel_select_ms":         int64(3),
		"upstream_header_ms":        int64(1020000),
		"upstream_body_ms":          int64(0),
		"upstream_first_byte_ms":    int64(250),
		"upstream_stream_ms":        int64(0),
		"settle_ms":                 int64(8),
		"attempts":                  int64(2),
		"upstream_header_timeout_s": int64(1800),
		"streaming_timeout_s":       int64(300),
		"relay_timeout_s":           int64(0),
	}))

	raw := other.JSONString()
	require.JSONEq(t, `{
		"admin_info": {
			"timing": {
				"queue_wait_ms": 120,
				"channel_select_ms": 3,
				"upstream_header_ms": 1020000,
				"upstream_body_ms": 0,
				"upstream_first_byte_ms": 250,
				"upstream_stream_ms": 0,
				"settle_ms": 8,
				"attempts": 2,
				"upstream_header_timeout_s": 1800,
				"streaming_timeout_s": 300,
				"relay_timeout_s": 0
			}
		}
	}`, raw)

	userView := formatLogOtherJSON(raw, logOtherVisibilityUser)
	require.JSONEq(t, `{}`, userView)

	adminView := formatLogOtherJSON(raw, logOtherVisibilityAdmin)
	require.JSONEq(t, raw, adminView)
}
