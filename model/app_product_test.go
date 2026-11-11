package model

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// legacyBalanceProduct reproduces the pre-fix enabled column for upgrade tests.
type legacyBalanceProduct struct {
	AppProduct
	Enabled bool `gorm:"default:true"`
}

func TestAppBalanceFinancialDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, upgrade := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/upgrade=%t", dialect, upgrade), func(t *testing.T) {
				namespace := fmt.Sprintf("bf_%d", time.Now().UnixNano())
				var driver gorm.Dialector
				switch dialect {
				case "sqlite":
					driver = sqlite.Open("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "balance.db")) + "?_pragma=busy_timeout(5000)")
				case "mysql":
					dsn := os.Getenv("TEST_MYSQL_DSN")
					if dsn == "" {
						t.Skip("TEST_MYSQL_DSN not configured")
					}
					driver = mysql.Open(dsn)
				case "postgres":
					dsn := os.Getenv("TEST_POSTGRES_DSN")
					if dsn == "" {
						t.Skip("TEST_POSTGRES_DSN not configured")
					}
					admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
					require.NoError(t, err)
					require.NoError(t, admin.Exec(`CREATE SCHEMA "`+namespace+`"`).Error)
					t.Cleanup(func() {
						assert.NoError(t, admin.Exec(`DROP SCHEMA "`+namespace+`" CASCADE`).Error)
						connection, err := admin.DB()
						if err == nil {
							assert.NoError(t, connection.Close())
						}
					})
					parsed, err := url.Parse(dsn)
					require.NoError(t, err)
					query := parsed.Query()
					query.Set("search_path", namespace)
					parsed.RawQuery = query.Encode()
					driver = postgres.Open(parsed.String())
				}
				db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: namespace + "_"}})
				require.NoError(t, err)
				previousDB, previousLogDB := DB, LOG_DB
				previousMain, previousLog, previousRedis := common.MainDatabaseType(), common.LogDatabaseType(), common.RedisEnabled
				DB, LOG_DB = db, db
				common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
				common.RedisEnabled = false
				previousBatch := common.BatchUpdateEnabled
				common.BatchUpdateEnabled = false
				t.Cleanup(func() { common.BatchUpdateEnabled = previousBatch })
				initCol()
				models := []any{&User{}, &Log{}, &UserAppWallet{}, &AppProduct{}, &AppProductOrder{}, &TopUp{}, &SubscriptionPlan{}, &SubscriptionOrder{}, &UserSubscription{}}
				connection, err := db.DB()
				require.NoError(t, err)
				connection.SetMaxOpenConns(8)
				t.Cleanup(func() {
					// NamingStrategy restricts cleanup to this test's disposable tables.
					assert.NoError(t, db.Migrator().DropTable(models...))
					DB, LOG_DB = previousDB, previousLogDB
					common.SetDatabaseTypes(previousMain, previousLog)
					common.RedisEnabled = previousRedis
					initCol()
					assert.NoError(t, connection.Close())
				})
				var version string
				versionQuery := "SELECT VERSION()"
				if dialect == "sqlite" {
					versionQuery = "SELECT sqlite_version()"
				}
				require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
				t.Logf("database version: %s", version)
				if upgrade {
					productTable := db.NamingStrategy.TableName("AppProduct")
					require.NoError(t, db.Table(productTable).AutoMigrate(&legacyBalanceProduct{}))
					legacy := &legacyBalanceProduct{AppProduct: AppProduct{Id: 90, AppId: 1, Name: "existing disabled", QuotaAmount: 10}}
					require.NoError(t, db.Table(productTable).Create(legacy).Error)
					require.NoError(t, db.Table(productTable).Where("id = ?", legacy.Id).Update("enabled", false).Error)
				}
				for range 2 {
					require.NoError(t, db.AutoMigrate(models...))
				}
				if upgrade {
					legacy, err := GetAppProductById(90)
					require.NoError(t, err)
					assert.False(t, legacy.Enabled)
					assert.Equal(t, "existing disabled", legacy.Name)
				}
				runAppBalanceFinancialCases(t, db)
			})
		}
	}
}

func runAppBalanceFinancialCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	user := &User{Username: "balance-user", Quota: 1000}
	require.NoError(t, db.Create(user).Error)
	cache := useUserCacheMiniRedis(t)
	cacheKey := getUserCacheKey(user.Id)
	cache.HSet(cacheKey, "Id", fmt.Sprint(user.Id), "CacheSchema", fmt.Sprint(userCacheSchemaVersion), "Quota", "1000")
	product := &AppProduct{AppId: 7, Name: "package", PriceAmount: 0.001, QuotaAmount: 800, Enabled: true, MaxPurchasePerUser: 1}
	require.NoError(t, db.Create(product).Error)
	require.NoError(t, IncreaseUserAppQuota(user.Id, 7, 600))
	initial := getUserAppWallet(t, user.Id, 7)
	require.NoError(t, db.Model(&UserAppWallet{}).Where("id = ?", initial.Id).UpdateColumns(map[string]any{"created_at": 123, "updated_at": 456, "used_quota": 17}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		wallet, err := ensureUserAppWallet(tx, user.Id, 7)
		if err != nil {
			return err
		}
		assert.Equal(t, initial.Id, wallet.Id)
		assert.Equal(t, 600, wallet.Quota)
		assert.Equal(t, 17, wallet.UsedQuota)
		assert.EqualValues(t, 123, wallet.CreatedAt)
		assert.EqualValues(t, 456, wallet.UpdatedAt)
		return nil
	}))
	injected := errors.New("financial write failure")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("balance_order_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "AppProductOrder" {
			tx.AddError(injected)
		}
	}))
	_, err := PurchaseAppProductWithBalance(user.Id, 7, product.Id)
	require.NoError(t, db.Callback().Update().Remove("balance_order_failure"))
	assert.ErrorIs(t, err, injected)
	assert.Equal(t, 600, getUserAppWalletQuota(t, user.Id, 7))
	var count int64
	require.NoError(t, db.Model(&AppProductOrder{}).Count(&count).Error)
	assert.Zero(t, count, "rollback releases the purchase slot")
	order, err := PurchaseAppProductWithBalance(user.Id, 7, product.Id)
	require.NoError(t, err)
	assert.Equal(t, AppProductOrderStatusSuccess, order.Status)
	assert.Equal(t, 900, getUserAppWalletQuota(t, user.Id, 7))
	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "replay", PaymentProviderBalance, 0))
	assert.Equal(t, 900, getUserAppWalletQuota(t, user.Id, 7))
	_, err = PurchaseAppProductWithBalance(user.Id, 7, product.Id)
	assert.ErrorIs(t, err, ErrAppProductPurchaseLimitExceeded)

	disabled := &AppProduct{AppId: 7, Name: "disabled", QuotaAmount: 10, Enabled: false}
	require.NoError(t, db.Create(disabled).Error)
	assert.False(t, disabled.Enabled)
	_, err = PurchaseAppProductWithBalance(user.Id, 7, disabled.Id)
	assert.ErrorIs(t, err, ErrAppProductDisabled)
	require.NoError(t, db.Model(&UserAppWallet{}).Where("user_id = ? AND app_id = ?", user.Id, 7).Update("quota", common.MaxWalletQuota).Error)
	capacityProduct := &AppProduct{AppId: 7, Name: "capacity", PriceAmount: 0.001, QuotaAmount: 501, Enabled: true}
	require.NoError(t, db.Create(capacityProduct).Error)
	_, err = PurchaseAppProductWithBalance(user.Id, 7, capacityProduct.Id)
	assert.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	assert.Equal(t, common.MaxWalletQuota, getUserAppWalletQuota(t, user.Id, 7))
	assert.ErrorIs(t, TransferUserQuotaToApp(user.Id, 7, 1, 100), ErrTopUpQuotaLimitExceeded)
	var storedUser User
	require.NoError(t, db.Where("id = ?", user.Id).First(&storedUser).Error)
	assert.Equal(t, 1000, storedUser.Quota)
	assert.Equal(t, "1000", cache.HGet(cacheKey, "Quota"), "capacity rollback leaves cache unchanged")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("balance_topup_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "TopUp" {
			tx.AddError(injected)
		}
	}))
	err = TransferUserQuotaToApp(user.Id, 8, 1, 100)
	require.NoError(t, db.Callback().Create().Remove("balance_topup_failure"))
	assert.ErrorIs(t, err, injected)
	require.NoError(t, db.Where("id = ?", user.Id).First(&storedUser).Error)
	assert.Equal(t, 1000, storedUser.Quota)
	require.NoError(t, db.Model(&UserAppWallet{}).Where("user_id = ? AND app_id = ?", user.Id, 8).Count(&count).Error)
	assert.Zero(t, count, "new wallet rolls back with failed audit write")
	require.NoError(t, db.Model(&TopUp{}).Count(&count).Error)
	assert.Zero(t, count)
	assert.Equal(t, "1000", cache.HGet(cacheKey, "Quota"), "audit failure leaves cache unchanged")
	common.BatchUpdateEnabled = true
	err = TransferUserQuotaToApp(user.Id, 8, 1, 100)
	common.BatchUpdateEnabled = false
	assert.ErrorContains(t, err, "immediate quota persistence")
	assert.Equal(t, "1000", cache.HGet(cacheKey, "Quota"))
	require.NoError(t, TransferUserQuotaToApp(user.Id, 8, 1, 100))
	assert.Equal(t, "900", cache.HGet(cacheKey, "Quota"), "only committed debit reaches cache")
	assert.Equal(t, 100, getUserAppWalletQuota(t, user.Id, 8))
	require.NoError(t, db.Where("id = ?", user.Id).First(&storedUser).Error)
	assert.Equal(t, 900, storedUser.Quota)
	var topup TopUp
	require.NoError(t, db.First(&topup).Error)
	assert.Equal(t, common.TopUpStatusSuccess, topup.Status)
	assert.NotZero(t, topup.CompleteTime)
	assert.Equal(t, PaymentProviderBalance, topup.PaymentProvider)
	err = TransferUserQuotaToApp(user.Id, 8, 1, 901)
	assert.ErrorContains(t, err, "Insufficient balance")
	assert.Equal(t, 100, getUserAppWalletQuota(t, user.Id, 8))

	for _, existingWallet := range []bool{false, true} {
		appId := 9
		if existingWallet {
			appId = 10
			_, err := EnsureUserAppWallet(user.Id, appId)
			require.NoError(t, err)
		}
		plan := &SubscriptionPlan{AppId: appId, Title: "free limited", PriceAmount: 0, Enabled: true, MaxPurchasePerUser: 1, DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
		require.NoError(t, db.Create(plan).Error)
		InvalidateSubscriptionPlanCache(plan.Id)
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Go(func() { <-start; errs[i] = PurchaseSubscriptionWithBalance(user.Id, plan.Id, appId) })
		}
		close(start)
		wg.Wait()
		successes := 0
		for _, err := range errs {
			if err == nil {
				successes++
			} else {
				assert.ErrorContains(t, err, "Purchase limit for this plan has been reached")
			}
		}
		assert.Equal(t, 1, successes)
		require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}

	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			// Establish an old snapshot before another connection wins the insert.
			var missing int64
			if err := tx.Model(&UserAppWallet{}).Where("user_id = ? AND app_id = ?", user.Id, 12).Count(&missing).Error; err != nil {
				return err
			}
			require.Zero(t, missing)
			winner := &UserAppWallet{UserId: user.Id, AppId: 12, Quota: 35, UsedQuota: 4}
			if err := db.Create(winner).Error; err != nil {
				return err
			}
			wallet, err := ensureUserAppWallet(tx, user.Id, 12)
			if err != nil {
				return err
			}
			assert.Equal(t, *winner, *wallet, "lookup must see the committed winner, not the old snapshot")
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}))
	}

	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			<-start
			errs[i] = db.Transaction(func(tx *gorm.DB) error { _, err := ensureUserAppWallet(tx, user.Id, 11); return err })
		})
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, db.Model(&UserAppWallet{}).Where("user_id = ? AND app_id = ?", user.Id, 11).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assert.Error(t, db.Create(&UserAppWallet{UserId: user.Id, AppId: 11}).Error, "composite uniqueness survives migration")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("wallet_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "UserAppWallet" {
			tx.AddError(injected)
		}
	}))
	_, err = EnsureUserAppWallet(user.Id, 11)
	require.NoError(t, db.Callback().Create().Remove("wallet_failure"))
	assert.ErrorIs(t, err, injected, "an existing wallet must not hide real insert errors")
}

func mustCreateAppProduct(t *testing.T, appId int, maxPurchasePerUser int) *AppProduct {
	t.Helper()
	product := &AppProduct{
		AppId:              appId,
		Name:               "test product",
		PriceAmount:        10,
		Currency:           "USD",
		QuotaAmount:        1000,
		Enabled:            true,
		MaxPurchasePerUser: maxPurchasePerUser,
	}
	require.NoError(t, DB.Create(product).Error)
	return product
}

func TestCreateAppProductOrderEnforcesPurchaseLimit(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 1, 1)

	order, err := CreateAppProductOrder(7, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)
	require.NotEmpty(t, order.TradeNo)
	assert.True(t, strings.HasPrefix(order.TradeNo, AppProductOrderTradeNoPrefix))
	assert.Equal(t, AppProductOrderStatusPending, order.Status)
	assert.Equal(t, product.PriceAmount, order.Money)
	assert.Equal(t, product.PriceAmount, order.PriceAmount)
	assert.Equal(t, product.Currency, order.Currency)
	assert.Equal(t, product.QuotaAmount, order.QuotaAmount)
	assert.NotZero(t, order.CreateTime)

	// A second order is rejected while the first one is still pending.
	_, err = CreateAppProductOrder(7, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductPurchaseLimitExceeded)

	// A successful purchase keeps the slot consumed.
	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount))
	_, err = CreateAppProductOrder(7, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductPurchaseLimitExceeded)

	// Generated trade numbers are unique across users.
	otherOrder, err := CreateAppProductOrder(8, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)
	assert.NotEqual(t, order.TradeNo, otherOrder.TradeNo)
}

func TestExpiredAppProductOrderReleasesPurchaseSlot(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 2, 1)

	order, err := CreateAppProductOrder(9, 2, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	require.NoError(t, UpdateAppProductOrderStatus(order.TradeNo, PaymentProviderWaffoPancake, AppProductOrderStatusExpired))
	_, err = CreateAppProductOrder(9, 2, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	// An expired order can no longer be completed.
	require.ErrorIs(t,
		CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount),
		ErrAppProductOrderStatusInvalid,
	)
}

func TestCreateAppProductOrderRejectsDisabledAndCrossAppProducts(t *testing.T) {
	setupAppModelTestDB(t)

	disabled := mustCreateAppProduct(t, 3, 0)
	require.NoError(t, DB.Model(&AppProduct{}).Where("id = ?", disabled.Id).Update("enabled", false).Error)
	_, err := CreateAppProductOrder(10, 3, disabled.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductDisabled)

	otherAppProduct := mustCreateAppProduct(t, 4, 0)
	_, err = CreateAppProductOrder(10, 3, otherAppProduct.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductCrossApp)

	_, err = CreateAppProductOrder(10, 3, 999999, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductNotFound)
}

func TestCompleteAppProductOrderCreditsExactlyOnce(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 5, 0)

	order, err := CreateAppProductOrder(11, 5, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "payload-1", PaymentProviderWaffoPancake, product.PriceAmount))
	assert.EqualValues(t, product.QuotaAmount, getUserAppWalletQuota(t, 11, 5))

	completed := GetAppProductOrderByTradeNo(order.TradeNo)
	require.NotNil(t, completed)
	assert.Equal(t, AppProductOrderStatusSuccess, completed.Status)
	assert.NotZero(t, completed.CompleteTime)
	assert.Equal(t, "payload-1", completed.ProviderPayload)

	// A duplicate callback must not credit again.
	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "payload-2", PaymentProviderWaffoPancake, product.PriceAmount))
	assert.EqualValues(t, product.QuotaAmount, getUserAppWalletQuota(t, 11, 5))
}

func TestCompleteAppProductOrderRejectsAmountAndProviderMismatch(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 6, 0)

	order, err := CreateAppProductOrder(12, 6, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	require.ErrorIs(t,
		CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount-0.01),
		ErrAppProductOrderAmountMismatch,
	)
	assert.Zero(t, getUserAppWalletQuota(t, 12, 6))

	require.ErrorIs(t,
		CompleteAppProductOrder(order.TradeNo, "", PaymentProviderStripe, product.PriceAmount),
		ErrPaymentMethodMismatch,
	)
	assert.Zero(t, getUserAppWalletQuota(t, 12, 6))

	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount))
	assert.EqualValues(t, product.QuotaAmount, getUserAppWalletQuota(t, 12, 6))
}
