package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxResponseBytes = 10 << 20
	// maxRedirects bounds the redirect chain a single request may follow.
	maxRedirects = 10
)

// ConfigError reports a server configuration the client refuses to use, such
// as plain HTTP to a non-loopback host.
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return e.Message
}

// Client is a thin HTTP client for the new-api admin API.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// Error is an API-level failure: a non-2xx status or a success=false
// envelope.
type Error struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *Error) Error() string {
	return e.Message
}

// IsAuth reports whether the failure is an authentication/authorization
// error (HTTP 401/403 or a terminal auth code such as invalid_grant).
func (e *Error) IsAuth() bool {
	return e.StatusCode == 401 || e.StatusCode == 403 || e.Code == "invalid_grant"
}

// New creates a client for server (trailing slashes trimmed) with an
// optional bearer token. The server must use HTTPS unless it is loopback,
// because the token is sent on every request. Requests time out after 30
// seconds and redirects may not leave the original origin.
func New(server, token string) (*Client, error) {
	base, err := validateServerURL(server)
	if err != nil {
		return nil, err
	}
	return &Client{
		BaseURL:    base,
		Token:      token,
		HTTPClient: newHTTPClient(),
	}, nil
}

// validateServerURL rejects URLs the CLI must not send a bearer token to.
func validateServerURL(server string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(server), "/")
	u, err := url.Parse(base)
	if err != nil {
		return "", &ConfigError{Message: fmt.Sprintf("invalid server URL %q: %v", server, err)}
	}
	if u.Scheme == "" || u.Host == "" {
		return "", &ConfigError{Message: fmt.Sprintf("invalid server URL %q: a scheme and host are required, e.g. https://api.example.com", server)}
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", &ConfigError{Message: fmt.Sprintf("insecure server URL %q: use https:// for non-loopback hosts", server)}
		}
	default:
		return "", &ConfigError{Message: fmt.Sprintf("invalid server URL %q: scheme must be http:// or https://", server)}
	}
	return base, nil
}

// isLoopbackHost reports whether host is localhost or a loopback IP literal.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// defaultHTTPClient backs Client values built without New.
var defaultHTTPClient = newHTTPClient()

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: checkRedirect,
	}
}

// checkRedirect keeps the bearer token bound to the server it was issued
// for: a redirect may neither leave the original origin (scheme, host and
// effective port must all match) nor downgrade HTTPS.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	origin := via[0].URL
	if origin.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect from https to %s", req.URL.Scheme)
	}
	if req.URL.Scheme != origin.Scheme ||
		!strings.EqualFold(req.URL.Hostname(), origin.Hostname()) ||
		effectivePort(req.URL) != effectivePort(origin) {
		return fmt.Errorf("refusing cross-origin redirect to %s", req.URL.Redacted())
	}
	return nil
}

// effectivePort returns the port the URL connects to, including scheme
// defaults, so "https://h" and "https://h:443" compare equal.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch u.Scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// Do sends a request and returns the unwrapped data field of the response
// envelope. A 2xx response must be a new-api JSON envelope; anything else is
// reported as an http_error. body is JSON-marshaled when non-nil; pass
// json.RawMessage to send it as-is.
func (c *Client) Do(method, path string, body any) (json.RawMessage, error) {
	return c.do(method, path, body, false)
}

// DoPassthrough behaves like Do but returns a non-envelope 2xx body
// verbatim. Only the `api` command uses it, to inspect endpoints that
// intentionally answer with plain JSON or text; management commands stay
// strict so an HTML error page is never mistaken for data.
func (c *Client) DoPassthrough(method, path string, body any) (json.RawMessage, error) {
	return c.do(method, path, body, true)
}

func (c *Client) do(method, path string, body any, allowRaw bool) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("cannot marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = defaultHTTPClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Read one byte past the limit so an oversized body is reported instead
	// of being silently truncated.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBytes {
		return nil, &Error{
			StatusCode: resp.StatusCode,
			Code:       "response_too_large",
			Message:    fmt.Sprintf("HTTP %s: response body exceeds %d bytes", resp.Status, maxResponseBytes),
		}
	}

	var env struct {
		Success *bool           `json:"success"`
		Message string          `json:"message"`
		Code    string          `json:"code"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Success != nil {
		if *env.Success {
			if len(env.Data) == 0 {
				return json.RawMessage("null"), nil
			}
			return env.Data, nil
		}
		msg := env.Message
		if msg == "" {
			msg = resp.Status
		}
		return nil, &Error{StatusCode: resp.StatusCode, Code: env.Code, Message: msg}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if allowRaw {
			if len(raw) == 0 {
				raw = []byte("null")
			}
			return json.RawMessage(raw), nil
		}
		return nil, &Error{
			StatusCode: resp.StatusCode,
			Code:       "http_error",
			Message:    fmt.Sprintf("HTTP %s: expected a new-api JSON envelope, got: %s", resp.Status, bodySnippet(raw)),
		}
	}
	return nil, &Error{
		StatusCode: resp.StatusCode,
		Code:       "http_error",
		Message:    fmt.Sprintf("HTTP %s: %s", resp.Status, bodySnippet(raw)),
	}
}

// bodySnippet condenses a response body for an error message, keeping the
// output short even when the server returned a full HTML page.
func bodySnippet(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if s == "" {
		return "(empty body)"
	}
	runes := []rune(s)
	if len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return s
}
