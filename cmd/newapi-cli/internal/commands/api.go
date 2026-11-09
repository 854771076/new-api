package commands

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newAPICmd() *cobra.Command {
	var dataFlag string
	cmd := &cobra.Command{
		Use:   "api <METHOD> <path>",
		Short: "Call an arbitrary API endpoint, e.g. newapi-cli api GET /api/user/?p=1",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := args[0]
			if method != strings.ToUpper(method) || !validMethod(method) {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "METHOD must be uppercase, e.g. GET"}
			}
			path := args[1]
			if !strings.HasPrefix(path, "/") {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "path must start with /"}
			}
			var body any
			if dataFlag != "" {
				raw := dataFlag
				if file, ok := strings.CutPrefix(dataFlag, "@"); ok {
					content, err := os.ReadFile(file)
					if err != nil {
						return generalErr("cannot read %s: %v", file, err)
					}
					raw = string(content)
				}
				if !json.Valid([]byte(raw)) {
					return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--data is not valid JSON"}
				}
				body = json.RawMessage(raw)
			}
			// `api` is the escape hatch for arbitrary endpoints, so it keeps
			// accepting a non-envelope body that management commands reject.
			c, err := newClient()
			if err != nil {
				return err
			}
			data, err := c.DoPassthrough(method, path, body)
			if err != nil {
				return mapClientErr(err)
			}
			return writeData(cmd, data)
		},
	}
	cmd.Flags().StringVar(&dataFlag, "data", "", "JSON request body, or @file to read it from a file")
	return cmd
}

func validMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	}
	return false
}
