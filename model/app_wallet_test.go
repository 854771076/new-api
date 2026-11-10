package model

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func openAppModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "app-models.db")) + "?_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		_ = sqlDB.Close()
	})
	return db
}

func setupAppModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openAppModelTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&SubscriptionPlan{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&TopUp{},
		&AppProduct{},
		&AppProductOrder{},
		&UserAppWallet{},
	))
	return db
}

func TestAppIdDefaultsToZeroOnLegacyModels(t *testing.T) {
	setupAppModelTestDB(t)

	plan := &SubscriptionPlan{Title: "legacy plan", PriceAmount: 1, DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
	require.NoError(t, DB.Create(plan).Error)
	order := &SubscriptionOrder{UserId: 1, PlanId: plan.Id, TradeNo: "legacy-sub-order", Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(order).Error)
	sub := &UserSubscription{UserId: 1, PlanId: plan.Id, AmountTotal: 10, EndTime: 1, Status: "active"}
	require.NoError(t, DB.Create(sub).Error)
	topUp := &TopUp{UserId: 1, Amount: 1, Money: 1, TradeNo: "legacy-topup-order", Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(topUp).Error)

	reloadedPlan := SubscriptionPlan{}
	require.NoError(t, DB.Where("id = ?", plan.Id).First(&reloadedPlan).Error)
	assert.Zero(t, reloadedPlan.AppId)

	reloadedOrder := SubscriptionOrder{}
	require.NoError(t, DB.Where("id = ?", order.Id).First(&reloadedOrder).Error)
	assert.Zero(t, reloadedOrder.AppId)

	reloadedSub := UserSubscription{}
	require.NoError(t, DB.Where("id = ?", sub.Id).First(&reloadedSub).Error)
	assert.Zero(t, reloadedSub.AppId)

	reloadedTopUp := TopUp{}
	require.NoError(t, DB.Where("id = ?", topUp.Id).First(&reloadedTopUp).Error)
	assert.Zero(t, reloadedTopUp.AppId)
}

func TestEnsureSubscriptionPlanTableSQLiteAddsAppId(t *testing.T) {
	t.Run("legacy table keeps data and gets app_id default 0", func(t *testing.T) {
		db := openAppModelTestDB(t)
		require.NoError(t, db.Exec(`CREATE TABLE subscription_plans (
			id integer,
			title varchar(128) NOT NULL,
			price_amount decimal(10,6) NOT NULL,
			PRIMARY KEY (id)
		)`).Error)
		require.NoError(t, db.Exec("INSERT INTO subscription_plans (id, title, price_amount) VALUES (1, 'legacy', 1)").Error)

		require.NoError(t, ensureSubscriptionPlanTableSQLite())
		assert.True(t, db.Migrator().HasColumn(&SubscriptionPlan{}, "app_id"))

		var appId int
		require.NoError(t, db.Raw("SELECT app_id FROM subscription_plans WHERE id = 1").Scan(&appId).Error)
		assert.Zero(t, appId)

		// The migration must be idempotent.
		require.NoError(t, ensureSubscriptionPlanTableSQLite())
	})

	t.Run("fresh table is created with app_id", func(t *testing.T) {
		db := openAppModelTestDB(t)
		require.NoError(t, ensureSubscriptionPlanTableSQLite())
		assert.True(t, db.Migrator().HasColumn(&SubscriptionPlan{}, "app_id"))
	})
}

func TestEnsureUserAppWalletIsIdempotentAndUnique(t *testing.T) {
	setupAppModelTestDB(t)

	first, err := EnsureUserAppWallet(11, 5)
	require.NoError(t, err)
	assert.Zero(t, first.Quota)

	second, err := EnsureUserAppWallet(11, 5)
	require.NoError(t, err)
	assert.Equal(t, first.Id, second.Id)

	// The unique index rejects a second row for the same user/app pair.
	require.Error(t, DB.Create(&UserAppWallet{UserId: 11, AppId: 5}).Error)

	again, err := EnsureUserAppWallet(11, 5)
	require.NoError(t, err)
	assert.Equal(t, first.Id, again.Id)

	const workers = 8
	ids := make([]int, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			wallet, ensureErr := EnsureUserAppWallet(22, 5)
			errs[i] = ensureErr
			if ensureErr == nil {
				ids[i] = wallet.Id
			}
		})
	}
	wg.Wait()

	for i := range workers {
		require.NoError(t, errs[i])
		assert.Equal(t, ids[0], ids[i])
	}
	var count int64
	require.NoError(t, DB.Model(&UserAppWallet{}).Where("user_id = ? AND app_id = ?", 22, 5).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestUserAppWalletQuotaOperations(t *testing.T) {
	setupAppModelTestDB(t)

	require.NoError(t, IncreaseUserAppQuota(31, 7, 100))
	quota, err := GetUserAppQuota(31, 7)
	require.NoError(t, err)
	assert.Equal(t, 100, quota)

	reserved, err := TryReserveUserAppQuota(31, 7, 60)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 40, getUserAppWalletQuota(t, 31, 7))

	reserved, err = TryReserveUserAppQuota(31, 7, 41)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 40, getUserAppWalletQuota(t, 31, 7))

	reserved, err = TryReserveUserAppQuota(31, 7, 0)
	require.NoError(t, err)
	assert.True(t, reserved)

	_, err = TryReserveUserAppQuota(31, 7, -1)
	assert.Error(t, err)

	quota, err = GetUserAppQuota(404, 7)
	require.NoError(t, err)
	assert.Zero(t, quota)

	require.NoError(t, IncreaseUserAppUsedQuota(31, 7, 30))
	assert.Equal(t, 30, getUserAppWallet(t, 31, 7).UsedQuota)
	assert.Equal(t, 40, getUserAppWalletQuota(t, 31, 7))
	assert.Error(t, IncreaseUserAppUsedQuota(31, 7, -1))

	// Settlement may overdraw the pool, matching DecreaseUserQuota semantics.
	require.NoError(t, DecreaseUserAppQuota(31, 7, 60))
	assert.Equal(t, -20, getUserAppWalletQuota(t, 31, 7))

	_, err = EnsureUserAppWallet(31, 0)
	assert.ErrorIs(t, err, ErrInvalidAppId)
	_, err = GetUserAppQuota(31, 0)
	assert.ErrorIs(t, err, ErrInvalidAppId)
	_, err = TryReserveUserAppQuota(31, 0, 1)
	assert.ErrorIs(t, err, ErrInvalidAppId)
	assert.ErrorIs(t, IncreaseUserAppQuota(31, 0, 1), ErrInvalidAppId)
	assert.ErrorIs(t, DecreaseUserAppQuota(31, 0, 1), ErrInvalidAppId)
	assert.ErrorIs(t, IncreaseUserAppUsedQuota(31, 0, 1), ErrInvalidAppId)
}

func TestIncreaseUserAppQuotaEnforcesWalletLimit(t *testing.T) {
	setupAppModelTestDB(t)

	require.NoError(t, IncreaseUserAppQuota(41, 3, common.MaxWalletQuota))
	assert.ErrorIs(t, IncreaseUserAppQuota(41, 3, 1), ErrWalletQuotaLimitExceeded)
	assert.Equal(t, common.MaxWalletQuota, getUserAppWalletQuota(t, 41, 3))
	assert.Error(t, IncreaseUserAppQuota(41, 3, common.MaxWalletQuota+1))
}

func TestAppProductOrderTradeNoUnique(t *testing.T) {
	setupAppModelTestDB(t)

	first := &AppProduct{AppId: 1, Name: "first", PriceAmount: 1, Currency: "USD", QuotaAmount: 100, SortOrder: 2}
	require.NoError(t, DB.Create(first).Error)
	assert.NotZero(t, first.CreatedAt)

	disabled := &AppProduct{AppId: 1, Name: "disabled", PriceAmount: 1, Currency: "USD", QuotaAmount: 100}
	require.NoError(t, DB.Create(disabled).Error)
	require.NoError(t, DB.Model(&AppProduct{}).Where("id = ?", disabled.Id).Update("enabled", false).Error)

	otherApp := &AppProduct{AppId: 2, Name: "other", PriceAmount: 1, Currency: "USD", QuotaAmount: 100}
	require.NoError(t, DB.Create(otherApp).Error)

	product, err := GetAppProductById(first.Id)
	require.NoError(t, err)
	assert.Equal(t, "first", product.Name)
	_, err = GetAppProductById(0)
	assert.Error(t, err)

	enabled, err := ListEnabledAppProducts(1)
	require.NoError(t, err)
	require.Len(t, enabled, 1)
	assert.Equal(t, first.Id, enabled[0].Id)
	enabled, err = ListEnabledAppProducts(2)
	require.NoError(t, err)
	require.Len(t, enabled, 1)
	assert.Equal(t, otherApp.Id, enabled[0].Id)

	order := &AppProductOrder{
		UserId:          1,
		AppId:           1,
		ProductId:       first.Id,
		Money:           1,
		TradeNo:         "app-product-trade-1",
		PaymentProvider: PaymentProviderStripe,
		Status:          AppProductOrderStatusPending,
	}
	require.NoError(t, order.Insert())
	assert.NotZero(t, order.CreateTime)

	duplicate := &AppProductOrder{UserId: 2, AppId: 1, ProductId: first.Id, Money: 1, TradeNo: order.TradeNo}
	assert.Error(t, duplicate.Insert())

	loaded := GetAppProductOrderByTradeNo(order.TradeNo)
	require.NotNil(t, loaded)
	assert.Equal(t, order.Id, loaded.Id)
	assert.Nil(t, GetAppProductOrderByTradeNo("missing-trade-no"))
	assert.Nil(t, GetAppProductOrderByTradeNo(""))

	loaded.Status = AppProductOrderStatusSuccess
	loaded.CompleteTime = common.GetTimestamp()
	require.NoError(t, loaded.Update())

	reloaded := GetAppProductOrderByTradeNo(order.TradeNo)
	require.NotNil(t, reloaded)
	assert.Equal(t, AppProductOrderStatusSuccess, reloaded.Status)
	assert.NotZero(t, reloaded.CompleteTime)
}

func TestAppModelsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
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
				driver = postgres.Open(dsn)
			}

			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "app_models_test_"}})
			require.NoError(t, err)
			previousDB, previousLogDB := DB, LOG_DB
			previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
			DB, LOG_DB = db, db
			common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
			initCol()
			models := []any{
				&SubscriptionPlan{},
				&SubscriptionOrder{},
				&UserSubscription{},
				&TopUp{},
				&AppProduct{},
				&AppProductOrder{},
				&UserAppWallet{},
			}
			t.Cleanup(func() {
				_ = db.Migrator().DropTable(models...)
				DB, LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMainType, previousLogType)
				initCol()
				if sqlDB, dbErr := db.DB(); dbErr == nil {
					_ = sqlDB.Close()
				}
			})

			_ = db.Migrator().DropTable(models...)
			for range 2 {
				require.NoError(t, db.AutoMigrate(models...))
			}
			for _, model := range models {
				assert.True(t, db.Migrator().HasColumn(model, "app_id"))
			}

			plan := &SubscriptionPlan{Title: dialect + " plan", PriceAmount: 1, DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
			require.NoError(t, db.Create(plan).Error)
			order := &SubscriptionOrder{UserId: 1, PlanId: plan.Id, TradeNo: dialect + "-sub-order", Status: common.TopUpStatusPending}
			require.NoError(t, db.Create(order).Error)
			sub := &UserSubscription{UserId: 1, PlanId: plan.Id, AmountTotal: 1, EndTime: 1, Status: "active"}
			require.NoError(t, db.Create(sub).Error)
			topUp := &TopUp{UserId: 1, Amount: 1, Money: 1, TradeNo: dialect + "-topup-order", Status: common.TopUpStatusPending}
			require.NoError(t, db.Create(topUp).Error)

			// Simulate upgrading a released schema that lacks app_id while it
			// already holds rows. SQLite is covered by the dedicated migration
			// test above.
			if dialect != "sqlite" {
				legacyModels := []any{&SubscriptionPlan{}, &SubscriptionOrder{}, &UserSubscription{}, &TopUp{}}
				for _, model := range legacyModels {
					require.NoError(t, db.Migrator().DropColumn(model, "app_id"))
					assert.False(t, db.Migrator().HasColumn(model, "app_id"))
				}
				for range 2 {
					require.NoError(t, db.AutoMigrate(models...))
				}
				for _, model := range legacyModels {
					assert.True(t, db.Migrator().HasColumn(model, "app_id"))
				}
			}

			reloadedPlan := SubscriptionPlan{}
			require.NoError(t, db.Where("id = ?", plan.Id).First(&reloadedPlan).Error)
			assert.Zero(t, reloadedPlan.AppId)
			reloadedOrder := SubscriptionOrder{}
			require.NoError(t, db.Where("id = ?", order.Id).First(&reloadedOrder).Error)
			assert.Zero(t, reloadedOrder.AppId)
			reloadedSub := UserSubscription{}
			require.NoError(t, db.Where("id = ?", sub.Id).First(&reloadedSub).Error)
			assert.Zero(t, reloadedSub.AppId)
			reloadedTopUp := TopUp{}
			require.NoError(t, db.Where("id = ?", topUp.Id).First(&reloadedTopUp).Error)
			assert.Zero(t, reloadedTopUp.AppId)

			wallet, err := EnsureUserAppWallet(1, 1)
			require.NoError(t, err)
			again, err := EnsureUserAppWallet(1, 1)
			require.NoError(t, err)
			assert.Equal(t, wallet.Id, again.Id)
		})
	}
}

func TestListEnabledSubscriptionPlansAppScope(t *testing.T) {
	setupAppModelTestDB(t)

	createPlan := func(appId int, title string, sortOrder int) *SubscriptionPlan {
		plan := &SubscriptionPlan{
			AppId:         appId,
			Title:         title,
			PriceAmount:   1,
			DurationUnit:  SubscriptionDurationMonth,
			DurationValue: 1,
			SortOrder:     sortOrder,
		}
		require.NoError(t, DB.Create(plan).Error)
		InvalidateSubscriptionPlanCache(plan.Id)
		return plan
	}
	platformPlan := createPlan(0, "platform", 1)
	appPlan := createPlan(5, "app five", 3)
	disabledPlan := createPlan(5, "app five disabled", 2)
	require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", disabledPlan.Id).Update("enabled", false).Error)
	otherAppPlan := createPlan(6, "app six", 1)

	platformPlans, err := ListEnabledSubscriptionPlans(0)
	require.NoError(t, err)
	require.Len(t, platformPlans, 1)
	assert.Equal(t, platformPlan.Id, platformPlans[0].Id)

	appPlans, err := ListEnabledSubscriptionPlans(5)
	require.NoError(t, err)
	require.Len(t, appPlans, 1)
	assert.Equal(t, appPlan.Id, appPlans[0].Id)
	assert.NotEqual(t, otherAppPlan.Id, appPlans[0].Id)

	otherPlans, err := ListEnabledSubscriptionPlans(6)
	require.NoError(t, err)
	require.Len(t, otherPlans, 1)
	assert.Equal(t, otherAppPlan.Id, otherPlans[0].Id)

	_, err = ListEnabledSubscriptionPlans(-1)
	assert.ErrorIs(t, err, ErrInvalidAppId)
}

func TestPreConsumeUserSubscriptionAppIsolation(t *testing.T) {
	setupAppModelTestDB(t)

	plan := &SubscriptionPlan{
		AppId:         5,
		Title:         "app five plan",
		PriceAmount:   1,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	now := common.GetTimestamp()
	sub := &UserSubscription{
		AppId:               5,
		UserId:              77,
		PlanId:              plan.Id,
		AmountTotal:         1000,
		StartTime:           now,
		EndTime:             now + 3600,
		Status:              "active",
		AllowWalletOverflow: true,
	}
	require.NoError(t, DB.Create(sub).Error)

	// A request from another app must not touch app 5's subscription.
	_, err := PreConsumeUserSubscription("req-app-6", 77, 6, "gpt-test", 0, 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no active subscription")

	hasApp5, err := HasActiveUserSubscription(77, 5)
	require.NoError(t, err)
	assert.True(t, hasApp5)
	hasPlatform, err := HasActiveUserSubscription(77, 0)
	require.NoError(t, err)
	assert.False(t, hasPlatform)

	res, err := PreConsumeUserSubscription("req-app-5", 77, 5, "gpt-test", 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 100, int(res.PreConsumed))

	reloaded := UserSubscription{}
	require.NoError(t, DB.Where("id = ?", sub.Id).First(&reloaded).Error)
	assert.EqualValues(t, 100, reloaded.AmountUsed)
}

func TestPurchaseSubscriptionWithBalanceAppScope(t *testing.T) {
	db := setupAppModelTestDB(t)

	plan := &SubscriptionPlan{
		AppId:         5,
		Title:         "app five plan",
		PriceAmount:   1,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	require.NoError(t, db.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	const userId = 88
	require.NoError(t, IncreaseUserAppQuota(userId, 5, 1_000_000))
	require.NoError(t, IncreaseUserAppQuota(userId, 6, 1_000_000))

	// Buying app 5's plan through app 6 is rejected before any order is created.
	err := PurchaseSubscriptionWithBalance(userId, plan.Id, 6)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to this app")
	var orderCount int64
	require.NoError(t, db.Model(&SubscriptionOrder{}).Count(&orderCount).Error)
	assert.Zero(t, orderCount)

	require.NoError(t, PurchaseSubscriptionWithBalance(userId, plan.Id, 5))

	requiredQuota := int(math.Ceil(plan.PriceAmount * common.QuotaPerUnit))
	assert.Equal(t, 1_000_000-requiredQuota, getUserAppWalletQuota(t, userId, 5))
	assert.Equal(t, 1_000_000, getUserAppWalletQuota(t, userId, 6))

	var order SubscriptionOrder
	require.NoError(t, db.Order("id desc").First(&order).Error)
	assert.Equal(t, 5, order.AppId)
	assert.Equal(t, common.TopUpStatusSuccess, order.Status)

	var created UserSubscription
	require.NoError(t, db.Order("id desc").First(&created).Error)
	assert.Equal(t, 5, created.AppId)
	assert.Equal(t, plan.Id, created.PlanId)
}

func getUserAppWallet(t *testing.T, userId, appId int) UserAppWallet {
	t.Helper()
	var wallet UserAppWallet
	require.NoError(t, DB.Where("user_id = ? AND app_id = ?", userId, appId).First(&wallet).Error)
	return wallet
}

func getUserAppWalletQuota(t *testing.T, userId, appId int) int {
	t.Helper()
	return getUserAppWallet(t, userId, appId).Quota
}
