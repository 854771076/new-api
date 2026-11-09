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

func numericIDArg(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if _, err := strconv.Atoi(args[0]); err != nil {
		return fmt.Errorf("invalid id %q: must be a number", args[0])
	}
	return nil
}

func addPageFlags(cmd *cobra.Command, p, pageSize *int) {
	cmd.Flags().IntVar(p, "p", 1, "page number")
	cmd.Flags().IntVar(pageSize, "page-size", 20, "page size")
}

func listPath(resource string, p, pageSize int, extra string) string {
	path := fmt.Sprintf("/api/%s/?p=%d&page_size=%d", resource, p, pageSize)
	return path + extra
}

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage users"}
	var p, pageSize int
	list := &cobra.Command{
		Use:   "list",
		Short: "List users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, listPath("user", p, pageSize, ""), nil)
		},
	}
	addPageFlags(list, &p, &pageSize)
	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a user",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, "/api/user/"+url.PathEscape(args[0]), nil)
		},
	}
	cmd.AddCommand(list, get)
	return cmd
}

func newChannelCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "channel", Short: "Manage channels"}
	var p, pageSize int
	list := &cobra.Command{
		Use:   "list",
		Short: "List channels",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, listPath("channel", p, pageSize, ""), nil)
		},
	}
	addPageFlags(list, &p, &pageSize)
	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a channel",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, "/api/channel/"+url.PathEscape(args[0]), nil)
		},
	}
	cmd.AddCommand(list, get)
	return cmd
}

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Manage tokens"}
	var p, pageSize int
	list := &cobra.Command{
		Use:   "list",
		Short: "List tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, listPath("token", p, pageSize, ""), nil)
		},
	}
	addPageFlags(list, &p, &pageSize)
	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a token",
		Args:  numericIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, "/api/token/"+url.PathEscape(args[0]), nil)
		},
	}
	var name string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodPost, "/api/token/", map[string]string{"name": name})
		},
	}
	create.Flags().StringVar(&name, "name", "", "token name")
	_ = create.MarkFlagRequired("name")
	cmd.AddCommand(list, get, create)
	return cmd
}

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "log", Short: "Inspect logs"}
	var p, pageSize int
	var logType string
	list := &cobra.Command{
		Use:   "list",
		Short: "List logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			extra := ""
			if logType != "" {
				if _, err := strconv.Atoi(logType); err != nil {
					return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--type must be a number"}
				}
				extra = "&type=" + url.QueryEscape(logType)
			}
			return runData(cmd, http.MethodGet, listPath("log", p, pageSize, extra), nil)
		},
	}
	addPageFlags(list, &p, &pageSize)
	list.Flags().StringVar(&logType, "type", "", "log type filter (numeric)")
	cmd.AddCommand(list)
	return cmd
}

func newOptionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "option", Short: "Manage server options"}
	get := &cobra.Command{
		Use:   "get <key>",
		Short: "Show one option",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			data, err := c.Do(http.MethodGet, "/api/option/", nil)
			if err != nil {
				return mapClientErr(err)
			}
			var options []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(data, &options); err != nil {
				return generalErr("unexpected /api/option/ response: %v", err)
			}
			for _, o := range options {
				if o.Key == args[0] {
					raw, err := json.Marshal(map[string]string{"key": o.Key, "value": o.Value})
					if err != nil {
						return generalErr("%v", err)
					}
					return writeData(cmd, raw)
				}
			}
			return &CommandError{ExitCode: ExitGeneral, Code: "not_found", Message: fmt.Sprintf("option %q not found", args[0])}
		},
	}
	update := &cobra.Command{
		Use:   "update <key>=<value>",
		Short: "Update one option",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value, ok := strings.Cut(args[0], "=")
			if !ok || key == "" {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "argument must be in <key>=<value> form"}
			}
			return runData(cmd, http.MethodPut, "/api/option/", map[string]string{"key": key, "value": value})
		},
	}
	cmd.AddCommand(get, update)
	return cmd
}
