package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/client"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/config"
	"github.com/QuantumNous/new-api/cmd/newapi-cli/internal/output"
)

// Exit codes.
const (
	ExitGeneral = 1
	ExitAuth    = 2
	ExitUsage   = 3
)

// CommandError is a failure carrying the process exit code and the machine
// readable code emitted on stderr.
type CommandError struct {
	ExitCode int
	Code     string
	Message  string
}

func (e *CommandError) Error() string {
	return e.Message
}

var (
	flagProfile string
	flagPretty  bool
)

// NewRootCmd builds the full command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "newapi-cli",
		Short:         "Admin CLI for a new-api server",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&flagProfile, "profile", "", "config profile (env "+config.EnvProfile+")")
	root.PersistentFlags().BoolVar(&flagPretty, "pretty", false, "human-readable output instead of JSON")
	root.AddCommand(
		newAuthCmd(),
		newUserCmd(),
		newChannelCmd(),
		newTokenCmd(),
		newLogCmd(),
		newOptionCmd(),
		newPlanCmd(),
		newProductCmd(),
		newAPICmd(),
	)
	return root
}

func generalErr(format string, args ...any) *CommandError {
	return &CommandError{ExitCode: ExitGeneral, Code: "error", Message: fmt.Sprintf(format, args...)}
}

func mapClientErr(err error) error {
	var apiErr *client.Error
	if errors.As(err, &apiErr) {
		code := apiErr.Code
		if code == "" {
			code = "api_error"
		}
		if apiErr.IsAuth() {
			if apiErr.Code == "" {
				code = "unauthorized"
			}
			return &CommandError{ExitCode: ExitAuth, Code: code, Message: apiErr.Message}
		}
		return &CommandError{ExitCode: ExitGeneral, Code: code, Message: apiErr.Message}
	}
	return generalErr("%v", err)
}

// mapClientConfigErr maps an unusable server configuration to a usage error.
func mapClientConfigErr(err error) *CommandError {
	var cfgErr *client.ConfigError
	if errors.As(err, &cfgErr) {
		return &CommandError{ExitCode: ExitUsage, Code: "config_error", Message: cfgErr.Message}
	}
	return generalErr("%v", err)
}

func profileName() string {
	if flagProfile != "" {
		return flagProfile
	}
	if name := os.Getenv(config.EnvProfile); name != "" {
		return name
	}
	return config.DefaultProfile
}

func loadProfile() (string, *config.Config, string, error) {
	name := profileName()
	path, err := config.DefaultPath()
	if err != nil {
		return "", nil, "", generalErr("%v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", nil, "", generalErr("%v", err)
	}
	return path, cfg, name, nil
}

func newClient() (*client.Client, error) {
	_, cfg, name, err := loadProfile()
	if err != nil {
		return nil, err
	}
	p := cfg.Profile(name)
	if p == nil || p.Token == "" || p.Server == "" {
		return nil, &CommandError{
			ExitCode: ExitAuth,
			Code:     "not_logged_in",
			Message:  fmt.Sprintf("profile %q is not logged in; run `newapi-cli auth login`", name),
		}
	}
	c, err := client.New(p.Server, p.Token)
	if err != nil {
		return nil, mapClientConfigErr(err)
	}
	return c, nil
}

// runData performs an authenticated request and writes the unwrapped data to
// stdout in the selected output format.
func runData(cmd *cobra.Command, method, path string, body any) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	data, err := c.Do(method, path, body)
	if err != nil {
		return mapClientErr(err)
	}
	return writeData(cmd, data)
}

func writeData(cmd *cobra.Command, data json.RawMessage) error {
	if flagPretty {
		return output.Pretty(cmd.OutOrStdout(), data)
	}
	return output.JSON(cmd.OutOrStdout(), data)
}
