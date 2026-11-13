package commands

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// oauthAppOptions holds the shared flags of `oauth-app create` and
// `oauth-app update`.
type oauthAppOptions struct {
	name                   string
	logoURL                string
	redirectURIs           []string
	postLogoutRedirectURIs []string
	scopes                 []string
	publicClient           bool
	status                 int
	supabaseProjectRef     string
	waffoPancakeReturnURL  string
}

// addOAuthAppFlags registers the oauth-app flags. `status` is only accepted by
// the update API (creation always yields an enabled app), so it is registered
// only when withStatus is true.
func addOAuthAppFlags(cmd *cobra.Command, o *oauthAppOptions, withStatus bool) {
	flags := cmd.Flags()
	flags.StringVar(&o.name, "name", "", "application name")
	flags.StringVar(&o.logoURL, "logo-url", "", "logo https URL or data URI")
	flags.StringSliceVar(&o.redirectURIs, "redirect-uris", nil, "comma-separated allowed redirect URIs")
	flags.StringSliceVar(&o.postLogoutRedirectURIs, "post-logout-redirect-uris", nil, "comma-separated allowed post-logout redirect URIs")
	flags.StringSliceVar(&o.scopes, "scopes", nil, "comma-separated allowed scopes")
	flags.BoolVar(&o.publicClient, "public-client", false, "register as a public client (no client secret)")
	if withStatus {
		flags.IntVar(&o.status, "status", 0, "application status: 1 enabled, 2 disabled")
	}
	flags.StringVar(&o.supabaseProjectRef, "supabase-project-ref", "", "Supabase project ref metadata")
	flags.StringVar(&o.waffoPancakeReturnURL, "waffo-pancake-return-url", "", "Waffo Pancake checkout success redirect override")
}

func newOAuthAppCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "oauth-app", Short: "Manage OAuth applications"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List OAuth applications",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, "/api/oauth-apps/", nil)
		},
	}

	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Show an OAuth application",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, "/api/oauth-apps/"+url.PathEscape(args[0]), nil)
		},
	}

	var createOpts oauthAppOptions
	create := &cobra.Command{
		Use:   "create",
		Short: "Create an OAuth application",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(createOpts.name) == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--name must not be empty"}
			}
			if len(createOpts.redirectURIs) == 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--redirect-uris must not be empty"}
			}
			if len(createOpts.scopes) == 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--scopes must not be empty"}
			}
			body := map[string]any{
				"name":                      strings.TrimSpace(createOpts.name),
				"logo_url":                  strings.TrimSpace(createOpts.logoURL),
				"redirect_uris":             createOpts.redirectURIs,
				"post_logout_redirect_uris": createOpts.postLogoutRedirectURIs,
				"scopes":                    createOpts.scopes,
				"public_client":             createOpts.publicClient,
				"supabase_project_ref":      strings.TrimSpace(createOpts.supabaseProjectRef),
				"waffo_pancake_return_url":  strings.TrimSpace(createOpts.waffoPancakeReturnURL),
			}
			return runData(cmd, http.MethodPost, "/api/oauth-apps/", body)
		},
	}
	addOAuthAppFlags(create, &createOpts, false)
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("redirect-uris")
	_ = create.MarkFlagRequired("scopes")

	var updateOpts oauthAppOptions
	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an OAuth application; unpassed fields keep their current value",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := map[string]any{}
			if cmd.Flags().Changed("name") {
				if strings.TrimSpace(updateOpts.name) == "" {
					return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--name must not be empty"}
				}
				changed["name"] = strings.TrimSpace(updateOpts.name)
			}
			if cmd.Flags().Changed("logo-url") {
				changed["logo_url"] = strings.TrimSpace(updateOpts.logoURL)
			}
			if cmd.Flags().Changed("redirect-uris") {
				changed["redirect_uris"] = updateOpts.redirectURIs
			}
			if cmd.Flags().Changed("post-logout-redirect-uris") {
				changed["post_logout_redirect_uris"] = updateOpts.postLogoutRedirectURIs
			}
			if cmd.Flags().Changed("scopes") {
				changed["scopes"] = updateOpts.scopes
			}
			if cmd.Flags().Changed("public-client") {
				changed["public_client"] = updateOpts.publicClient
			}
			if cmd.Flags().Changed("status") {
				changed["status"] = updateOpts.status
			}
			if cmd.Flags().Changed("supabase-project-ref") {
				changed["supabase_project_ref"] = strings.TrimSpace(updateOpts.supabaseProjectRef)
			}
			if cmd.Flags().Changed("waffo-pancake-return-url") {
				changed["waffo_pancake_return_url"] = strings.TrimSpace(updateOpts.waffoPancakeReturnURL)
			}
			if len(changed) == 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "at least one oauth-app field must be provided"}
			}
			appPath := "/api/oauth-apps/" + url.PathEscape(args[0])
			c, err := newClient()
			if err != nil {
				return err
			}
			current, err := c.Do(http.MethodGet, appPath, nil)
			if err != nil {
				return mapClientErr(err)
			}
			var body map[string]any
			if err := json.Unmarshal(current, &body); err != nil || body == nil {
				return generalErr("unexpected oauth app response: %s", string(current))
			}
			for key, value := range changed {
				body[key] = value
			}
			return runData(cmd, http.MethodPut, appPath, body)
		},
	}
	addOAuthAppFlags(update, &updateOpts, true)

	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an OAuth application",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodDelete, "/api/oauth-apps/"+url.PathEscape(args[0]), nil)
		},
	}

	cmd.AddCommand(list, get, create, update, del)
	return cmd
}
