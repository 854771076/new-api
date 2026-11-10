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
	cmd.AddCommand(list)
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
