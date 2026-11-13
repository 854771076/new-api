package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveWaffoPancakeReturnURL(t *testing.T) {
	const appID = 7001

	require.NoError(t, model.DB.AutoMigrate(&model.OAuthApp{}))
	t.Cleanup(func() { model.DB.Delete(&model.OAuthApp{}, appID) })

	old := setting.WaffoPancakeReturnURL
	setting.WaffoPancakeReturnURL = "https://global.example/success"
	t.Cleanup(func() { setting.WaffoPancakeReturnURL = old })

	require.NoError(t, model.DB.Create(&model.OAuthApp{
		Id:               appID,
		Name:             "return-url-test",
		ClientID:         "nac_return_url_test",
		ClientSecretHash: "test",
		Status:           model.OAuthAppStatusEnabled,
	}).Error)

	setAppReturnURL := func(url string) {
		t.Helper()
		require.NoError(t, model.DB.Model(&model.OAuthApp{}).
			Where("id = ?", appID).
			Update("waffo_pancake_return_url", url).Error)
	}

	t.Run("app override wins over global", func(t *testing.T) {
		setAppReturnURL("https://app.example/success")
		assert.Equal(t, "https://app.example/success", ResolveWaffoPancakeReturnURL(appID))
	})

	t.Run("blank app url falls back to global", func(t *testing.T) {
		setAppReturnURL("  ")
		assert.Equal(t, setting.WaffoPancakeReturnURL, ResolveWaffoPancakeReturnURL(appID))
	})

	t.Run("missing app falls back to global", func(t *testing.T) {
		assert.Equal(t, setting.WaffoPancakeReturnURL, ResolveWaffoPancakeReturnURL(999999))
	})

	t.Run("non-app payment uses global", func(t *testing.T) {
		assert.Equal(t, setting.WaffoPancakeReturnURL, ResolveWaffoPancakeReturnURL(0))
		assert.Equal(t, setting.WaffoPancakeReturnURL, ResolveWaffoPancakeReturnURL(-1))
	})
}
