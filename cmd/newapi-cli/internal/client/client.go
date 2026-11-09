package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseBytes = 10 << 20

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
// optional bearer token. Requests time out after 30 seconds.
func New(server, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(server, "/"),
		Token:      token,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Do sends a request and returns the unwrapped data field of the response
// envelope. Responses without an envelope are returned verbatim. body is
// JSON-marshaled when non-nil; pass json.RawMessage to send it as-is.
func (c *Client) Do(method, path string, body any) (json.RawMessage, error) {
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
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
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
	if resp.StatusCode >= 400 {
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, &Error{StatusCode: resp.StatusCode, Code: "http_error", Message: fmt.Sprintf("HTTP %s: %s", resp.Status, snippet)}
	}
	return json.RawMessage(raw), nil
}
