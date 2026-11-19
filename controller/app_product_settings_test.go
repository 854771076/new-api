package controller

import (
	"context"
	"fmt"
	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stripe/stripe-go/v81"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Schema from v2026.11.12.4, used to exercise real upgrade migrations.
type releasedProductApp struct {
	Id                     int    `json:"id" gorm:"primaryKey"`
	Name                   string `json:"name" gorm:"type:varchar(100);not null;index"`
	LogoURL                string `json:"logo_url" gorm:"type:text;column:logo_url"`
	ClientID               string `json:"client_id" gorm:"type:varchar(64);uniqueIndex;not null;column:client_id"`
	ClientSecretHash       string `json:"-" gorm:"type:varchar(64);not null;column:client_secret_hash"`
	RedirectURIs           string `json:"-" gorm:"type:text"` // JSON array of allowed redirect URIs
	PostLogoutRedirectURIs string `json:"-" gorm:"type:text"` // JSON array of allowed post-logout redirect URIs
	Scopes                 string `json:"-" gorm:"type:text"` // JSON array of allowed scopes
	Status                 int    `json:"status" gorm:"type:int;default:1"`
	CreatedBy              int    `json:"created_by" gorm:"type:int;not null;default:0;column:created_by"`
	CreatedAt              int64  `json:"created_at" gorm:"type:bigint;autoCreateTime"`
	UpdatedAt              int64  `json:"updated_at" gorm:"type:bigint;autoUpdateTime"`

	SupabaseProjectRef string `json:"-" gorm:"type:varchar(64);column:supabase_project_ref"`

	AIModelWhitelist string `json:"-" gorm:"type:text;column:ai_model_whitelist"`

	PublicClient bool `json:"public_client" gorm:"column:public_client"`

	WaffoPancakeReturnURL string `json:"-" gorm:"type:text;column:waffo_pancake_return_url"`
}

func (releasedProductApp) TableName() string { return "oauth_apps" }

func TestAppProductSettingsDatabaseMatrix(t *testing.T) {
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if kind == "postgres" {
			dsn = os.Getenv("TEST_POSTGRES_DSN")
		}
		if kind != "sqlite" && dsn == "" {
			t.Run(kind, func(t *testing.T) { t.Skip("external database DSN not configured") })
			continue
		}
		for _, upgrade := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/upgrade=%t", kind, upgrade), func(t *testing.T) {
				db, _ := newAuditTestDatabase(t, kind, dsn)
				conn, err := db.DB()
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, conn.Close()) })
				previous := model.DB
				model.DB = db
				t.Cleanup(func() { model.DB = previous })
				var version string
				query := "SELECT version()"
				if kind == "sqlite" {
					query = "SELECT sqlite_version()"
				}
				require.NoError(t, db.Raw(query).Scan(&version).Error)
				t.Logf("database: %s", version)
				if upgrade {
					require.NoError(t, db.AutoMigrate(&releasedProductApp{}))
					legacy := releasedProductApp{Name: "Legacy App", ClientID: "legacy-client", ClientSecretHash: "secret-hash", RedirectURIs: `["https://legacy.example/callback"]`, Scopes: `["openid"]`, Status: 1}
					require.NoError(t, db.Create(&legacy).Error)
				}
				for range 2 {
					require.NoError(t, db.AutoMigrate(&model.OAuthApp{}, &model.OAuthRefreshToken{}, &model.UserAppWallet{}))
				}
				if upgrade {
					var legacy model.OAuthApp
					require.NoError(t, db.Where("client_id = ?", "legacy-client").First(&legacy).Error)
					assert.True(t, legacy.IsOAuthEnabled())
					assert.False(t, legacy.APIKeyEnabled)
					assert.Nil(t, legacy.QuotaPerCredit)
					assert.False(t, legacy.MarketplaceVisible)
					assert.Equal(t, "secret-hash", legacy.ClientSecretHash)
					assert.Equal(t, []string{"https://legacy.example/callback"}, legacy.GetRedirectURIs())
					duplicate := legacy
					duplicate.Id = 0
					require.Error(t, db.Create(&duplicate).Error, "client_id uniqueness survives migration")
				}
				router := gin.New()
				router.POST("/api/oauth-apps/", CreateOAuthApp)
				router.PUT("/api/oauth-apps/:id", UpdateOAuthApp)
				router.GET("/api/app-marketplace", GetAppMarketplace)
				create := accessTokenRequest(router, "POST", "/api/oauth-apps/", "", "", `{"name":"Proxy App","quota_per_credit":100,"marketplace_visible":true,"marketplace_description":"Public text","marketplace_url":"https://app.example"}`)
				var result struct {
					Success      bool
					Data         OAuthAppResponse
					ClientSecret string `json:"client_secret"`
				}
				require.NoError(t, common.Unmarshal(create.Body.Bytes(), &result))
				require.True(t, result.Success, create.Body.String())
				assert.False(t, result.Data.ClientSecretConfigured)
				assert.False(t, result.Data.OAuthEnabled)
				assert.True(t, result.Data.APIKeyEnabled)
				assert.Empty(t, result.ClientSecret)
				assert.EqualValues(t, 100, *result.Data.QuotaPerCredit)
				appID := result.Data.Id
				stored, err := model.GetOAuthAppByID(appID)
				require.NoError(t, err)
				assert.Empty(t, stored.ClientSecretHash)
				cases := []struct {
					body    string
					success bool
				}{
					{`{"quota_per_credit":0,"name":"Must not persist"}`, false},
					{`{"quota_per_credit":-1}`, false}, {`{"quota_per_credit":1.5}`, false}, {`{"quota_per_credit":9007199254740992}`, false},
					{`{"marketplace_url":"javascript:alert(1)"}`, false}, {`{"marketplace_url":"http://app.example"}`, false}, {`{"marketplace_url":"https://user:pass@app.example"}`, false},
					{`{"api_key_enabled":false}`, false}, {`{"oauth_enabled":true}`, false},
					{`{"quota_per_credit":null}`, true},
				}
				for _, tc := range cases {
					response := accessTokenRequest(router, "PUT", fmt.Sprintf("/api/oauth-apps/%d", appID), "", "", tc.body)
					var envelope struct{ Success bool }
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
					assert.Equal(t, tc.success, envelope.Success, tc.body+response.Body.String())
				}
				stored, err = model.GetOAuthAppByID(appID)
				require.NoError(t, err)
				assert.Equal(t, "Proxy App", stored.Name)
				assert.Nil(t, stored.QuotaPerCredit)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("GET", "/api/app-marketplace", nil))
				var public struct {
					Success bool
					Data    []map[string]any
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &public))
				require.True(t, public.Success)
				require.Len(t, public.Data, 1)
				assert.Equal(t, map[string]any{"id": float64(appID), "name": "Proxy App", "logo_url": "", "description": "Public text", "url": "https://app.example"}, public.Data[0])
				// Stable priority + ID ordering, and hidden/disabled rows never appear.
				off := false
				for _, app := range []model.OAuthApp{
					{Name: "Earlier", ClientID: "earlier", Status: 1, OAuthEnabled: &off, APIKeyEnabled: true, MarketplaceVisible: true, MarketplaceSort: -1, MarketplaceURL: "https://earlier.example"},
					{Name: "Later", ClientID: "later", Status: 1, OAuthEnabled: &off, APIKeyEnabled: true, MarketplaceVisible: true, MarketplaceURL: "https://later.example"},
					{Name: "Hidden", ClientID: "hidden", Status: 1},
					{Name: "Disabled", ClientID: "disabled", Status: 2, MarketplaceVisible: true},
				} {
					require.NoError(t, db.Create(&app).Error)
				}
				response = httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("GET", "/api/app-marketplace", nil))
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &public))
				require.Len(t, public.Data, 3)
				assert.Equal(t, "Earlier", public.Data[0]["name"])
				assert.Equal(t, "Proxy App", public.Data[1]["name"])
				assert.Equal(t, "Later", public.Data[2]["name"])
				for range 2 {
					require.NoError(t, db.AutoMigrate(&model.OAuthApp{}, &model.OAuthRefreshToken{}, &model.UserAppWallet{}))
				}
				stored, err = model.GetOAuthAppByID(appID)
				require.NoError(t, err)
				assert.Nil(t, stored.QuotaPerCredit)
				assert.True(t, stored.MarketplaceVisible)
				wallet := model.UserAppWallet{UserId: 42, AppId: appID, Quota: -100, UsedQuota: 200}
				require.NoError(t, db.Create(&wallet).Error)
				for _, body := range []string{`{"marketplace_visible":false,"quota_per_credit":1000}`, `{"marketplace_visible":true}`} {
					response := accessTokenRequest(router, "PUT", fmt.Sprintf("/api/oauth-apps/%d", appID), "", "", body)
					require.Contains(t, response.Body.String(), `"success":true`)
					var balance model.UserAppWallet
					require.NoError(t, db.First(&balance, wallet.Id).Error)
					assert.Equal(t, -100, balance.Quota, "display settings preserve debt")
					assert.Equal(t, 200, balance.UsedQuota)
				}
			})
		}
	}
}

func TestAppCreditConversion(t *testing.T) {
	for _, tc := range []struct {
		quota int
		ratio int64
		want  string
	}{
		{100000, 1000, "100"}, {100000, 100, "1000"}, {1, 3, "0.333333"}, {-1, 3, "-0.333333"}, {-100, 100, "-1"}, {0, 100, "0"}, {-1, common.MaxWalletQuota, "0"}, {common.MaxWalletQuota, 1, "9007199254740991"},
	} {
		app := model.OAuthApp{QuotaPerCredit: &tc.ratio}
		require.NotNil(t, app.Credits(tc.quota))
		assert.Equal(t, tc.want, *app.Credits(tc.quota))
	}
	app := model.OAuthApp{}
	assert.Nil(t, app.Credits(100))
}

func TestAppModesDisableOAuthWithoutDisablingProxy(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	token, _, err := service.IssueOAuthAccessToken(env.user.Id, env.app.ClientID, "api:read")
	require.NoError(t, err)
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{TokenHash: model.OAuthRefreshTokenHash("refresh-before-disable"), AppID: env.app.Id, UserID: env.user.Id, SessionID: env.sessionID, AuthVersion: 1, AuthTime: 1700000000, ExpiresAt: time.Now().Add(time.Hour).Unix(), Scope: "openid"}))
	_, err = service.LoadOAuthAppByClientID(env.app.ClientID)
	require.NoError(t, err)
	router := gin.New()
	router.PUT("/api/oauth-apps/:id", UpdateOAuthApp)
	t.Run("revocation failure rolls back the mode change", func(t *testing.T) {
		const callback = "test:fail-app-refresh-revocation"
		require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "oauth_refresh_tokens" {
				tx.AddError(fmt.Errorf("refresh token storage unavailable"))
			}
		}))
		t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
		res := accessTokenRequest(router, "PUT", fmt.Sprintf("/api/oauth-apps/%d", env.app.Id), "", "", `{"api_key_enabled":true,"oauth_enabled":false}`)
		require.Contains(t, res.Body.String(), `"success":false`)
		stored, err := model.GetOAuthAppByID(env.app.Id)
		require.NoError(t, err)
		assert.True(t, stored.IsOAuthEnabled())
		assert.False(t, stored.APIKeyEnabled)
	})
	res := accessTokenRequest(router, "PUT", fmt.Sprintf("/api/oauth-apps/%d", env.app.Id), "", "", `{"api_key_enabled":true,"oauth_enabled":false}`)
	require.Contains(t, res.Body.String(), `"success":true`)
	app, err := service.LoadOAuthAppByClientID(env.app.ClientID)
	require.NoError(t, err)
	assert.True(t, app.IsEnabled())
	assert.False(t, app.IsOAuthEnabled())
	stored, err := model.GetOAuthAppByID(env.app.Id)
	require.NoError(t, err)
	assert.True(t, stored.APIKeyEnabled)
	assert.Equal(t, env.app.GetRedirectURIs(), stored.GetRedirectURIs())
	assert.Equal(t, env.app.ClientSecretHash, stored.ClientSecretHash)
	_, _, err = service.ValidateOAuthAccessToken(token)
	assert.ErrorIs(t, err, service.ErrOAuthTokenInvalid)
	refresh, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash("refresh-before-disable"))
	require.NoError(t, err)
	assert.NotZero(t, refresh.RevokedAt)
	for _, grant := range []string{"authorization_code", "refresh_token"} {
		c, rec := newOAuthFormContext(t, "/oauth/token", url.Values{"grant_type": {grant}, "client_id": {app.ClientID}, "client_secret": {"test-secret"}, "refresh_token": {"refresh-before-disable"}, "code": {"unused"}})
		OAuthToken(c)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	c, rec := env.newConsentContext(env.consentRequest(t, "openid", "unused"))
	OAuthAuthorizeConsent(c)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	// Both memory hits and database reloads must retain the OAuth mode.
	app, err = service.LoadOAuthAppByClientID(env.app.ClientID)
	require.NoError(t, err)
	assert.False(t, app.IsOAuthEnabled())
}

func TestAppSettingsRequireAdminScope(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.OAuthApp{}))
	write, _ := createScopedAccessToken(t, user.Id, 0, "option:write")
	read, _ := createScopedAccessToken(t, user.Id, 0, "option:read")
	router := gin.New()
	router.POST("/api/oauth-apps/", middleware.AdminAuth(), CreateOAuthApp)
	for _, tc := range []struct {
		token string
		code  int
	}{{"", 401}, {read, 403}, {write, 200}} {
		res := accessTokenRequest(router, "POST", "/api/oauth-apps/", tc.token, "", `{"name":"Scoped App"}`)
		assert.Equal(t, tc.code, res.Code, res.Body.String())
	}
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleCommonUser).Error)
	res := accessTokenRequest(router, "POST", "/api/oauth-apps/", write, "", `{"name":"Forbidden App"}`)
	assert.Equal(t, 403, res.Code)
}

type releasedProductUser struct {
	Id                   int                        `json:"id"`
	Username             string                     `json:"username" gorm:"unique;index" validate:"max=20"`
	Password             string                     `json:"password" gorm:"not null;" validate:"min=8,max=128"`
	HasPassword          bool                       `json:"-" gorm:"-:all"`
	OriginalPassword     string                     `json:"original_password" gorm:"-:all"` // this field is only for Password change verification, don't save it to database!
	DisplayName          string                     `json:"display_name" gorm:"index" validate:"max=20"`
	Role                 int                        `json:"role" gorm:"type:int;default:1"`   // admin, common
	Status               int                        `json:"status" gorm:"type:int;default:1"` // enabled, disabled
	Email                string                     `json:"email" gorm:"index" validate:"max=50"`
	GitHubId             string                     `json:"github_id" gorm:"column:github_id;index"`
	DiscordId            string                     `json:"discord_id" gorm:"column:discord_id;index"`
	OidcId               string                     `json:"oidc_id" gorm:"column:oidc_id;index"`
	WeChatId             string                     `json:"wechat_id" gorm:"column:wechat_id;index"`
	TelegramId           string                     `json:"telegram_id" gorm:"column:telegram_id;index"`
	VerificationCode     string                     `json:"verification_code" gorm:"-:all"`                         // this field is only for Email verification, don't save it to database!
	AccessToken          *string                    `json:"-" gorm:"type:char(32);column:access_token;uniqueIndex"` // Deprecated: 旧版面板访问令牌，仅在升级后的过渡期内使用；删除 users.access_token 列时一并移除。
	AccessTokenCreatedAt *int64                     `json:"-" gorm:"type:bigint;column:access_token_created_at"`    // Deprecated: 旧版面板访问令牌，仅在升级后的过渡期内使用；删除 users.access_token 列时一并移除。
	Quota                int                        `json:"quota" gorm:"type:int;default:0"`
	UsedQuota            int                        `json:"used_quota" gorm:"type:int;default:0;column:used_quota"` // used quota
	RequestCount         int                        `json:"request_count" gorm:"type:int;default:0;"`               // request number
	Group                string                     `json:"group" gorm:"type:varchar(64);default:'default'"`
	AffCode              string                     `json:"aff_code" gorm:"type:varchar(32);column:aff_code;uniqueIndex"`
	AffCount             int                        `json:"aff_count" gorm:"type:int;default:0;column:aff_count"`
	AffQuota             int                        `json:"aff_quota" gorm:"type:int;default:0;column:aff_quota"`           // 邀请剩余额度
	AffHistoryQuota      int                        `json:"aff_history_quota" gorm:"type:int;default:0;column:aff_history"` // 邀请历史额度
	InviterId            int                        `json:"inviter_id" gorm:"type:int;column:inviter_id;index"`
	DeletedAt            gorm.DeletedAt             `gorm:"index"`
	LinuxDOId            string                     `json:"linux_do_id" gorm:"column:linux_do_id;index"`
	Setting              string                     `json:"setting" gorm:"type:text;column:setting"`
	Remark               string                     `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	StripeCustomer       string                     `json:"stripe_customer" gorm:"type:varchar(64);column:stripe_customer;index"`
	CreatedAt            int64                      `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	LastLoginAt          int64                      `json:"last_login_at" gorm:"default:0;column:last_login_at"`
	AuthVersion          int64                      `json:"-" gorm:"type:bigint;not null;default:1;column:auth_version"`
	AdminPermissions     map[string]map[string]bool `json:"admin_permissions,omitempty" gorm:"-:all"`
}

func (releasedProductUser) TableName() string { return "users" }

type releasedProductToken struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"key" gorm:"type:varchar(128);uniqueIndex"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index" `
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"` // used quota
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	AutoGroups         string         `json:"-" gorm:"type:text"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
	// 允许用此令牌查询所属用户的额度；新建令牌默认开启，已有令牌升级后保持关闭
	AllowUserQuotaQuery bool   `json:"allow_user_quota_query"`
	CacheExpiresAt      int64  `json:"-" gorm:"-"`
	CacheMAC            string `json:"-" gorm:"-"`
}

func (releasedProductToken) TableName() string { return "tokens" }

// Pre-validity schema from main: upgrades must preserve existing budgets and rotations.
type previousQuotaRule struct {
	Id                 int
	UserId             int    `gorm:"index"`
	Name               string `gorm:"type:varchar(128)"`
	Description        string `gorm:"type:varchar(255)"`
	Enabled            bool
	QuotaLimitEnabled  bool
	QuotaPeriod        string `gorm:"type:varchar(16)"`
	QuotaLimit         int64
	TimeWindowEnabled  bool
	TimeWindowTimezone string `gorm:"type:varchar(32)"`
	AllowedWeekdays    string `gorm:"type:text"`
	CreatedAt          int64
	UpdatedAt          int64
}

func (previousQuotaRule) TableName() string { return "token_quota_rules" }

type previousKeyOperation struct {
	OperationKey string `gorm:"primaryKey;type:varchar(64)"`
	Fingerprint  string `gorm:"type:varchar(64);not null"`
	TokenId      int    `gorm:"not null"`
	OldTokenId   int    `gorm:"not null"`
	Committed    bool
}

func (previousKeyOperation) TableName() string { return "app_key_operations" }

func TestAppProxyDatabaseMatrix(t *testing.T) {
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if kind == "postgres" {
			dsn = os.Getenv("TEST_POSTGRES_DSN")
		}
		if kind != "sqlite" && dsn == "" {
			t.Run(kind, func(t *testing.T) { t.Skip("external database DSN not configured") })
			continue
		}
		for _, upgrade := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/upgrade=%t", kind, upgrade), func(t *testing.T) {
				db, _ := newAuditTestDatabase(t, kind, dsn)
				conn, err := db.DB()
				require.NoError(t, err)
				conn.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, conn.Close()) })
				previous, previousLog, redis := model.DB, model.LOG_DB, common.RedisEnabled
				main, log := common.MainDatabaseType(), common.LogDatabaseType()
				model.DB, model.LOG_DB, common.RedisEnabled = db, db, false
				switch kind {
				case "mysql":
					common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
				case "postgres":
					common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
				default:
					common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
				}
				t.Cleanup(func() {
					model.DB, model.LOG_DB, common.RedisEnabled = previous, previousLog, redis
					common.SetDatabaseTypes(main, log)
				})
				if upgrade {
					require.NoError(t, db.AutoMigrate(&releasedProductUser{}, &releasedProductToken{}, &releasedProductApp{}))
					require.NoError(t, db.Create(&releasedProductUser{Username: "legacy", Password: "hash", AffCode: "legacy", Quota: 9000, UsedQuota: 300}).Error)
					require.NoError(t, db.Create(&releasedProductToken{UserId: 1, Key: "legacy-key", RemainQuota: 500, UsedQuota: 40}).Error)
					require.NoError(t, db.AutoMigrate(&previousQuotaRule{}, &previousKeyOperation{}, &model.TokenQuotaRuleUsage{}))
					require.NoError(t, db.Create(&previousQuotaRule{UserId: 1, Name: "Existing budget", Enabled: true, QuotaLimitEnabled: true, QuotaLimit: 100, QuotaPeriod: "daily", TimeWindowTimezone: "UTC", AllowedWeekdays: "[]"}).Error)
					require.NoError(t, db.Create(&model.TokenQuotaRuleUsage{TokenId: 1, RuleId: 1, PeriodKey: "legacy", UsedQuota: 40}).Error)
					for _, committed := range []bool{false, true} {
						require.NoError(t, db.Create(&previousKeyOperation{OperationKey: fmt.Sprintf("legacy-%t", committed), Fingerprint: "legacy", TokenId: 1, OldTokenId: 1, Committed: committed}).Error)
					}
				}
				for range 2 {
					require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TokenQuotaRule{}, &model.TokenQuotaRuleUsage{}, &model.OAuthApp{}, &model.AppProxyIdentity{}, &model.AppKeyOperation{}, &model.UserAppWallet{}, &model.UserSubscription{}, &model.AppCheckout{}, &model.TopUp{}, &model.AppProductOrder{}, &model.SubscriptionOrder{}, &model.Log{}))
				}
				if upgrade {
					legacy, err := model.GetUserById(1, true)
					require.NoError(t, err)
					assert.Equal(t, "platform", legacy.Source())
					assert.Zero(t, legacy.OwnerAppId)
					assert.Equal(t, 9000, legacy.Quota)
					legacyKey, err := model.GetTokenById(1)
					require.NoError(t, err)
					assert.Zero(t, legacyKey.AppId)
					assert.Equal(t, 500, legacyKey.RemainQuota)
					assert.Equal(t, 40, legacyKey.UsedQuota)
					var oldRule model.TokenQuotaRule
					require.NoError(t, db.First(&oldRule, 1).Error)
					assert.Nil(t, oldRule.ValidFrom)
					assert.Nil(t, oldRule.ValidUntil)
					assert.Equal(t, "effective", oldRule.ValidityAt(time.Now().Unix()))
					var oldUsage model.TokenQuotaRuleUsage
					require.NoError(t, db.Where("token_id = ? AND rule_id = ?", 1, 1).First(&oldUsage).Error)
					assert.EqualValues(t, 40, oldUsage.UsedQuota)
					for _, committed := range []bool{false, true} {
						var operation model.AppKeyOperation
						require.NoError(t, db.First(&operation, "operation_key = ?", fmt.Sprintf("legacy-%t", committed)).Error)
						assert.Equal(t, committed, operation.Committed)
						assert.False(t, operation.Cancelled)
					}
				}
				app := model.OAuthApp{Name: "Proxy", ClientID: "matrix-proxy", APIKeyEnabled: true, ProxyIssuer: "https://native.example/auth/v1", Status: 1, QuotaPerCredit: new(int64(100))}
				require.NoError(t, db.Create(&app).Error)
				var ids [2]int
				var errs [2]error
				var wg sync.WaitGroup
				for i := range 2 {
					wg.Go(func() {
						user, err := model.ProvisionAppProxyUser(app.Id, app.ProxyIssuer, "native-subject")
						errs[i] = err
						if user != nil {
							ids[i] = user.Id
						}
					})
				}
				wg.Wait()
				for _, err := range errs {
					require.NoError(t, err)
				}
				assert.Equal(t, ids[0], ids[1])
				assert.Positive(t, ids[0])
				user, err := model.GetUserById(ids[0], true)
				require.NoError(t, err)
				assert.True(t, user.IsProxy())
				assert.Empty(t, user.Email)
				assert.Empty(t, user.Password)
				assert.Zero(t, user.Quota)
				other := app
				other.Id = 0
				other.ClientID = "second-app"
				require.NoError(t, db.Create(&other).Error)
				otherUser, err := model.ProvisionAppProxyUser(other.Id, other.ProxyIssuer, "native-subject")
				require.NoError(t, err)
				assert.NotEqual(t, user.Id, otherUser.Id)
				_, err = model.ProvisionAppProxyUser(app.Id, "https://attacker.example", "native-subject")
				assert.ErrorIs(t, err, model.ErrProxyIdentity)
				require.NoError(t, db.Model(user).Updates(map[string]any{"identity_source": "platform", "owner_app_id": other.Id}).Error)
				user, err = model.GetUserById(user.Id, true)
				require.NoError(t, err)
				assert.True(t, user.IsProxy())
				assert.Equal(t, app.Id, user.OwnerAppId)
				_, err = model.ReadAppWallet(user.Id, 0)
				assert.ErrorIs(t, err, model.ErrProxyIdentity)
				_, err = model.ReadAppWallet(user.Id, other.Id)
				assert.ErrorIs(t, err, model.ErrProxyIdentity)
				require.NoError(t, model.IncreaseUserAppQuota(user.Id, app.Id, 1000))
				require.NoError(t, db.Model(&model.UserAppWallet{}).Where("user_id = ? AND app_id = ?", user.Id, app.Id).Update("used_quota", 200).Error)
				wallet, err := model.ReadAppWallet(user.Id, app.Id)
				require.NoError(t, err)
				assert.Equal(t, "10", *wallet.Credits)
				assert.Equal(t, "2", *wallet.UsedCredits)
				assert.Equal(t, 1000, wallet.Quota)
				assert.False(t, wallet.Capabilities.SubscriptionSupported)
				assert.Nil(t, wallet.RequestCount)
				keyInput := &model.Token{Name: "Hosted", UnlimitedQuota: true, ExpiredTime: -1, ModelLimitsEnabled: true, ModelLimits: "allowed"}
				key, err := model.CreateAppProxyKey(user.Id, app.Id, "initial-issue", keyInput, 0)
				require.NoError(t, err)
				recovered, err := model.CreateAppProxyKey(user.Id, app.Id, "initial-issue", &model.Token{Name: "Hosted", UnlimitedQuota: true, ExpiredTime: -1, ModelLimitsEnabled: true, ModelLimits: "allowed"}, 0)
				require.NoError(t, err)
				assert.Equal(t, key.Id, recovered.Id)
				assert.Equal(t, key.Key, recovered.Key)
				_, err = model.CreateAppProxyKey(user.Id, app.Id, "initial-issue", &model.Token{Name: "Changed"}, 0)
				assert.ErrorIs(t, err, model.ErrAppOperationConflict)
				replacement, err := model.CreateAppProxyKey(user.Id, app.Id, "rotate-secret", &model.Token{}, key.Id)
				require.NoError(t, err)
				assert.Equal(t, key.ModelLimits, replacement.ModelLimits)
				active, err := model.GetTokenById(key.Id)
				require.NoError(t, err)
				assert.Equal(t, common.TokenStatusEnabled, active.Status)
				require.NoError(t, model.CommitAppProxyKeyRotation(user.Id, app.Id, replacement.Id, replacement.Key))
				require.NoError(t, model.CommitAppProxyKeyRotation(user.Id, app.Id, replacement.Id, replacement.Key))
				active, err = model.GetTokenById(key.Id)
				require.NoError(t, err)
				assert.Equal(t, common.TokenStatusDisabled, active.Status)
				limited, err := model.CreateAppProxyKey(user.Id, app.Id, "limited-issue", &model.Token{RemainQuota: 10, ExpiredTime: -1}, 0)
				require.NoError(t, err)
				_, err = model.CreateAppProxyKey(user.Id, app.Id, "limited-rotate", &model.Token{}, limited.Id)
				assert.ErrorIs(t, err, model.ErrLimitedKeyRotation)
				rule := &model.TokenQuotaRule{UserId: user.Id, Name: "Daily budget", Enabled: true, QuotaLimitEnabled: true, QuotaPeriod: "daily", QuotaLimit: 100, TimeWindowTimezone: "UTC", AllowedWeekdays: "[1,2,3,4,5,6,7]"}
				require.NoError(t, model.SaveTokenQuotaRule(rule))
				require.NoError(t, model.SetTokenQuotaRuleBinding(user.Id, replacement.Id, rule.Id))
				_, err = model.ReserveTokenQuotaRule(replacement.Id, 90, true)
				require.NoError(t, err)
				_, err = model.CreateAppProxyKey(user.Id, app.Id, "rule-bound-rotate", &model.Token{}, replacement.Id)
				assert.ErrorIs(t, err, model.ErrLimitedKeyRotation)
				var usage model.TokenQuotaRuleUsage
				require.NoError(t, db.Where("token_id = ? AND rule_id = ?", replacement.Id, rule.Id).First(&usage).Error)
				assert.EqualValues(t, 90, usage.UsedQuota)
				active, err = model.GetTokenById(replacement.Id)
				require.NoError(t, err)
				assert.Equal(t, common.TokenStatusEnabled, active.Status)

				stagedSource, err := model.CreateAppProxyKey(user.Id, app.Id, "staged-source", &model.Token{UnlimitedQuota: true, ExpiredTime: -1}, 0)
				require.NoError(t, err)
				staged, err := model.CreateAppProxyKey(user.Id, app.Id, "staged-rule-rotate", &model.Token{}, stagedSource.Id)
				require.NoError(t, err)
				assert.ErrorIs(t, model.SetTokenQuotaRuleBinding(user.Id, stagedSource.Id, rule.Id), model.ErrAppOperationConflict)
				require.NoError(t, model.CancelAppProxyKeyRotation(user.Id, app.Id, staged.Id))
				require.NoError(t, model.CancelAppProxyKeyRotation(user.Id, app.Id, staged.Id))
				assert.ErrorIs(t, model.CommitAppProxyKeyRotation(user.Id, app.Id, staged.Id, staged.Key), model.ErrRotationCancelled)
				_, err = model.CreateAppProxyKey(user.Id, app.Id, "staged-rule-rotate", &model.Token{}, stagedSource.Id)
				assert.ErrorIs(t, err, model.ErrRotationCancelled)
				assert.ErrorIs(t, model.UpdateAppProxyKeyPolicy(staged, map[string]any{"status": common.TokenStatusEnabled}), model.ErrRotationCancelled)
				assert.ErrorIs(t, model.CancelAppProxyKeyRotation(user.Id, app.Id, replacement.Id), model.ErrAppOperationConflict)
				require.NoError(t, model.SetTokenQuotaRuleBinding(user.Id, stagedSource.Id, rule.Id))
				runAppQuotaValidityCases(t, db, user.Id, app.Id)
				active, err = model.GetTokenById(stagedSource.Id)
				require.NoError(t, err)
				assert.Equal(t, common.TokenStatusEnabled, active.Status)

				// A persisted request and its exact order survive lost responses and
				// conversion/configuration changes without duplicate wallet credit.
				intent := &model.AppCheckout{Actor: "matrix-admin", IdentitySource: user.Source(), UserId: user.Id, AppId: app.Id, Kind: "topup", PaymentMethod: model.PaymentMethodWaffoPancake, Currency: "USD", Money: "0", Quota: int(2 * common.QuotaPerUnit), QuotaPerCredit: new(int64(100))}
				created, err := model.ReserveAppCheckout(intent, "payment-once", []int{2})
				require.NoError(t, err)
				require.True(t, created)
				originalUnit := common.QuotaPerUnit
				topup := &model.TopUp{UserId: user.Id, AppId: app.Id, Amount: 2, Money: 2, TradeNo: intent.OrderId, PaymentProvider: model.PaymentProviderWaffoPancake, PaymentMethod: model.PaymentMethodWaffoPancake, Status: common.TopUpStatusPending}
				require.NoError(t, topup.Insert())
				require.NoError(t, model.VerifyAppCheckoutPayment(intent.OrderId, "2.00", "USD"))
				for _, payment := range [][2]string{{"1.99", "USD"}, {"2", "CNY"}, {"2", ""}, {"invalid", "USD"}, {"-2", "USD"}} {
					assert.ErrorIs(t, model.VerifyAppCheckoutPayment(intent.OrderId, payment[0], payment[1]), model.ErrAppOperationConflict)
				}
				pending, err := model.ReadAppCheckout(user.Id, app.Id, intent.OrderId)
				require.NoError(t, err)
				assert.Equal(t, "reconciliation_required", pending.State)
				var unchanged model.TopUp
				require.NoError(t, db.Where("trade_no = ?", intent.OrderId).First(&unchanged).Error)
				assert.Equal(t, common.TopUpStatusPending, unchanged.Status)
				require.NoError(t, db.Model(&model.AppCheckout{}).Where("order_id = ?", intent.OrderId).Update("expires_at", common.GetTimestamp()-1).Error)
				assert.ErrorIs(t, model.VerifyAppCheckoutPayment(intent.OrderId, "2", "USD"), model.ErrAppOperationConflict)

				require.NoError(t, db.Model(&app).Update("api_key_enabled", false).Error)
				common.QuotaPerUnit = originalUnit * 2
				t.Cleanup(func() { common.QuotaPerUnit = originalUnit })
				require.NoError(t, model.RechargeWaffoPancake(intent.OrderId))
				require.NoError(t, model.RechargeWaffoPancake(intent.OrderId))
				common.QuotaPerUnit = originalUnit
				observed, err := model.ReadAppCheckout(user.Id, app.Id, intent.OrderId)
				require.NoError(t, err)
				assert.Equal(t, "success", observed.State)
				// Valid duplicate webhooks acknowledge the already-paid order after expiry.
				require.NoError(t, model.VerifyAppCheckoutPayment(intent.OrderId, "2", "USD"))
				assert.Equal(t, int(2*originalUnit), observed.Quota)
				assert.EqualValues(t, 100, *observed.QuotaPerCredit)
				require.NotNil(t, observed.Credits)
				assert.Equal(t, "10000", *observed.Credits)
				page := &common.PageInfo{Page: 1, PageSize: 10}
				orders, total, err := model.ListScopedAppOrders(user.Id, app.Id, "", page)
				require.NoError(t, err)
				assert.EqualValues(t, 1, total)
				require.Len(t, orders, 1)
				assert.Equal(t, intent.OrderId, orders[0].OrderId)
				assert.Equal(t, "USD", orders[0].Currency)
				assert.Equal(t, 2.0, orders[0].Money)
				assert.Equal(t, observed.Credits, orders[0].Credits)

				// Legacy providers did not persist a currency; never infer it
				// from today's configuration or mislabel a stored unit count.
				for _, provider := range []string{model.PaymentProviderEpay, model.PaymentProviderStripe, model.PaymentProviderCreem} {
					legacy := &model.TopUp{UserId: user.Id, AppId: app.Id, Amount: 1, Money: 2, TradeNo: "legacy-" + provider, PaymentProvider: provider, Status: common.TopUpStatusSuccess}
					require.NoError(t, legacy.Insert())
					items, count, err := model.ListScopedAppOrders(user.Id, app.Id, legacy.TradeNo, page)
					require.NoError(t, err)
					require.EqualValues(t, 1, count)
					require.Len(t, items, 1)
					currency := ""
					if provider == model.PaymentProviderEpay {
						currency = "CNY"
					}
					assert.Equal(t, currency, items[0].Currency)
					assert.Nil(t, items[0].QuotaPerCredit)
					assert.Nil(t, items[0].Credits)
				}
				for i, invalid := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
					bad := &model.AppCheckout{Actor: "matrix-admin", IdentitySource: user.Source(), UserId: user.Id, AppId: app.Id, Kind: "topup", Money: "0", Quota: 100}
					_, err := model.ReserveAppCheckout(bad, fmt.Sprintf("invalid-money-%d", i), i)
					require.NoError(t, err)
					topup := &model.TopUp{UserId: user.Id, AppId: app.Id, Amount: 100, Money: invalid, TradeNo: bad.OrderId, PaymentProvider: model.PaymentProviderWaffoPancake}
					require.Error(t, topup.Insert())
					stored, err := model.ReadAppCheckout(user.Id, app.Id, bad.OrderId)
					require.NoError(t, err)
					assert.False(t, stored.SnapshotReady)
				}

				require.NoError(t, db.Model(&app).Update("api_key_enabled", true).Error)
				require.NoError(t, db.Model(user).Update("status", common.UserStatusDisabled).Error)
				recoveredUser, err := model.ProvisionAppProxyUser(app.Id, app.ProxyIssuer, "native-subject")
				require.NoError(t, err)
				assert.Equal(t, user.Id, recoveredUser.Id)
				assert.Equal(t, common.UserStatusDisabled, recoveredUser.Status)
				_, err = model.CreateAppProxyKey(user.Id, app.Id, "disabled-create", &model.Token{}, 0)
				assert.ErrorIs(t, err, model.ErrProxyIdentity)
				for range 2 {
					require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.OAuthApp{}, &model.AppProxyIdentity{}, &model.AppKeyOperation{}, &model.UserAppWallet{}, &model.AppCheckout{}))
				}
				wallet, err = model.ReadAppWallet(user.Id, app.Id)
				require.NoError(t, err)
				assert.Equal(t, 1000+int(2*originalUnit), wallet.Quota)
			})
		}
	}
}

func runAppQuotaValidityCases(t *testing.T, db *gorm.DB, userID, appID int) {
	t.Helper()
	require.NoError(t, i18n.Init())
	rule := model.TokenQuotaRule{UserId: userID, Name: "Timed", Enabled: true, QuotaLimitEnabled: true, QuotaLimit: 100, QuotaPeriod: "monthly", TimeWindowTimezone: "Asia/Shanghai", AllowedWeekdays: "[]", ValidFrom: new(time.Now().Unix() - 60), ValidUntil: new(time.Now().Unix() + 3600)}
	require.NoError(t, model.SaveTokenQuotaRule(&rule))
	key, err := model.CreateAppProxyKey(userID, appID, "timed-atomic-key", &model.Token{UnlimitedQuota: true, ExpiredTime: -1, QuotaRuleId: rule.Id}, 0)
	require.NoError(t, err)
	reservation, err := model.ReserveTokenQuotaRule(key.Id, 40, true)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	info := &relaycommon.RelayInfo{UserId: userID, AppId: appID, TokenId: key.Id, ChannelMeta: &relaycommon.ChannelMeta{}, TokenQuotaReservation: reservation, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: model.GenerateTaskID()}, BillingSource: "wallet"}
	require.NoError(t, model.StageTaskQuotaReservation(context.Background(), info, constant.TaskPlatform("test")))
	require.NoError(t, model.StageTaskQuotaReservation(context.Background(), info, constant.TaskPlatform("test")), "retrying the same snapshot must accept MySQL no-op updates")
	var staged model.Task
	require.NoError(t, db.First(&staged, info.TaskReservationID).Error)
	assert.Equal(t, reservation, staged.PrivateData.TokenQuotaReservation)
	assert.Equal(t, userID, staged.UserId)
	assert.Equal(t, appID, staged.AppId)
	assert.Empty(t, model.GetAllUnFinishSyncTasks(10), "uncertain submissions must not be polled or resubmitted")
	data := model.TaskPrivateData{TokenId: key.Id, TokenQuotaReservation: reservation}
	encoded, err := data.Value()
	require.NoError(t, err)
	var restored model.TaskPrivateData
	require.NoError(t, restored.Scan(encoded))
	assert.Equal(t, reservation, restored.TokenQuotaReservation)
	rule.QuotaLimit = 50
	require.NoError(t, model.SaveTokenQuotaRule(&rule))
	require.NoError(t, model.AdjustTokenQuotaRuleUsage(restored.TokenQuotaReservation, 30, true))
	_, err = model.ReserveTokenQuotaRule(key.Id, 1, true)
	assert.ErrorIs(t, err, model.ErrTokenQuotaExhausted)
	rule.QuotaLimit = 200
	require.NoError(t, model.SaveTokenQuotaRule(&rule))
	assert.ErrorIs(t, model.AdjustTokenQuotaRuleUsage(reservation, 31, true), model.ErrTokenQuotaExhausted)
	rule.ValidUntil = new(time.Now().Unix() - 1)
	require.NoError(t, model.SaveTokenQuotaRule(&rule))
	_, err = model.ReserveTokenQuotaRule(key.Id, 0, false)
	assert.ErrorIs(t, err, model.ErrTokenQuotaExpired, "free requests also obey expiry")
	require.NoError(t, model.SetTokenQuotaRuleBinding(userID, key.Id, 0))
	require.NoError(t, model.DeleteTokenQuotaRule(userID, rule.Id))
	require.NoError(t, model.AdjustTokenQuotaRuleUsage(reservation, 20, true), "deletion preserves the frozen cap")
	assert.ErrorIs(t, model.AdjustTokenQuotaRuleUsage(&relaycommon.TokenQuotaReservation{UsageID: reservation.UsageID}, 1, true), model.ErrReservationSnapshotUnavailable)
	require.NoError(t, model.AdjustTokenQuotaRuleUsage(&relaycommon.TokenQuotaReservation{UsageID: reservation.UsageID}, 30, false))
	var usage model.TokenQuotaRuleUsage
	require.NoError(t, db.First(&usage, reservation.UsageID).Error)
	assert.EqualValues(t, 120, usage.UsedQuota, "real settlement is never clipped to the cap")
	require.NoError(t, model.AdjustTokenQuotaRuleUsage(reservation, -120, false))
	require.NoError(t, db.First(&usage, reservation.UsageID).Error)
	assert.Zero(t, usage.UsedQuota)
	staged.Status = model.TaskStatusSuccess
	staged.PrivateData.UpstreamTaskID = "accepted-upstream"
	require.NoError(t, staged.CompleteReservedTask(context.Background(), 0))
	require.NoError(t, db.First(&staged, info.TaskReservationID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, staged.Status)

	rule.Id, rule.QuotaLimitEnabled, rule.ValidUntil = 0, false, nil
	rule.ValidFrom = new(time.Now().Unix() + 3600)
	require.NoError(t, model.SaveTokenQuotaRule(&rule))
	require.NoError(t, model.SetTokenQuotaRuleBinding(userID, key.Id, rule.Id))
	_, err = model.ReserveTokenQuotaRule(key.Id, 0, false)
	assert.ErrorIs(t, err, model.ErrTokenQuotaNotStarted)
	rule.ValidFrom = nil
	require.NoError(t, model.SaveTokenQuotaRule(&rule))
	uncapped, err := model.ReserveTokenQuotaRule(key.Id, 40, true)
	require.NoError(t, err)
	assert.EqualValues(t, 40, uncapped.Amount, "time-only task snapshots retain the reserved funding amount")
	rule.QuotaLimitEnabled = true
	require.NoError(t, model.SaveTokenQuotaRule(&rule))
	require.NoError(t, model.AdjustTokenQuotaRuleUsage(uncapped, 40, true))
	assert.Zero(t, uncapped.UsageID, "no new budget row for previously uncapped work")
	rollbackSource, err := model.CreateAppProxyKey(userID, appID, "cancel-rollback-source", &model.Token{UnlimitedQuota: true, ExpiredTime: -1}, 0)
	require.NoError(t, err)
	rollbackReplacement, err := model.CreateAppProxyKey(userID, appID, "cancel-rollback-replacement", &model.Token{}, rollbackSource.Id)
	require.NoError(t, err)
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:cancel-failure", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*model.AppKeyOperation); ok {
			tx.AddError(fmt.Errorf("injected cancellation failure"))
		}
	}))
	cancelFailure := model.CancelAppProxyKeyRotation(userID, appID, rollbackReplacement.Id)
	require.NoError(t, db.Callback().Update().Remove("test:cancel-failure"))
	require.Error(t, cancelFailure)
	var unchangedReplacement model.Token
	require.NoError(t, db.First(&unchangedReplacement, rollbackReplacement.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, unchangedReplacement.Status, "failed cancellation must not half-disable the replacement")
	assert.ErrorIs(t, model.SetTokenQuotaRuleBinding(userID, rollbackSource.Id, rule.Id), model.ErrAppOperationConflict)
	require.NoError(t, model.CancelAppProxyKeyRotation(userID, appID, rollbackReplacement.Id))
	staleWriter := httptest.NewRecorder()
	staleContext, _ := gin.CreateTestContext(staleWriter)
	staleContext.Request = httptest.NewRequest(http.MethodPost, "/v1/tasks", nil)
	assert.Error(t, middleware.SetupContextForToken(staleContext, rollbackReplacement), "a cached enabled replacement cannot bypass cancellation")
	assert.Equal(t, http.StatusForbidden, staleWriter.Code)
	require.NoError(t, model.SetTokenQuotaRuleBinding(userID, rollbackSource.Id, rule.Id))
	conn, err := db.DB()
	require.NoError(t, err)
	if common.MainDatabaseType() != common.DatabaseTypeSQLite {
		conn.SetMaxOpenConns(4)
	}
	defer conn.SetMaxOpenConns(1)
	reservation, err = model.ReserveTokenQuotaRule(key.Id, 0, true)
	require.NoError(t, err)
	var adjustments [2]error
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		wg.Go(func() { <-start; adjustments[i] = model.AdjustTokenQuotaRuleUsage(reservation, 120, true) })
	}
	close(start)
	wg.Wait()
	successes := 0
	for _, err := range adjustments {
		if err == nil {
			successes++
		} else {
			assert.ErrorIs(t, err, model.ErrTokenQuotaExhausted)
		}
	}
	assert.Equal(t, 1, successes, "competing supplements must share the frozen aggregate cap")

	source, err := model.CreateAppProxyKey(userID, appID, "concurrent-source", &model.Token{UnlimitedQuota: true, ExpiredTime: -1}, 0)
	require.NoError(t, err)
	replacement, err := model.CreateAppProxyKey(userID, appID, "concurrent-replacement", &model.Token{}, source.Id)
	require.NoError(t, err)
	var cancelErr, commitErr error
	start = make(chan struct{})
	wg.Go(func() { <-start; cancelErr = model.CancelAppProxyKeyRotation(userID, appID, replacement.Id) })
	wg.Go(func() {
		<-start
		commitErr = model.CommitAppProxyKeyRotation(userID, appID, replacement.Id, replacement.Key)
	})
	close(start)
	wg.Wait()
	if cancelErr == nil {
		assert.ErrorIs(t, commitErr, model.ErrRotationCancelled)
	} else {
		require.NoError(t, commitErr)
		assert.ErrorIs(t, cancelErr, model.ErrAppOperationConflict)
	}
	var finalSource, finalReplacement model.Token
	require.NoError(t, db.First(&finalSource, source.Id).Error)
	require.NoError(t, db.First(&finalReplacement, replacement.Id).Error)
	if cancelErr == nil {
		assert.Equal(t, common.TokenStatusEnabled, finalSource.Status)
		assert.Equal(t, common.TokenStatusDisabled, finalReplacement.Status)
	} else {
		assert.Equal(t, common.TokenStatusDisabled, finalSource.Status)
		assert.Equal(t, common.TokenStatusEnabled, finalReplacement.Status)
	}

	source, err = model.CreateAppProxyKey(userID, appID, "concurrent-bind-source", &model.Token{UnlimitedQuota: true, ExpiredTime: -1}, 0)
	require.NoError(t, err)
	var createErr, bindErr error
	start = make(chan struct{})
	wg.Go(func() {
		<-start
		_, createErr = model.CreateAppProxyKey(userID, appID, "concurrent-bind-replacement", &model.Token{}, source.Id)
	})
	wg.Go(func() { <-start; bindErr = model.SetTokenQuotaRuleBinding(userID, source.Id, rule.Id) })
	close(start)
	wg.Wait()
	if bindErr == nil {
		assert.ErrorIs(t, createErr, model.ErrLimitedKeyRotation)
	} else {
		require.NoError(t, createErr)
		assert.ErrorIs(t, bindErr, model.ErrAppOperationConflict)
	}

	app, err := model.GetOAuthAppByID(appID)
	require.NoError(t, err)
	billedUser, err := model.ProvisionAppProxyUser(appID, app.ProxyIssuer, "billing-snapshot")
	require.NoError(t, err)
	require.NoError(t, model.IncreaseUserAppQuota(billedUser.Id, appID, 500))
	billedRule := model.TokenQuotaRule{UserId: billedUser.Id, Name: "Frozen task budget", Enabled: true, QuotaLimitEnabled: true, QuotaLimit: 100, QuotaPeriod: "monthly", TimeWindowTimezone: "UTC", AllowedWeekdays: "[]"}
	require.NoError(t, model.SaveTokenQuotaRule(&billedRule))
	billedKey, err := model.CreateAppProxyKey(billedUser.Id, appID, "billing-snapshot-key", &model.Token{UnlimitedQuota: true, ExpiredTime: -1, QuotaRuleId: billedRule.Id}, 0)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tasks", nil)
	c.Set("app_proxy", true)
	billedInfo := &relaycommon.RelayInfo{UserId: billedUser.Id, AppId: appID, TokenId: billedKey.Id, TokenKey: billedKey.Key, TokenUnlimited: true, ForcePreConsume: true, ChannelMeta: &relaycommon.ChannelMeta{}, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: model.GenerateTaskID()}}
	session, apiErr := service.NewBillingSession(c, billedInfo, 40)
	require.Nil(t, apiErr)
	require.NoError(t, model.StageTaskQuotaReservation(context.Background(), billedInfo, constant.TaskPlatform("test")))
	billedRule.QuotaLimit, billedRule.ValidUntil = 50, new(time.Now().Unix()-1)
	require.NoError(t, model.SaveTokenQuotaRule(&billedRule))
	require.NoError(t, session.ReserveBeforeSubmit(70), "in-flight work uses its original budget after expiry and a cap reduction")
	assert.Error(t, session.ReserveBeforeSubmit(101), "failed supplements must roll back wallet funding")
	// Persist the final admission snapshot before sending, then publish the task once.
	require.NoError(t, model.StageTaskQuotaReservation(context.Background(), billedInfo, constant.TaskPlatform("test")))
	billedTask := model.InitTask(constant.TaskPlatform("test"), billedInfo)
	billedTask.Quota = 70
	billedTask.PrivateData = model.TaskPrivateData{TokenId: billedKey.Id, BillingSource: "wallet", TokenQuotaReservation: billedInfo.TokenQuotaReservation}
	require.NoError(t, session.PersistTask(context.Background(), billedTask))
	require.NoError(t, session.Settle(70))
	require.NoError(t, session.Settle(70))
	billedWallet, err := model.ReadAppWallet(billedUser.Id, appID)
	require.NoError(t, err)
	assert.Equal(t, 430, billedWallet.Quota)
	assert.Equal(t, 70, billedWallet.UsedQuota, "task publication and session settlement must not double-count usage")
	var billedUsage model.TokenQuotaRuleUsage
	require.NoError(t, db.First(&billedUsage, billedInfo.TokenQuotaReservation.UsageID).Error)
	assert.EqualValues(t, 70, billedUsage.UsedQuota)
	var persistedBilledTask model.Task
	require.NoError(t, db.First(&persistedBilledTask, billedTask.ID).Error)
	assert.EqualValues(t, 70, persistedBilledTask.PrivateData.TokenQuotaReservation.Amount)
	assert.EqualValues(t, 100, persistedBilledTask.PrivateData.TokenQuotaReservation.QuotaLimit)
	var taskCount int64
	require.NoError(t, db.Model(&model.Task{}).Where("user_id = ?", billedUser.Id).Count(&taskCount).Error)
	assert.EqualValues(t, 1, taskCount)
}

func TestAppProxyQuotaManagementAuthorization(t *testing.T) {
	admin, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.OAuthApp{}, &model.AppProxyIdentity{}, &model.AppKeyOperation{}, &model.Token{}, &model.TokenQuotaRule{}, &model.TokenQuotaRuleUsage{}, &model.UserAppWallet{}))
	write, _ := createScopedAccessToken(t, admin.Id, 0, "api_key:read", "api_key:write")
	read, _ := createScopedAccessToken(t, admin.Id, 0, "api_key:read")
	app := model.OAuthApp{Name: "Rules", ClientID: "rules-http", ProxyIssuer: "https://native.example/auth/v1", APIKeyEnabled: true, Status: 1}
	require.NoError(t, model.DB.Create(&app).Error)
	user, err := model.ProvisionAppProxyUser(app.Id, app.ProxyIssuer, "rules-user")
	require.NoError(t, err)
	other, err := model.ProvisionAppProxyUser(app.Id, app.ProxyIssuer, "rules-other")
	require.NoError(t, err)
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	group := router.Group("/api/app-proxy", middleware.AdminAuth())
	group.GET("/users/:user_id/quota-rules", ListTokenQuotaRules)
	group.POST("/users/:user_id/quota-rules", SaveTokenQuotaRule)
	group.PUT("/users/:user_id/quota-rules/:id", SaveTokenQuotaRule)
	group.DELETE("/users/:user_id/quota-rules/:id", DeleteTokenQuotaRule)
	group.PUT("/users/:user_id/keys/:token_id/quota-rule", BindTokenQuotaRule)
	group.POST("/users/:user_id/keys/:token_id/cancel-rotation", CancelAppProxyKeyRotation)
	base := fmt.Sprintf("/api/app-proxy/users/%d", user.Id)
	query := fmt.Sprintf("?app_id=%d", app.Id)
	body := `{"name":"Membership","enabled":true,"quota_limit_enabled":false,"quota_period":"daily","quota_limit":0,"time_window_enabled":false,"time_window_timezone":"UTC","allowed_weekdays":"[]","valid_from":100,"valid_until":200}`
	ordinary := model.User{Username: "ordinary-rule-actor", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "ordinary-rule-actor"}
	require.NoError(t, model.DB.Create(&ordinary).Error)
	ordinaryPAT, _ := createScopedAccessToken(t, ordinary.Id, 0, "api_key:read", "api_key:write")
	assert.Equal(t, 403, accessTokenRequest(router, "GET", base+"/quota-rules"+query, ordinaryPAT, "", "").Code)
	assert.Equal(t, 403, accessTokenRequest(router, "POST", base+"/quota-rules"+query, ordinaryPAT, "", body).Code)

	assert.Equal(t, 401, accessTokenRequest(router, "GET", base+"/quota-rules"+query, "", "", "").Code)
	assert.Equal(t, 403, accessTokenRequest(router, "POST", base+"/quota-rules"+query, read, "", body).Code)
	response := decodeAPIResponse(t, accessTokenRequest(router, "POST", base+"/quota-rules"+query, write, "", body))
	require.True(t, response.Success, response.Message)
	var rule model.TokenQuotaRule
	require.NoError(t, common.Unmarshal(response.Data, &rule))
	assert.Equal(t, user.Id, rule.UserId)
	assert.Equal(t, "expired", rule.ValidityStatus)
	for _, extra := range []string{`,"user_id":1`, `,"app_id":1`, `,"unknown":true`} {
		assert.False(t, decodeAPIResponse(t, accessTokenRequest(router, "POST", base+"/quota-rules"+query, write, "", strings.TrimSuffix(body, "}")+extra+"}")).Success)
	}
	assert.False(t, decodeAPIResponse(t, accessTokenRequest(router, "GET", base+"/quota-rules?app_id=999999", read, "", "")).Success)
	foreignPath := fmt.Sprintf("/api/app-proxy/users/%d/quota-rules/%d%s", other.Id, rule.Id, query)
	assert.False(t, decodeAPIResponse(t, accessTokenRequest(router, "PUT", foreignPath, write, "", body)).Success)
	key, err := model.CreateAppProxyKey(user.Id, app.Id, "api-rule-key", &model.Token{UnlimitedQuota: true, ExpiredTime: -1}, 0)
	require.NoError(t, err)
	bindPath := fmt.Sprintf("%s/keys/%d/quota-rule%s", base, key.Id, query)
	bindBody := fmt.Sprintf(`{"rule_id":%d}`, rule.Id)
	require.True(t, decodeAPIResponse(t, accessTokenRequest(router, "PUT", bindPath, write, "", bindBody)).Success)
	require.NoError(t, model.DB.Model(&app).Update("api_key_enabled", false).Error)
	assert.False(t, decodeAPIResponse(t, accessTokenRequest(router, "PUT", base+fmt.Sprintf("/quota-rules/%d", rule.Id)+query, write, "", body)).Success)
	assert.True(t, decodeAPIResponse(t, accessTokenRequest(router, "GET", base+"/quota-rules"+query, read, "", "")).Success)
	assert.True(t, decodeAPIResponse(t, accessTokenRequest(router, "PUT", bindPath, write, "", `{"rule_id":0}`)).Success)
}

func TestAppProxyHTTPAndFunding(t *testing.T) {
	admin, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.OAuthApp{}, &model.AppProxyIdentity{}, &model.AppKeyOperation{}, &model.Token{}, &model.TokenQuotaRule{}, &model.TokenQuotaRuleUsage{}, &model.UserAppWallet{}, &model.UserSubscription{}, &model.SubscriptionPlan{}))
	write, _ := createScopedAccessToken(t, admin.Id, 0, "user:write", "api_key:write", "api_key:read", "user:read")
	read, _ := createScopedAccessToken(t, admin.Id, 0, "user:read")
	router := gin.New()
	router.POST("/api/app-proxy/users", middleware.AdminAuth(), ProvisionAppProxyUser)
	router.GET("/api/app-proxy/users/:user_id/wallet", middleware.AdminAuth(), AdminAppWallet)
	router.POST("/api/app-proxy/users/:user_id/keys", middleware.AdminAuth(), CreateAppProxyKey)
	router.GET("/api/app-proxy/users/:user_id/keys", middleware.AdminAuth(), ListAppProxyKeys)
	app := model.OAuthApp{Name: "HTTP", ClientID: "proxy-http", ProxyIssuer: "https://native.example/auth/v1", APIKeyEnabled: true, Status: 1}
	require.NoError(t, model.DB.Create(&app).Error)
	body := fmt.Sprintf(`{"app_id":%d,"issuer":%q,"subject":"native-user"}`, app.Id, app.ProxyIssuer)
	assert.Equal(t, 401, accessTokenRequest(router, "POST", "/api/app-proxy/users", "", "", body).Code)
	assert.Equal(t, 403, accessTokenRequest(router, "POST", "/api/app-proxy/users", read, "", body).Code)
	response := accessTokenRequest(router, "POST", "/api/app-proxy/users", write, "", body)
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), "password")
	var provision struct {
		Data struct {
			UserId int `json:"user_id"`
		}
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &provision))
	userID := provision.Data.UserId
	require.Positive(t, userID)
	_, err := service.CreateLoginSession(userID, "password", "", "")
	assert.ErrorIs(t, err, service.ErrLoginSessionInvalid)
	_, _, err = service.IssueOAuthAccessToken(userID, app.ClientID, "openid")
	assert.ErrorIs(t, err, service.ErrOAuthTokenInvalid)
	plan := &model.SubscriptionPlan{Id: 1}
	_, err = model.CreateUserSubscriptionFromPlanTx(model.DB, userID, plan, "admin")
	assert.ErrorIs(t, err, model.ErrProxySubscriptionUnsupported)
	path := fmt.Sprintf("/api/app-proxy/users/%d/keys?app_id=%d", userID, app.Id)
	for _, unsupported := range []string{`{"name":"Hosted","quota_rule_id":1}`, `{"name":"Hosted","user_id":1}`, `null`} {
		rejected := accessTokenRequest(router, "POST", path, write, "", unsupported)
		assert.Contains(t, rejected.Body.String(), `"success":false`)
	}
	request := httptest.NewRequest("POST", path, strings.NewReader(`{"name":"Hosted","unlimited_quota":true}`))
	request.Header.Set("Authorization", "Bearer "+write)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "http-issue-once")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	require.Equal(t, 200, result.Code, result.Body.String())
	var created struct {
		Success bool
		Data    model.Token
	}
	require.NoError(t, common.Unmarshal(result.Body.Bytes(), &created))
	require.True(t, created.Success)
	require.NotEmpty(t, created.Data.Key)
	listed := accessTokenRequest(router, "GET", path, write, "", "")
	assert.NotContains(t, listed.Body.String(), created.Data.Key)
	assert.Contains(t, listed.Body.String(), `"app_id":`)
	readOnly := gin.New()
	readOnly.GET("/key-status", middleware.TokenAuthReadOnly(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	assert.Equal(t, http.StatusNoContent, accessTokenRequest(readOnly, "GET", "/key-status", "sk-"+created.Data.Key, "", "").Code)
	require.NoError(t, model.IncreaseUserAppQuota(userID, app.Id, 1000))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	require.NoError(t, middleware.SetupContextForToken(c, &created.Data))
	assert.Equal(t, app.Id, common.GetContextKeyInt(c, constant.ContextKeyOAuthAppID))
	info := &relaycommon.RelayInfo{UserId: userID, AppId: app.Id, IsPlayground: true, RequestId: "proxy-funding", UserSetting: dto.UserSetting{BillingPreference: "subscription_only"}}
	session, billingErr := service.NewBillingSession(c, info, 100)
	require.Nil(t, billingErr)
	require.NotNil(t, session)
	require.NoError(t, session.Settle(125))
	wallet, err := model.ReadAppWallet(userID, app.Id)
	require.NoError(t, err)
	assert.Equal(t, 875, wallet.Quota)
	assert.Equal(t, 125, wallet.UsedQuota)
	user, err := model.GetUserById(userID, true)
	require.NoError(t, err)
	assert.Zero(t, user.Quota)
	require.NoError(t, model.DB.Model(&app).Update("api_key_enabled", false).Error)
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	assert.ErrorIs(t, middleware.SetupContextForToken(c, &created.Data), model.ErrProxyIdentity)
	assert.True(t, c.IsAborted())
	assert.Equal(t, http.StatusForbidden, accessTokenRequest(readOnly, "GET", "/key-status", "sk-"+created.Data.Key, "", "").Code)
	require.NoError(t, model.DB.Model(&app).Update("api_key_enabled", true).Error)
	require.NoError(t, model.DB.Model(&created.Data).Update("status", common.TokenStatusDisabled).Error)
	assert.Equal(t, http.StatusUnauthorized, accessTokenRequest(readOnly, "GET", "/key-status", "sk-"+created.Data.Key, "", "").Code)
}

func TestAppProviderPaymentSnapshots(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:read api:write")
	secret, webhook, priceID, minimum, promotion, stripeKey := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId, setting.StripeMinTopUp, setting.StripePromotionCodesEnabled, stripe.Key
	backend := stripe.GetBackend(stripe.APIBackend)
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId, setting.StripeMinTopUp, setting.StripePromotionCodesEnabled, stripe.Key = secret, webhook, priceID, minimum, promotion, stripeKey
		stripe.SetBackend(stripe.APIBackend, backend)
	})
	setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId, setting.StripeMinTopUp, setting.StripePromotionCodesEnabled = "sk_test_fixture", "whsec_fixture", "price_fixture", 1, true
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/prices/price_fixture":
			_, _ = w.Write([]byte(`{"id":"price_fixture","active":true,"currency":"usd","type":"one_time","billing_scheme":"per_unit","unit_amount":250}`))
		case "/v1/checkout/sessions":
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, "false", r.PostForm.Get("allow_promotion_codes"))
			_, _ = w.Write([]byte(`{"id":"cs_fixture","url":"https://checkout.stripe.com/fixture"}`))
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(provider.Close)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(provider.URL), HTTPClient: provider.Client(), MaxNetworkRetries: stripe.Int64(0)}))
	amount := max(int64(2), getStripeMinTopup())
	quota, err := validateTopUpQuota(amount)
	require.NoError(t, err)
	intent := &model.AppCheckout{Actor: "provider-test", IdentitySource: env.user.Source(), UserId: env.user.Id, AppId: env.app.Id, Kind: "topup", PaymentMethod: "stripe", Currency: "USD", Money: "0", Quota: quota}
	_, err = model.ReserveAppCheckout(intent, "stripe-snapshot", amount)
	require.NoError(t, err)
	_, _, err = generateStripeTopUpCheckout(model.WithAppCheckout(context.Background(), intent), env.user, amount, env.app.Id)
	require.NoError(t, err)
	order, err := model.ReadAppCheckout(env.user.Id, env.app.Id, intent.OrderId)
	require.NoError(t, err)
	units := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		units = amount / int64(common.QuotaPerUnit)
	}
	assert.Equal(t, fmt.Sprintf("%g", float64(units)*2.5), order.Money)
	assert.Equal(t, quota, order.Quota)
	require.NoError(t, model.VerifyAppCheckoutPayment(intent.OrderId, order.Money, "USD"))

	address, id, key, methods := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods = address, id, key, methods
	})
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://pay.example.com", "epay_id", "test-key"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	epayIntent := &model.AppCheckout{Actor: "provider-test", IdentitySource: env.user.Source(), UserId: env.user.Id, AppId: env.app.Id, Kind: "topup", PaymentMethod: "alipay", Money: "0", Quota: quota}
	_, err = model.ReserveAppCheckout(epayIntent, "epay-snapshot", amount)
	require.NoError(t, err)
	topup := &model.TopUp{UserId: env.user.Id, AppId: env.app.Id, Amount: amount, Money: 2, TradeNo: epayIntent.OrderId, PaymentProvider: model.PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending}
	require.NoError(t, topup.Insert())
	require.NoError(t, model.VerifyAppCheckoutPayment(epayIntent.OrderId, "2", "CNY"))
	router := gin.New()
	router.GET("/notify", EpayNotify)
	params := url.Values{"pid": {"epay_id"}, "type": {"alipay"}, "money": {"0.01"}, "name": {"item"}, "out_trade_no": {epayIntent.OrderId}, "trade_status": {epay.StatusTradeSuccess}}
	params.Set("sign", epay.MD5String(params.Encode(), "test-key"))
	params.Set("sign_type", "MD5")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest("GET", "/notify?"+params.Encode(), nil))
	assert.Equal(t, "fail", result.Body.String())
	saved, err := model.ReadAppCheckout(env.user.Id, env.app.Id, epayIntent.OrderId)
	require.NoError(t, err)
	assert.Equal(t, "reconciliation_required", saved.State)
	require.NoError(t, env.db.First(topup, topup.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, topup.Status)
}

func TestAppCheckoutRecoveryAndOrderOwnership(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:read api:write")
	require.NoError(t, env.db.Model(env.app).Update("quota_per_credit", 100).Error)
	require.NoError(t, env.db.Model(env.user).Update("quota", 3000000).Error)
	router := gin.New()
	router.POST("/oauth/topup/checkout", OAuthTopupCheckout)
	router.GET("/oauth/orders/:order_id", OAuthAppOrder)
	purchase := func(key, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/oauth/topup/checkout", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+env.token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	first := purchase("recover-purchase-1", `{"amount":2,"payment_method":"balance"}`)
	require.Equal(t, 200, first.Code, first.Body.String())
	var result struct {
		OrderId        string `json:"order_id"`
		Status         string
		Credits        string
		QuotaPerCredit *int64 `json:"quota_per_credit"`
	}
	require.NoError(t, common.Unmarshal(first.Body.Bytes(), &result))
	require.NotEmpty(t, result.OrderId)
	assert.Equal(t, "success", result.Status)
	assert.Equal(t, "10000", result.Credits)
	require.NoError(t, env.db.Model(env.app).Update("quota_per_credit", 1000).Error)
	retry := purchase("recover-purchase-1", `{"amount":2,"payment_method":"balance"}`)
	assert.JSONEq(t, first.Body.String(), retry.Body.String())
	wallet, err := model.ReadAppWallet(env.user.Id, env.app.Id)
	require.NoError(t, err)
	assert.Equal(t, 1000000, wallet.Quota)
	user, err := model.GetUserById(env.user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 2000000, user.Quota)
	conflict := purchase("recover-purchase-1", `{"amount":3,"payment_method":"balance"}`)
	assert.Equal(t, 409, conflict.Code)
	second := purchase("recover-purchase-2", `{"amount":2,"payment_method":"balance"}`)
	require.Equal(t, 200, second.Code, second.Body.String())
	var next struct {
		OrderId string `json:"order_id"`
	}
	require.NoError(t, common.Unmarshal(second.Body.Bytes(), &next))
	assert.NotEqual(t, result.OrderId, next.OrderId)
	queried := accessTokenRequest(router, "GET", "/oauth/orders/"+result.OrderId, env.token, "", "")
	require.Equal(t, 200, queried.Code, queried.Body.String())
	assert.Contains(t, queried.Body.String(), `"status":"success"`)
	assert.Contains(t, queried.Body.String(), `"quota_per_credit":100`)
	assert.Contains(t, queried.Body.String(), `"credits":"10000"`)
	wrongScope, _, err := service.IssueOAuthAccessToken(env.user.Id, env.app.ClientID, "api:write")
	require.NoError(t, err)
	assert.Equal(t, 403, accessTokenRequest(router, "GET", "/oauth/orders/"+result.OrderId, wrongScope, "", "").Code)
	other := &model.User{Username: "other-buyer", Password: "", AffCode: "other-buyer", Status: 1, Role: 1, Group: "default", AuthVersion: 1}
	require.NoError(t, env.db.Create(other).Error)
	otherToken, _, err := service.IssueOAuthAccessToken(other.Id, env.app.ClientID, "api:read")
	require.NoError(t, err)
	leak := accessTokenRequest(router, "GET", "/oauth/orders/"+result.OrderId, otherToken, "", "")
	assert.NotContains(t, leak.Body.String(), `"status":"success"`)
}

func TestAppCreditCachePreservesRatio(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", useRedis), func(t *testing.T) {
			env := setupOAuthPurchaseTestEnv(t, "api:read")
			if useRedis {
				previous := common.RDB
				server := miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: server.Addr()})
				common.RDB, common.RedisEnabled = client, true
				t.Cleanup(func() { common.RedisEnabled = false; common.RDB = previous; require.NoError(t, client.Close()) })
			}
			service.InvalidateOAuthAppCache(env.app.ClientID)
			t.Cleanup(func() { service.InvalidateOAuthAppCache(env.app.ClientID) })
			createOAuthTestProduct(t, env, env.app.Id)
			createOAuthTestPlan(t, env, env.app.Id, 1)
			require.NoError(t, env.db.AutoMigrate(&model.Ability{}, &model.Channel{}, &model.Model{}, &model.Vendor{}))
			for _, ratio := range []any{int64(100), nil} {
				require.NoError(t, env.db.Model(env.app).Update("quota_per_credit", ratio).Error)
				service.InvalidateOAuthAppCache(env.app.ClientID)
				for range 2 {
					c, recorder := newOAuthPurchaseContext(t, http.MethodGet, "/oauth/products", env.token, "")
					OAuthProductList(c)
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					if ratio == nil {
						assert.Contains(t, recorder.Body.String(), `"credits":null`)
					} else {
						assert.Contains(t, recorder.Body.String(), `"credits":"10"`)
					}
					c, recorder = newOAuthPurchaseContext(t, http.MethodGet, "/oauth/subscription/plans", env.token, "")
					OAuthSubscriptionPlans(c)
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					if ratio == nil {
						assert.Contains(t, recorder.Body.String(), `"credits":null`)
					} else {
						assert.Contains(t, recorder.Body.String(), `"credits":"10"`)
					}
					c, recorder = newOAuthPurchaseContext(t, http.MethodGet, "/oauth/pricing", env.token, "")
					OAuthPricing(c)
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					var response map[string]any
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					require.Contains(t, response, "quota_per_credit")
					if ratio == nil {
						assert.Nil(t, response["quota_per_credit"])
					} else {
						assert.Equal(t, float64(100), response["quota_per_credit"])
					}
				}
			}
		})
	}
}

func TestAppSubscriptionSelectedEpayMethod(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:read api:write")
	address, id, key, methods := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods = address, id, key, methods
	})
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://pay.example.com", "epay_id", "test-key"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}, {"type": "wxpay"}}
	plan := createOAuthTestPlan(t, env, env.app.Id, 1)
	require.NoError(t, env.db.Model(plan).Update("currency", "CNY").Error)
	for _, method := range []string{"wxpay", "alipay", "epay", "unconfigured"} {
		t.Run(method, func(t *testing.T) {
			body := fmt.Sprintf(`{"plan_id":%d,"payment_method":%q}`, plan.Id, method)
			var first string
			for attempt := range 2 {
				c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/subscription/checkout", env.token, body)
				c.Request.Header.Set("Idempotency-Key", "selected-"+method)
				OAuthSubscriptionCheckout(c)
				if method == "unconfigured" {
					assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
					assert.Contains(t, recorder.Body.String(), "unsupported_payment_method")
				} else {
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					var response struct {
						PaymentMethod string            `json:"payment_method"`
						Params        map[string]string `json:"params"`
						OrderID       string            `json:"order_id"`
					}
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					expected := method
					if method == "epay" {
						expected = "alipay"
					}
					assert.Equal(t, method, response.PaymentMethod)
					assert.Equal(t, expected, response.Params["type"])
					var orders []model.SubscriptionOrder
					require.NoError(t, env.db.Where("trade_no = ?", response.OrderID).Find(&orders).Error)
					require.Len(t, orders, 1)
					assert.Equal(t, expected, orders[0].PaymentMethod)
				}
				if attempt == 0 {
					first = recorder.Body.String()
				} else {
					assert.JSONEq(t, first, recorder.Body.String())
				}
			}
		})
	}
	var count int64
	require.NoError(t, env.db.Model(&model.SubscriptionOrder{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
}
