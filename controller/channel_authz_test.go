package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelHasSensitiveChanges(t *testing.T) {
	baseURL := "https://api.example.com"
	headerOverride := `{"Authorization":"Bearer {api_key}"}`
	origin := &model.Channel{
		Type:           1,
		Key:            "old-key",
		BaseURL:        &baseURL,
		HeaderOverride: &headerOverride,
		Models:         "gpt-4o",
		Group:          "default",
	}

	t.Run("non-sensitive routing fields", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Models = "gpt-4o,gpt-4o-mini"
		updated.Group = "vip"

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
			"models": updated.Models,
			"group":  updated.Group,
		}))
	})

	t.Run("key change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Key = "new-key"

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"key": updated.Key}))
	})

	t.Run("base url change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		newBaseURL := "https://leak.example.com"
		updated.BaseURL = &newBaseURL

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"base_url": newBaseURL}))
	})

	t.Run("header override change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		newHeaderOverride := `{"X-Key":"{api_key}"}`
		updated.HeaderOverride = &newHeaderOverride

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"header_override": newHeaderOverride}))
	})

	t.Run("omitted sensitive fields do not use zero values", func(t *testing.T) {
		updated := PatchChannel{}
		updated.Id = origin.Id
		updated.Priority = origin.Priority

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"priority": 10}))
	})

	t.Run("unknown field fails closed", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"future_secret_field": "x"}))
	})

	t.Run("status is operational", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Status = common.ChannelStatusManuallyDisabled

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"status": updated.Status}))
	})

	t.Run("read-only fields are ignored by sensitivity check", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Balance = 99
		updated.UsedQuota = 100
		updated.ResponseTime = 200

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
			"balance":       updated.Balance,
			"used_quota":    updated.UsedQuota,
			"response_time": updated.ResponseTime,
		}))
	})

	// concurrency_limit is a routing/scheduling parameter (CHN-01 C-4): a
	// ChannelWrite admin must be able to edit it without ChannelSensitiveWrite.
	// Before classification the fail-closed branch treats it as sensitive and
	// this subtest fails — the red input for 02-09 Task 02.
	t.Run("concurrency limit change is non-sensitive", func(t *testing.T) {
		limit := 5
		updated := PatchChannel{Channel: *origin}
		updated.ConcurrencyLimit = &limit

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"concurrency_limit": 5}))
	})
}

func TestClearChannelReadOnlyFields(t *testing.T) {
	channel := PatchChannel{Channel: model.Channel{
		CreatedTime:        11,
		TestTime:           22,
		ResponseTime:       33,
		Balance:            44.5,
		BalanceUpdatedTime: 55,
		UsedQuota:          66,
		Models:             "gpt-4o",
		Group:              "default",
	}}

	clearChannelReadOnlyFields(&channel, map[string]any{
		"created_time":         channel.CreatedTime,
		"test_time":            channel.TestTime,
		"response_time":        channel.ResponseTime,
		"balance":              channel.Balance,
		"balance_updated_time": channel.BalanceUpdatedTime,
		"used_quota":           channel.UsedQuota,
		"models":               channel.Models,
		"group":                channel.Group,
	})

	assert.Zero(t, channel.CreatedTime)
	assert.Zero(t, channel.TestTime)
	assert.Zero(t, channel.ResponseTime)
	assert.Zero(t, channel.Balance)
	assert.Zero(t, channel.BalanceUpdatedTime)
	assert.Zero(t, channel.UsedQuota)
	assert.Equal(t, "gpt-4o", channel.Models)
	assert.Equal(t, "default", channel.Group)
}

func TestUpdateChannelRejectsStatusField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/channel/",
		bytes.NewBufferString(`{"id":1,"status":2}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	UpdateChannel(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
}

func TestChannelStatusValidation(t *testing.T) {
	assert.True(t, isManageableChannelStatus(common.ChannelStatusEnabled))
	assert.True(t, isManageableChannelStatus(common.ChannelStatusManuallyDisabled))
	assert.False(t, isManageableChannelStatus(common.ChannelStatusAutoDisabled))
	assert.False(t, isManageableChannelStatus(0))
}

// TestChannelFieldsAreClassified guards the fail-closed sensitivity check: every
// JSON field of PatchChannel (including the embedded model.Channel) must be listed
// in channelSensitiveFields, channelNonSensitiveFields, or
// channelOperationalFields. A newly added field that is left unclassified will
// fail this test, forcing a conscious permission decision instead of silently
// defaulting either way.
func TestChannelFieldsAreClassified(t *testing.T) {
	classified := func(name string) bool {
		if _, ok := channelSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelNonSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelOperationalFields[name]; ok {
			return true
		}
		_, ok := channelReadOnlyFields[name]
		return ok
	}

	var collect func(rt reflect.Type) []string
	collect = func(rt reflect.Type) []string {
		var names []string
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				names = append(names, collect(field.Type)...)
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			names = append(names, name)
		}
		return names
	}

	for _, name := range collect(reflect.TypeFor[PatchChannel]()) {
		assert.Truef(t, classified(name),
			"channel field %q is not classified; add it to channelSensitiveFields, channelNonSensitiveFields, channelOperationalFields, or channelReadOnlyFields in channel_authz.go", name)
	}
}

// TestUpdateChannelConcurrencyLimitAdmission pins the CHN-01 write-path
// admission contract through the real UpdateChannel handler with a non-root
// admin context (the fixture's default policies never grant
// ChannelSensitiveWrite, so the fail-closed classification is the only thing
// that lets the update through). Three states: explicit 5 persists and echoes
// in data; an omitted field keeps the stored value; explicit 0 (unlimited)
// overwrites it. Before "concurrency_limit" is classified as non-sensitive the
// explicit states are rejected as a sensitive change — the red input for
// 02-09 Task 02.
func TestUpdateChannelConcurrencyLimitAdmission(t *testing.T) {
	setupTaskPluginBindChannelTest(t)

	channel := model.Channel{
		Id: 71, Type: 1, Name: "admission-channel", Key: "sk-admission",
		Models: "gpt-4o", Group: "default", Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&channel).Error)

	storedLimit := func() int {
		var stored int
		require.NoError(t, model.DB.Raw("SELECT concurrency_limit FROM channels WHERE id = ?", channel.Id).Scan(&stored).Error)
		return stored
	}
	put := func(body string) struct {
		Success bool          `json:"success"`
		Message string        `json:"message"`
		Data    model.Channel `json:"data"`
	} {
		recorder := putUpdateChannel(t, 2, common.RoleAdminUser, body)
		require.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Success bool          `json:"success"`
			Message string        `json:"message"`
			Data    model.Channel `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.Truef(t, response.Success, "update rejected: %s", response.Message)
		return response
	}

	t.Run("explicit positive limit persists and echoes in response", func(t *testing.T) {
		explicit := put(`{"id":71,"concurrency_limit":5}`)
		assert.Equal(t, 5, storedLimit(), "explicit concurrency_limit=5 must persist")
		require.NotNil(t, explicit.Data.ConcurrencyLimit)
		assert.Equal(t, 5, *explicit.Data.ConcurrencyLimit, "response data must echo the stored limit")
	})

	t.Run("omitted payload keeps stored limit", func(t *testing.T) {
		omitted := put(`{"id":71}`)
		assert.Equal(t, 5, storedLimit(), "omitted concurrency_limit must keep the stored value")
		require.NotNil(t, omitted.Data.ConcurrencyLimit)
		assert.Equal(t, 5, *omitted.Data.ConcurrencyLimit, "response data must echo the preserved limit")
	})

	t.Run("explicit zero clears to unlimited", func(t *testing.T) {
		cleared := put(`{"id":71,"concurrency_limit":0}`)
		assert.Equal(t, 0, storedLimit(), "explicit concurrency_limit=0 (unlimited) must overwrite 5")
		require.NotNil(t, cleared.Data.ConcurrencyLimit)
		assert.Equal(t, 0, *cleared.Data.ConcurrencyLimit, "response data must echo the cleared limit")
	})

	// Creation binds model.Channel directly (AddChannelRequest.Channel), so the
	// *int field needs no admission code: explicit values persist and an omitted
	// field falls through to the column default 0.
	createBody := `{"mode":"single","channel":{"type":1,"name":"%s","key":"sk-created","models":"gpt-4o","group":"default"%s}}`

	t.Run("add channel persists explicit limit", func(t *testing.T) {
		created := postAddChannel(t, 2, common.RoleAdminUser, fmt.Sprintf(createBody, "admission-created", `,"concurrency_limit":7`))
		require.Contains(t, created.Body.String(), `"success":true`)
		var explicitCreate model.Channel
		require.NoError(t, model.DB.Where("name = ?", "admission-created").First(&explicitCreate).Error)
		assert.Equal(t, 7, explicitCreate.GetConcurrencyLimit(), "AddChannel must persist an explicit concurrency_limit")
	})

	t.Run("add channel omitted limit defaults to zero", func(t *testing.T) {
		defaulted := postAddChannel(t, 2, common.RoleAdminUser, fmt.Sprintf(createBody, "admission-defaulted", ""))
		require.Contains(t, defaulted.Body.String(), `"success":true`)
		var defaultedCreate model.Channel
		require.NoError(t, model.DB.Where("name = ?", "admission-defaulted").First(&defaultedCreate).Error)
		assert.Equal(t, 0, defaultedCreate.GetConcurrencyLimit(), "omitted concurrency_limit must default to 0 (unlimited)")
	})
}
