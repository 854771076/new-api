package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// planOptions holds the shared flags of `plan create` and `plan update`.
type planOptions struct {
	appID                   int
	title                   string
	subtitle                string
	price                   float64
	currency                string
	durationUnit            string
	durationValue           int
	customSeconds           int64
	enabled                 bool
	sortOrder               int
	allowBalancePay         bool
	allowWalletOverflow     bool
	stripePriceID           string
	creemProductID          string
	waffoPancakeProductID   string
	maxPurchasePerUser      int
	upgradeGroup            string
	downgradeGroup          string
	totalAmount             int64
	quotaResetPeriod        string
	quotaResetCustomSeconds int64
}

func addPlanFlags(cmd *cobra.Command, o *planOptions) {
	flags := cmd.Flags()
	flags.IntVar(&o.appID, "app-id", 0, "app id the plan belongs to; 0 means platform-wide")
	flags.StringVar(&o.title, "title", "", "plan title")
	flags.StringVar(&o.subtitle, "subtitle", "", "plan subtitle")
	flags.Float64Var(&o.price, "price", 0, "display price amount")
	flags.StringVar(&o.currency, "currency", "USD", "ISO currency code")
	flags.StringVar(&o.durationUnit, "duration-unit", "month", "subscription duration unit: year|month|day|hour|custom")
	flags.IntVar(&o.durationValue, "duration-value", 1, "subscription duration value")
	flags.Int64Var(&o.customSeconds, "custom-seconds", 0, "duration in seconds when --duration-unit=custom")
	flags.BoolVar(&o.enabled, "enabled", true, "whether the plan can be purchased")
	flags.IntVar(&o.sortOrder, "sort-order", 0, "display order")
	flags.BoolVar(&o.allowBalancePay, "allow-balance-pay", true, "allow paying with wallet balance")
	flags.BoolVar(&o.allowWalletOverflow, "allow-wallet-overflow", true, "fall back to wallet balance after quota is exhausted")
	flags.StringVar(&o.stripePriceID, "stripe-price-id", "", "Stripe price id")
	flags.StringVar(&o.creemProductID, "creem-product-id", "", "Creem product id")
	flags.StringVar(&o.waffoPancakeProductID, "waffo-pancake-product-id", "", "Waffo Pancake product id")
	flags.IntVar(&o.maxPurchasePerUser, "max-purchase-per-user", 0, "purchase limit per user; 0 means unlimited")
	flags.StringVar(&o.upgradeGroup, "upgrade-group", "", "user group applied after purchase")
	flags.StringVar(&o.downgradeGroup, "downgrade-group", "", "user group restored on expiry")
	flags.Int64Var(&o.totalAmount, "total-amount", 0, "total quota in quota units; 0 means unlimited")
	flags.StringVar(&o.quotaResetPeriod, "quota-reset-period", "never", "quota reset period: never|daily|weekly|monthly|custom")
	flags.Int64Var(&o.quotaResetCustomSeconds, "quota-reset-custom-seconds", 0, "reset cycle in seconds when --quota-reset-period=custom")
}

// planBody builds the {"plan": {...}} request body for create.
func planBody(o *planOptions) map[string]any {
	return map[string]any{
		"app_id":                     o.appID,
		"title":                      strings.TrimSpace(o.title),
		"subtitle":                   strings.TrimSpace(o.subtitle),
		"price_amount":               o.price,
		"currency":                   o.currency,
		"duration_unit":              o.durationUnit,
		"duration_value":             o.durationValue,
		"custom_seconds":             o.customSeconds,
		"enabled":                    o.enabled,
		"sort_order":                 o.sortOrder,
		"allow_balance_pay":          o.allowBalancePay,
		"allow_wallet_overflow":      o.allowWalletOverflow,
		"stripe_price_id":            o.stripePriceID,
		"creem_product_id":           o.creemProductID,
		"waffo_pancake_product_id":   o.waffoPancakeProductID,
		"max_purchase_per_user":      o.maxPurchasePerUser,
		"upgrade_group":              o.upgradeGroup,
		"downgrade_group":            o.downgradeGroup,
		"total_amount":               o.totalAmount,
		"quota_reset_period":         o.quotaResetPeriod,
		"quota_reset_custom_seconds": o.quotaResetCustomSeconds,
	}
}

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Short: "Manage subscription plans"}
	var p, pageSize, appID int
	list := &cobra.Command{
		Use:   "list",
		Short: "List subscription plans",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if appID < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id must not be negative"}
			}
			path := fmt.Sprintf("/api/subscription/admin/plans?p=%d&page_size=%d", p, pageSize)
			if appID > 0 {
				path += "&app_id=" + strconv.Itoa(appID)
			}
			return runData(cmd, http.MethodGet, path, nil)
		},
	}
	addPageFlags(list, &p, &pageSize)
	list.Flags().IntVar(&appID, "app-id", 0, "only list plans of this app; empty lists every app")

	var createOpts planOptions
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a subscription plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if createOpts.appID < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id must not be negative"}
			}
			if strings.TrimSpace(createOpts.title) == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--title must not be empty"}
			}
			if createOpts.price < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--price must not be negative"}
			}
			if createOpts.maxPurchasePerUser < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--max-purchase-per-user must not be negative"}
			}
			if createOpts.totalAmount < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--total-amount must not be negative"}
			}
			return runData(cmd, http.MethodPost, "/api/subscription/admin/plans", map[string]any{"plan": planBody(&createOpts)})
		},
	}
	addPlanFlags(create, &createOpts)
	_ = create.MarkFlagRequired("title")

	var updateOpts planOptions
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a subscription plan; unpassed fields keep their current value",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := map[string]any{}
			for _, field := range []struct {
				flag  string
				key   string
				value any
			}{
				{"app-id", "app_id", updateOpts.appID},
				{"title", "title", strings.TrimSpace(updateOpts.title)},
				{"subtitle", "subtitle", strings.TrimSpace(updateOpts.subtitle)},
				{"price", "price_amount", updateOpts.price},
				{"currency", "currency", updateOpts.currency},
				{"duration-unit", "duration_unit", updateOpts.durationUnit},
				{"duration-value", "duration_value", updateOpts.durationValue},
				{"custom-seconds", "custom_seconds", updateOpts.customSeconds},
				{"enabled", "enabled", updateOpts.enabled},
				{"sort-order", "sort_order", updateOpts.sortOrder},
				{"allow-balance-pay", "allow_balance_pay", updateOpts.allowBalancePay},
				{"allow-wallet-overflow", "allow_wallet_overflow", updateOpts.allowWalletOverflow},
				{"stripe-price-id", "stripe_price_id", updateOpts.stripePriceID},
				{"creem-product-id", "creem_product_id", updateOpts.creemProductID},
				{"waffo-pancake-product-id", "waffo_pancake_product_id", updateOpts.waffoPancakeProductID},
				{"max-purchase-per-user", "max_purchase_per_user", updateOpts.maxPurchasePerUser},
				{"upgrade-group", "upgrade_group", updateOpts.upgradeGroup},
				{"downgrade-group", "downgrade_group", updateOpts.downgradeGroup},
				{"total-amount", "total_amount", updateOpts.totalAmount},
				{"quota-reset-period", "quota_reset_period", updateOpts.quotaResetPeriod},
				{"quota-reset-custom-seconds", "quota_reset_custom_seconds", updateOpts.quotaResetCustomSeconds},
			} {
				if cmd.Flags().Changed(field.flag) {
					changed[field.key] = field.value
				}
			}
			if len(changed) == 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "at least one plan field must be provided"}
			}
			if cmd.Flags().Changed("app-id") && updateOpts.appID < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id must not be negative"}
			}
			if cmd.Flags().Changed("title") && strings.TrimSpace(updateOpts.title) == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--title must not be empty"}
			}
			if cmd.Flags().Changed("price") && updateOpts.price < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--price must not be negative"}
			}
			if cmd.Flags().Changed("max-purchase-per-user") && updateOpts.maxPurchasePerUser < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--max-purchase-per-user must not be negative"}
			}
			if cmd.Flags().Changed("total-amount") && updateOpts.totalAmount < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--total-amount must not be negative"}
			}
			// The plan list endpoint has no per-plan GET, so read the current
			// record from the list and overlay the changed flags before PUTting
			// the complete object (the API replaces every field on PUT).
			wantID, _ := strconv.Atoi(args[0])
			c, err := newClient()
			if err != nil {
				return err
			}
			data, err := c.Do(http.MethodGet, "/api/subscription/admin/plans", nil)
			if err != nil {
				return mapClientErr(err)
			}
			var plans []struct {
				Plan map[string]any `json:"plan"`
			}
			if err := json.Unmarshal(data, &plans); err != nil {
				return generalErr("unexpected plans response: %s", string(data))
			}
			var plan map[string]any
			for _, p := range plans {
				if v, ok := p.Plan["id"].(float64); ok && int(v) == wantID {
					plan = p.Plan
					break
				}
			}
			if plan == nil {
				return &CommandError{ExitCode: ExitGeneral, Code: "not_found", Message: fmt.Sprintf("plan %d not found", wantID)}
			}
			for key, value := range changed {
				plan[key] = value
			}
			return runData(cmd, http.MethodPut, "/api/subscription/admin/plans/"+url.PathEscape(args[0]), map[string]any{"plan": plan})
		},
	}
	addPlanFlags(update, &updateOpts)

	cmd.AddCommand(list, create, update)
	return cmd
}

// appProductOptions holds the shared flags of `product create` and
// `product update`.
type appProductOptions struct {
	appID                 int
	name                  string
	description           string
	price                 float64
	currency              string
	quota                 int64
	enabled               bool
	sortOrder             int
	maxPurchasePerUser    int
	stripePriceID         string
	creemProductID        string
	waffoPancakeProductID string
}

func addAppProductFlags(cmd *cobra.Command, o *appProductOptions) {
	flags := cmd.Flags()
	flags.IntVar(&o.appID, "app-id", 0, "app id the product belongs to")
	flags.StringVar(&o.name, "name", "", "product name")
	flags.StringVar(&o.description, "description", "", "product description")
	flags.Float64Var(&o.price, "price", 0, "price amount with two decimal places")
	flags.StringVar(&o.currency, "currency", "USD", "ISO currency code")
	flags.Int64Var(&o.quota, "quota", 0, "quota credited after purchase")
	flags.BoolVar(&o.enabled, "enabled", true, "whether the product can be purchased")
	flags.IntVar(&o.sortOrder, "sort-order", 0, "display order")
	flags.IntVar(&o.maxPurchasePerUser, "max-purchase-per-user", 0, "purchase limit per user; 0 means unlimited")
	flags.StringVar(&o.stripePriceID, "stripe-price-id", "", "Stripe price id")
	flags.StringVar(&o.creemProductID, "creem-product-id", "", "Creem product id")
	flags.StringVar(&o.waffoPancakeProductID, "waffo-pancake-product-id", "", "Waffo Pancake product id")
}

func newProductCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "product", Short: "Manage app products and orders"}

	var listPage, listPageSize, listAppID int
	list := &cobra.Command{
		Use:   "list",
		Short: "List app products",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if listAppID < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id must not be negative"}
			}
			path := fmt.Sprintf("/api/app-products?p=%d&page_size=%d", listPage, listPageSize)
			if listAppID > 0 {
				path += "&app_id=" + strconv.Itoa(listAppID)
			}
			return runData(cmd, http.MethodGet, path, nil)
		},
	}
	addPageFlags(list, &listPage, &listPageSize)
	list.Flags().IntVar(&listAppID, "app-id", 0, "only list products of this app; empty lists every app")

	var createOpts appProductOptions
	create := &cobra.Command{
		Use:   "create",
		Short: "Create an app product",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if createOpts.appID <= 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id must be a positive integer"}
			}
			if strings.TrimSpace(createOpts.name) == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--name must not be empty"}
			}
			if createOpts.price < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--price must not be negative"}
			}
			if createOpts.quota < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--quota must not be negative"}
			}
			if createOpts.maxPurchasePerUser < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--max-purchase-per-user must not be negative"}
			}
			body := map[string]any{
				"app_id":                   createOpts.appID,
				"name":                     strings.TrimSpace(createOpts.name),
				"description":              strings.TrimSpace(createOpts.description),
				"price_amount":             createOpts.price,
				"currency":                 createOpts.currency,
				"quota_amount":             createOpts.quota,
				"enabled":                  createOpts.enabled,
				"sort_order":               createOpts.sortOrder,
				"max_purchase_per_user":    createOpts.maxPurchasePerUser,
				"stripe_price_id":          createOpts.stripePriceID,
				"creem_product_id":         createOpts.creemProductID,
				"waffo_pancake_product_id": createOpts.waffoPancakeProductID,
			}
			return runData(cmd, http.MethodPost, "/api/app-products", body)
		},
	}
	addAppProductFlags(create, &createOpts)
	for _, name := range []string{"app-id", "name", "price", "quota"} {
		_ = create.MarkFlagRequired(name)
	}

	var updateOpts appProductOptions
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an app product; unpassed fields keep their current value",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := map[string]any{}
			for _, field := range []struct {
				flag  string
				key   string
				value any
			}{
				{"app-id", "app_id", updateOpts.appID},
				{"name", "name", strings.TrimSpace(updateOpts.name)},
				{"description", "description", strings.TrimSpace(updateOpts.description)},
				{"price", "price_amount", updateOpts.price},
				{"currency", "currency", updateOpts.currency},
				{"quota", "quota_amount", updateOpts.quota},
				{"enabled", "enabled", updateOpts.enabled},
				{"sort-order", "sort_order", updateOpts.sortOrder},
				{"max-purchase-per-user", "max_purchase_per_user", updateOpts.maxPurchasePerUser},
				{"stripe-price-id", "stripe_price_id", updateOpts.stripePriceID},
				{"creem-product-id", "creem_product_id", updateOpts.creemProductID},
				{"waffo-pancake-product-id", "waffo_pancake_product_id", updateOpts.waffoPancakeProductID},
			} {
				if cmd.Flags().Changed(field.flag) {
					changed[field.key] = field.value
				}
			}
			if len(changed) == 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "at least one product field must be provided"}
			}
			if cmd.Flags().Changed("app-id") && updateOpts.appID <= 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id must be a positive integer"}
			}
			if cmd.Flags().Changed("name") && strings.TrimSpace(updateOpts.name) == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--name must not be empty"}
			}
			if cmd.Flags().Changed("price") && updateOpts.price < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--price must not be negative"}
			}
			if cmd.Flags().Changed("quota") && updateOpts.quota < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--quota must not be negative"}
			}
			if cmd.Flags().Changed("max-purchase-per-user") && updateOpts.maxPurchasePerUser < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--max-purchase-per-user must not be negative"}
			}
			// The API replaces the whole product on PUT, so read the current
			// record first and overlay the changed flags instead of sending a
			// partial body that would clear every other field.
			productPath := "/api/app-products/" + url.PathEscape(args[0])
			c, err := newClient()
			if err != nil {
				return err
			}
			current, err := c.Do(http.MethodGet, productPath, nil)
			if err != nil {
				return mapClientErr(err)
			}
			var body map[string]any
			if err := json.Unmarshal(current, &body); err != nil || body == nil {
				return generalErr("unexpected product response: %s", string(current))
			}
			for key, value := range changed {
				body[key] = value
			}
			return runData(cmd, http.MethodPut, productPath, body)
		},
	}
	addAppProductFlags(update, &updateOpts)

	enable := &cobra.Command{
		Use:   "enable <id>",
		Short: "Enable an app product",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodPatch, "/api/app-products/"+url.PathEscape(args[0]), map[string]any{"enabled": true})
		},
	}
	disable := &cobra.Command{
		Use:   "disable <id>",
		Short: "Disable an app product",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodPatch, "/api/app-products/"+url.PathEscape(args[0]), map[string]any{"enabled": false})
		},
	}

	var orderPage, orderPageSize, orderAppID, orderUserID int
	orders := &cobra.Command{
		Use:   "orders",
		Short: "List app product orders",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if orderAppID < 0 || orderUserID < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id and --user-id must not be negative"}
			}
			path := fmt.Sprintf("/api/app-products/orders?p=%d&page_size=%d", orderPage, orderPageSize)
			if orderAppID > 0 {
				path += "&app_id=" + strconv.Itoa(orderAppID)
			}
			if orderUserID > 0 {
				path += "&user_id=" + strconv.Itoa(orderUserID)
			}
			return runData(cmd, http.MethodGet, path, nil)
		},
	}
	addPageFlags(orders, &orderPage, &orderPageSize)
	orders.Flags().IntVar(&orderAppID, "app-id", 0, "only list orders of this app")
	orders.Flags().IntVar(&orderUserID, "user-id", 0, "only list orders of this user")

	completeOrder := &cobra.Command{
		Use:   "complete-order <trade_no>",
		Short: "Manually complete an order and credit its quota (write)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tradeNo := strings.TrimSpace(args[0])
			if tradeNo == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "trade_no must not be empty"}
			}
			path := "/api/app-products/orders/" + url.PathEscape(tradeNo) + "/complete"
			return runData(cmd, http.MethodPost, path, nil)
		},
	}

	cmd.AddCommand(list, create, update, enable, disable, orders, completeOrder)
	return cmd
}
