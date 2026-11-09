package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/buildinfo"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/client"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/config"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/output"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Authenticate against a new-api server"}
	cmd.AddCommand(newAuthLoginCmd(), newAuthStatusCmd(), newAuthLogoutCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var server, name string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authorize via the browser and store the issued token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, profile, err := loadProfile()
			if err != nil {
				return err
			}
			if server == "" {
				server = loginServer(server, cfg.Profile(profile))
			}

			in := bufio.NewScanner(cmd.InOrStdin())
			readLine := func() string {
				if !in.Scan() {
					return ""
				}
				return strings.TrimSpace(in.Text())
			}
			if server == "" {
				fmt.Fprint(cmd.ErrOrStderr(), "Server URL: ")
				server = readLine()
			}
			server = strings.TrimRight(server, "/")
			if server == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "server is required (--server)"}
			}
			if !strings.HasPrefix(server, "https://") && !strings.HasPrefix(server, "http://") {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "server must start with http:// or https://"}
			}
			if strings.HasPrefix(server, "http://") && !isLoopbackServer(server) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s is not HTTPS; the authorization code and token will be sent in plaintext.\n", server)
			}

			authURL := server + "/cli/authorize"
			if name != "" {
				authURL += "?name=" + url.QueryEscape(name)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Open this URL in your browser to authorize:\n\n  %s\n\n", authURL)
			fmt.Fprint(cmd.ErrOrStderr(), "Paste the authorization code: ")
			code := readLine()
			if code == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "authorization code is required"}
			}

			c, err := client.New(server, "")
			if err != nil {
				return mapClientConfigErr(err)
			}
			data, err := c.Do(http.MethodPost, "/api/cli/exchange", map[string]string{"code": code})
			if err != nil {
				return mapClientErr(err)
			}
			var payload struct {
				Token     string `json:"token"`
				ExpiresAt int64  `json:"expires_at"`
			}
			if err := json.Unmarshal(data, &payload); err != nil || payload.Token == "" {
				return generalErr("malformed exchange response from %s", server)
			}

			cfg.SetProfile(profile, &config.Profile{Server: server, Token: payload.Token, ExpiresAt: payload.ExpiresAt})
			if err := cfg.Save(path); err != nil {
				return generalErr("cannot save config: %v", err)
			}
			return output.JSON(cmd.OutOrStdout(), map[string]any{
				"status":     "ok",
				"server":     server,
				"expires_at": payload.ExpiresAt,
			})
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "new-api server URL; defaults to "+config.EnvServer+", the stored profile, then the build default")
	cmd.Flags().StringVar(&name, "name", "", "token name hint passed to the authorize page")
	return cmd
}

// loginServer resolves the server to authorize against: explicit input, then
// NEWAPI_CLI_SERVER, then the stored profile, then the build-time default.
func loginServer(explicit string, profile *config.Profile) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv(config.EnvServer); env != "" {
		return env
	}
	if profile != nil && profile.Server != "" {
		return profile.Server
	}
	return buildinfo.DefaultServer
}

// isLoopbackServer reports whether the URL targets the local machine, where
// plaintext HTTP does not leave the host.
func isLoopbackServer(server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the active profile and token expiry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, name, err := loadProfile()
			if err != nil {
				return err
			}
			p := cfg.Profile(name)
			if p == nil || p.Token == "" {
				return &CommandError{
					ExitCode: ExitAuth,
					Code:     "not_logged_in",
					Message:  fmt.Sprintf("profile %q is not logged in", name),
				}
			}
			return output.JSON(cmd.OutOrStdout(), map[string]any{
				"profile":        name,
				"server":         p.Server,
				"expires_at":     p.ExpiresAt,
				"expired":        p.ExpiresAt > 0 && time.Now().Unix() > p.ExpiresAt,
				"default_server": buildinfo.DefaultServer,
			})
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored credentials of the active profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, name, err := loadProfile()
			if err != nil {
				return err
			}
			cfg.DeleteProfile(name)
			if err := cfg.Save(path); err != nil {
				return generalErr("cannot save config: %v", err)
			}
			return output.JSON(cmd.OutOrStdout(), map[string]any{"status": "ok"})
		},
	}
}
