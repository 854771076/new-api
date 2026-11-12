package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	var p, pageSize, appID int
	list := &cobra.Command{
		Use:   "list",
		Short: "List users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if appID < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--app-id must not be negative"}
			}
			extra := ""
			if appID > 0 {
				extra = "&app_id=" + strconv.Itoa(appID)
			}
			return runData(cmd, http.MethodGet, listPath("user", p, pageSize, extra), nil)
		},
	}
	addPageFlags(list, &p, &pageSize)
	list.Flags().IntVar(&appID, "app-id", 0, "show app quota for this app; empty shows the platform view")
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
	var unlimitedQuota bool
	var quota int64
	var expiresIn string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if quota < 0 {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--quota must not be negative"}
			}
			if unlimitedQuota && cmd.Flags().Changed("quota") {
				return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--quota cannot be combined with --unlimited-quota=true"}
			}
			expiredTime := int64(-1)
			if expiresIn != "" {
				d, err := time.ParseDuration(expiresIn)
				if err != nil {
					return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--expires-in must be a Go duration such as 24h"}
				}
				if d <= 0 {
					return &CommandError{ExitCode: ExitUsage, Code: "usage_error", Message: "--expires-in must be positive"}
				}
				expiredTime = time.Now().Add(d).Unix()
			}
			body := map[string]any{
				"name":            name,
				"unlimited_quota": unlimitedQuota,
				"expired_time":    expiredTime,
			}
			if !unlimitedQuota {
				body["remain_quota"] = quota
			}
			return runData(cmd, http.MethodPost, "/api/token/", body)
		},
	}
	create.Flags().StringVar(&name, "name", "", "token name")
	create.Flags().BoolVar(&unlimitedQuota, "unlimited-quota", true, "ignore --quota and allow unlimited usage")
	create.Flags().Int64Var(&quota, "quota", 500000, "remaining quota when --unlimited-quota=false")
	create.Flags().StringVar(&expiresIn, "expires-in", "", "Go duration until expiry (e.g. 24h); empty means never expires")
	_ = create.MarkFlagRequired("name")
	cmd.AddCommand(list, get, create)
	return cmd
}

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "log", Short: "Inspect logs"}
	var p, pageSize, logType, channel int
	var startTS, endTS int64
	var modelName, tokenName, group, requestID, upstreamRequestID, username string
	list := &cobra.Command{
		Use:   "list",
		Short: "List usage logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			q.Set("p", strconv.Itoa(p))
			q.Set("page_size", strconv.Itoa(pageSize))
			if logType != 0 {
				q.Set("type", strconv.Itoa(logType))
			}
			if startTS != 0 {
				q.Set("start_timestamp", strconv.FormatInt(startTS, 10))
			}
			if endTS != 0 {
				q.Set("end_timestamp", strconv.FormatInt(endTS, 10))
			}
			if modelName != "" {
				q.Set("model_name", modelName)
			}
			if tokenName != "" {
				q.Set("token_name", tokenName)
			}
			if group != "" {
				q.Set("group", group)
			}
			if requestID != "" {
				q.Set("request_id", requestID)
			}
			if upstreamRequestID != "" {
				q.Set("upstream_request_id", upstreamRequestID)
			}
			if username != "" {
				q.Set("username", username)
			}
			if channel != 0 {
				q.Set("channel", strconv.Itoa(channel))
			}
			return runData(cmd, http.MethodGet, "/api/log/?"+q.Encode(), nil)
		},
	}
	addPageFlags(list, &p, &pageSize)
	list.Flags().IntVar(&logType, "type", 0, "log type filter")
	list.Flags().Int64Var(&startTS, "start-timestamp", 0, "start timestamp (unix seconds)")
	list.Flags().Int64Var(&endTS, "end-timestamp", 0, "end timestamp (unix seconds)")
	list.Flags().StringVar(&modelName, "model-name", "", "model name filter")
	list.Flags().StringVar(&tokenName, "token-name", "", "token name filter")
	list.Flags().StringVar(&group, "group", "", "group filter")
	list.Flags().StringVar(&requestID, "request-id", "", "request id filter")
	list.Flags().StringVar(&upstreamRequestID, "upstream-request-id", "", "upstream request id filter")
	list.Flags().StringVar(&username, "username", "", "username filter")
	list.Flags().IntVar(&channel, "channel", 0, "channel id filter")
	files := &cobra.Command{
		Use:   "files",
		Short: "List server log files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runData(cmd, http.MethodGet, "/api/performance/logs", nil)
		},
	}
	cmd.AddCommand(list, files)
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
