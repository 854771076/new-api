package commands

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/config"
)

func runTokenCreate(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newTokenCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{"create"}, args...))
	return cmd.Execute()
}

func requireUsageError(t *testing.T, err error, contains string) {
	t.Helper()
	require.Error(t, err)
	var cmdErr *CommandError
	require.True(t, errors.As(err, &cmdErr), "expected CommandError, got %T", err)
	assert.Equal(t, ExitUsage, cmdErr.ExitCode)
	assert.Equal(t, "usage_error", cmdErr.Code)
	assert.Contains(t, cmdErr.Message, contains)
}

func TestTokenCreateQuotaFlags(t *testing.T) {
	requireUsageError(t, runTokenCreate(t, "--name", "t", "--quota", "10"), "--quota")
	requireUsageError(t, runTokenCreate(t, "--name", "t", "--unlimited-quota=false", "--quota", "-5"), "negative")
}

func TestTokenCreateExpiresInMustBePositive(t *testing.T) {
	for _, value := range []string{"0s", "-1h", "-0.5s"} {
		t.Run(value, func(t *testing.T) {
			requireUsageError(t, runTokenCreate(t, "--name", "t", "--expires-in", value), "--expires-in")
		})
	}
}

func TestTokenCreateRejectsInvalidDuration(t *testing.T) {
	requireUsageError(t, runTokenCreate(t, "--name", "t", "--expires-in", "24"), "Go duration")
}

func TestTokenCreateReachesRequestWithValidFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	tests := []struct {
		name string
		args []string
	}{
		{name: "default unlimited quota", args: []string{"--name", "t"}},
		{name: "explicit finite quota", args: []string{"--name", "t", "--unlimited-quota=false", "--quota", "10"}},
		{name: "positive expiry", args: []string{"--name", "t", "--expires-in", "1h"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runTokenCreate(t, tc.args...)
			var cmdErr *CommandError
			require.True(t, errors.As(err, &cmdErr), "expected CommandError, got %v", err)
			assert.Equal(t, ExitAuth, cmdErr.ExitCode)
			assert.Equal(t, "not_logged_in", cmdErr.Code)
		})
	}
}

// recordedRequest is the last request the fake server received.
type recordedRequest struct {
	method string
	path   string
	query  url.Values
	body   []byte
}

// newRecordedServer answers every request with an empty success envelope and
// publishes each request so a test can assert the CLI wire contract.
func newRecordedServer(t *testing.T) (chan recordedRequest, string) {
	t.Helper()
	requests := make(chan recordedRequest, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requests <- recordedRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query(), body: raw}
		w.Header().Set("Content-Type", "application/json")
		data := "null"
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/app-products/") {
			data = `{"id":9,"app_id":5,"name":"Existing","description":"keep","price_amount":3,"currency":"USD","quota_amount":100,"enabled":true,"sort_order":1,"max_purchase_per_user":2}`
		}
		_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
	}))
	t.Cleanup(srv.Close)
	return requests, srv.URL
}

func pointCLIAt(t *testing.T, server string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvProfile, config.DefaultProfile)
	cfg := &config.Config{Profiles: map[string]*config.Profile{
		config.DefaultProfile: {Server: server, Token: "test-token"},
	}}
	require.NoError(t, cfg.Save(filepath.Join(home, config.DirName, config.FileName)))
}

func executeCommand(cmd *cobra.Command, args ...string) error {
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestUserListAppIDQuery(t *testing.T) {
	requests, server := newRecordedServer(t)
	pointCLIAt(t, server)

	require.NoError(t, executeCommand(newUserCmd(), "list", "--app-id", "7"))
	rec := <-requests
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/user/", rec.path)
	assert.Equal(t, "7", rec.query.Get("app_id"))
	assert.Equal(t, "1", rec.query.Get("p"))
	assert.Equal(t, "20", rec.query.Get("page_size"))

	require.NoError(t, executeCommand(newUserCmd(), "list"))
	rec = <-requests
	assert.Empty(t, rec.query.Get("app_id"))
}

func TestPlanListAppIDQuery(t *testing.T) {
	requests, server := newRecordedServer(t)
	pointCLIAt(t, server)

	require.NoError(t, executeCommand(newPlanCmd(), "list", "--app-id", "4"))
	rec := <-requests
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/subscription/admin/plans", rec.path)
	assert.Equal(t, "4", rec.query.Get("app_id"))
	assert.Equal(t, "1", rec.query.Get("p"))
	assert.Equal(t, "20", rec.query.Get("page_size"))

	require.NoError(t, executeCommand(newPlanCmd(), "list"))
	rec = <-requests
	assert.Empty(t, rec.query.Get("app_id"))
}

func TestProductListQuery(t *testing.T) {
	requests, server := newRecordedServer(t)
	pointCLIAt(t, server)

	require.NoError(t, executeCommand(newProductCmd(), "list", "--app-id", "3", "--p", "2", "--page-size", "5"))
	rec := <-requests
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/app-products", rec.path)
	assert.Equal(t, "3", rec.query.Get("app_id"))
	assert.Equal(t, "2", rec.query.Get("p"))
	assert.Equal(t, "5", rec.query.Get("page_size"))

	require.NoError(t, executeCommand(newProductCmd(), "list"))
	rec = <-requests
	assert.Empty(t, rec.query.Get("app_id"))
}

func TestProductCreateSendsControllerFields(t *testing.T) {
	requests, server := newRecordedServer(t)
	pointCLIAt(t, server)

	err := executeCommand(newProductCmd(), "create",
		"--app-id", "5",
		"--name", " Pro ",
		"--description", "pro plan",
		"--price", "9.99",
		"--currency", "eur",
		"--quota", "100000",
		"--sort-order", "2",
		"--max-purchase-per-user", "3",
		"--enabled=false",
		"--stripe-price-id", "price_1",
		"--creem-product-id", "prod_1",
		"--waffo-pancake-product-id", "wp_1",
	)
	require.NoError(t, err)

	rec := <-requests
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/app-products", rec.path)

	var body struct {
		AppID                 int     `json:"app_id"`
		Name                  string  `json:"name"`
		Description           string  `json:"description"`
		PriceAmount           float64 `json:"price_amount"`
		Currency              string  `json:"currency"`
		QuotaAmount           int64   `json:"quota_amount"`
		Enabled               bool    `json:"enabled"`
		SortOrder             int     `json:"sort_order"`
		MaxPurchasePerUser    int     `json:"max_purchase_per_user"`
		StripePriceID         string  `json:"stripe_price_id"`
		CreemProductID        string  `json:"creem_product_id"`
		WaffoPancakeProductID string  `json:"waffo_pancake_product_id"`
	}
	require.NoError(t, json.Unmarshal(rec.body, &body))
	assert.Equal(t, 5, body.AppID)
	assert.Equal(t, "Pro", body.Name)
	assert.Equal(t, "pro plan", body.Description)
	assert.Equal(t, 9.99, body.PriceAmount)
	assert.Equal(t, "eur", body.Currency)
	assert.Equal(t, int64(100000), body.QuotaAmount)
	assert.False(t, body.Enabled)
	assert.Equal(t, 2, body.SortOrder)
	assert.Equal(t, 3, body.MaxPurchasePerUser)
	assert.Equal(t, "price_1", body.StripePriceID)
	assert.Equal(t, "prod_1", body.CreemProductID)
	assert.Equal(t, "wp_1", body.WaffoPancakeProductID)
}

func TestProductUpdateReadsThenReplaces(t *testing.T) {
	requests, server := newRecordedServer(t)
	pointCLIAt(t, server)

	err := executeCommand(newProductCmd(), "update", "9",
		"--price", "1.5",
		"--description=",
		"--enabled=false",
	)
	require.NoError(t, err)

	read := <-requests
	assert.Equal(t, http.MethodGet, read.method)
	assert.Equal(t, "/api/app-products/9", read.path)

	rec := <-requests
	assert.Equal(t, http.MethodPut, rec.method)
	assert.Equal(t, "/api/app-products/9", rec.path)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.body, &body))
	assert.Equal(t, float64(5), body["app_id"])
	assert.Equal(t, "Existing", body["name"])
	assert.Equal(t, "", body["description"])
	assert.Equal(t, 1.5, body["price_amount"])
	assert.Equal(t, false, body["enabled"])
	// Fields the caller did not pass keep their current value.
	assert.Equal(t, "USD", body["currency"])
	assert.Equal(t, float64(100), body["quota_amount"])
	assert.Equal(t, float64(2), body["max_purchase_per_user"])
}

func TestProductEnableDisablePatch(t *testing.T) {
	requests, server := newRecordedServer(t)
	pointCLIAt(t, server)

	require.NoError(t, executeCommand(newProductCmd(), "enable", "12"))
	rec := <-requests
	assert.Equal(t, http.MethodPatch, rec.method)
	assert.Equal(t, "/api/app-products/12", rec.path)
	assert.JSONEq(t, `{"enabled":true}`, string(rec.body))

	require.NoError(t, executeCommand(newProductCmd(), "disable", "12"))
	rec = <-requests
	assert.Equal(t, http.MethodPatch, rec.method)
	assert.Equal(t, "/api/app-products/12", rec.path)
	assert.JSONEq(t, `{"enabled":false}`, string(rec.body))
}

func TestProductCompleteOrderPath(t *testing.T) {
	requests, server := newRecordedServer(t)
	pointCLIAt(t, server)

	require.NoError(t, executeCommand(newProductCmd(), "complete-order", "APO123"))
	rec := <-requests
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/app-products/orders/APO123/complete", rec.path)
	assert.Empty(t, rec.body)
}

func TestProductWriteValidation(t *testing.T) {
	requireUsageError(t, executeCommand(newProductCmd(), "create",
		"--app-id", "1", "--name", "p", "--price=-1", "--quota", "10"), "--price")
	requireUsageError(t, executeCommand(newProductCmd(), "create",
		"--app-id", "1", "--name", "p", "--price", "1", "--quota=-5"), "--quota")
	requireUsageError(t, executeCommand(newProductCmd(), "create",
		"--app-id=-1", "--name", "p", "--price", "1", "--quota", "10"), "--app-id")
	requireUsageError(t, executeCommand(newProductCmd(), "update", "9"), "at least one")
	requireUsageError(t, executeCommand(newProductCmd(), "update", "9", "--price=-1"), "--price")
	requireUsageError(t, executeCommand(newUserCmd(), "list", "--app-id=-1"), "--app-id")
	requireUsageError(t, executeCommand(newPlanCmd(), "list", "--app-id=-1"), "--app-id")

	err := executeCommand(newProductCmd(), "create", "--name", "p", "--price", "1", "--quota", "10")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app-id")
}
