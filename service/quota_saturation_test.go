package service

import (
	"math"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestAttachQuotaSaturationNestsUnderAdminInfo verifies the saturation marker
// is nested under other.admin_info.quota_saturation so it is admin-only (the
// log formatter strips admin_info for non-admin viewers).
func TestAttachQuotaSaturationNestsUnderAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	relayInfo := &relaycommon.RelayInfo{
		UserId:          7,
		OriginModelName: "gpt-image-1",
		QuotaClamp: &common.QuotaClamp{
			Op:       "QuotaFromDecimal",
			Kind:     common.QuotaClampOverflow,
			Original: 1.8e19,
			Clamped:  common.MaxQuota,
		},
	}

	other := model.NewLogOther()
	other.SetPublic("model_price", 0.004)
	attachQuotaSaturation(ctx, relayInfo, other)

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok, "admin_info should be created")
	sat, ok := adminInfo["quota_saturation"].(map[string]any)
	require.True(t, ok, "quota_saturation should be nested under admin_info")
	require.Equal(t, "QuotaFromDecimal", sat["op"])
	require.Equal(t, common.QuotaClampOverflow, sat["kind"])
	require.Equal(t, common.MaxQuota, sat["clamped"])
}

func TestCalcViolationFeeQuotaSaturates(t *testing.T) {
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	require.Equal(t, common.MaxQuota, calcViolationFeeQuota(1e20, 1))
}

func TestCalcOpenRouterCacheCreateTokensDoesNotWrap(t *testing.T) {
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	got := CalcOpenRouterCacheCreateTokens(dto.Usage{Cost: math.Inf(1)}, hosttypes.PriceData{
		ModelRatio:         1,
		CacheCreationRatio: 2,
		CacheRatio:         1,
		CompletionRatio:    1,
	})
	require.Equal(t, -1, got)
}

// TestAttachQuotaSaturationPreservesExistingAdminInfo verifies the marker is
// merged into a pre-existing admin_info map without clobbering it.
func TestAttachQuotaSaturationPreservesExistingAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	relayInfo := &relaycommon.RelayInfo{
		QuotaClamp: &common.QuotaClamp{Op: "QuotaFromFloat", Kind: common.QuotaClampUnderflow, Clamped: common.MinQuota},
	}
	other := model.NewLogOther()
	other.SetAdmin("admin_username", "root")
	attachQuotaSaturation(ctx, relayInfo, other)

	adminInfo := other.Snapshot()["admin_info"].(map[string]any)
	require.Equal(t, "root", adminInfo["admin_username"], "existing admin_info fields preserved")
	require.NotNil(t, adminInfo["quota_saturation"])
}

// TestAttachQuotaSaturationNoClampNoMarker verifies the common case (no
// saturation) leaves the log untouched.
func TestAttachQuotaSaturationNoClampNoMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	relayInfo := &relaycommon.RelayInfo{QuotaClamp: nil}
	other := model.NewLogOther()
	other.SetPublic("model_price", 0.004)
	attachQuotaSaturation(ctx, relayInfo, other)

	_, hasAdmin := other.Snapshot()["admin_info"]
	require.False(t, hasAdmin, "no admin_info should be added when there is no clamp")
}

func TestPreConsumeBillingRejectsSaturatedQuotaBeforeDeduction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		QuotaClamp: &common.QuotaClamp{
			Op:       "QuotaFromFloat",
			Kind:     common.QuotaClampOverflow,
			Original: 1e30,
			Clamped:  common.MaxQuota,
		},
	}

	apiErr := PreConsumeBilling(c, common.MaxQuota, info)

	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeModelPriceError, apiErr.GetErrorCode())
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Same(t, info.QuotaClamp, apiErr.Err)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, apiErr, &clamp)
	require.Same(t, info.QuotaClamp, clamp)
	require.Nil(t, info.Billing)
}

func TestPreConsumeBillingRejectsNegativeQuotaBeforeDeduction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{}

	apiErr := PreConsumeBilling(c, -1, info)

	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeModelPriceError, apiErr.GetErrorCode())
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Nil(t, info.Billing)
}

// TestNoteQuotaClampReachesAdminInfo verifies the exported seam used by channel
// adaptors: a saturation event registered through it lands in the consume log's
// admin_info.quota_saturation during settlement.
func TestNoteQuotaClampReachesAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	relayInfo := &relaycommon.RelayInfo{UserId: 7, OriginModelName: "spark-asr"}
	NoteQuotaClamp(relayInfo, &common.QuotaClamp{
		Op:       "QuotaRound",
		Kind:     common.QuotaClampOverflow,
		Original: 1e16,
		Clamped:  common.MaxQuota,
	})

	other := model.NewLogOther()
	attachQuotaSaturation(ctx, relayInfo, other)

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok, "admin_info should be created")
	sat, ok := adminInfo["quota_saturation"].(map[string]any)
	require.True(t, ok, "quota_saturation should be nested under admin_info")
	require.Equal(t, "QuotaRound", sat["op"])
	require.Equal(t, common.MaxQuota, sat["clamped"])
}

// TestNoteQuotaClampKeepsFirstClamp verifies the "first clamp wins" guard also
// applies to the exported seam.
func TestNoteQuotaClampKeepsFirstClamp(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{}
	first := &common.QuotaClamp{Op: "QuotaRound", Kind: common.QuotaClampOverflow, Clamped: common.MaxQuota}

	NoteQuotaClamp(relayInfo, first)
	NoteQuotaClamp(relayInfo, &common.QuotaClamp{Op: "QuotaFromDecimal", Kind: common.QuotaClampOverflow, Clamped: 1})

	require.Same(t, first, relayInfo.QuotaClamp)
}

// TestAudioQuotaSaturationReachesAdminInfo verifies the audio settlement path's
// own quota conversion saturation is recorded and attached to the log.
func TestAudioQuotaSaturationReachesAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	relayInfo := &relaycommon.RelayInfo{UserId: 7, OriginModelName: "spark-asr"}
	quota, clamp := calculateAudioQuota(QuotaInfo{
		InputDetails: TokenDetails{AudioTokens: common.MaxQuota},
		ModelName:    "spark-asr",
		ModelRatio:   2,
		GroupRatio:   1,
	})
	require.NotNil(t, clamp, "音频 token 极大时配额换算应饱和")
	require.Equal(t, common.MaxQuota, quota)
	noteQuotaClamp(relayInfo, clamp)

	other := model.NewLogOther()
	attachQuotaSaturation(ctx, relayInfo, other)

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	sat, ok := adminInfo["quota_saturation"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "QuotaFromDecimal", sat["op"])
}
