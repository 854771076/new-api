package jsplugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs"
)

// Fetcher sends one utils.fetch request for the hook call whose context
// carries it. The host binds it to the executing channel; the engine passes a
// context already bounded by the call's fetch budget, so the fetcher only sends.
type Fetcher func(ctx context.Context, request FetchRequest) (FetchResponse, error)

type FetchRequest struct {
	Hook    string // the calling hook, filled by the engine for logs and errors
	URL     string
	Method  string
	Headers map[string]string
}

type FetchResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// The utils.fetch limits are part of the published plugin contract
// (docs/plugin-api/v1.md). A fetcher reads at most MaxFetchBodyBytes+1 bytes
// so that the engine can reject a longer body.
const (
	maxFetchesPerCall   = 4
	fetchTimeout        = 15 * time.Second
	maxFetchTimePerCall = 30 * time.Second
	MaxFetchBodyBytes   = 1 << 20
	maxFetchURLBytes    = 64 << 10
)

type fetcherContextKey struct{}

// WithFetcher makes utils.fetch available to the hook called with ctx.
func WithFetcher(ctx context.Context, fetcher Fetcher) context.Context {
	return context.WithValue(ctx, fetcherContextKey{}, fetcher)
}

// Uploader sends one utils.uploadFile request for the hook call whose context
// carries it. Like Fetcher it receives a context already bounded by the call's
// budget; the host resolves the request_file bytes and builds the multipart body.
type Uploader func(ctx context.Context, request UploadRequest) (UploadResponse, error)

type UploadRequest struct {
	Hook      string // the calling hook, filled by the engine for logs and errors
	URL       string
	Headers   map[string]string
	FileRef   string // request_file:<field> or request_file:<field>#<index>
	FieldName string // multipart field name, default "file"
	Filename  string // multipart filename, default the file's own
	MimeType  string // multipart Content-Type, default the file's own
}

type UploadResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

type uploaderContextKey struct{}

// WithUploader makes utils.uploadFile available to the hook called with ctx.
func WithUploader(ctx context.Context, uploader Uploader) context.Context {
	return context.WithValue(ctx, uploaderContextKey{}, uploader)
}

// runtimeCall is the hook call a runtime is running, as its host functions
// see it. The engine sets it for one call and clears it when the call ends,
// so an idle pooled runtime keeps no request data.
type runtimeCall struct {
	engine   *Engine
	runtime  *moejs.Runtime
	ctx      context.Context
	hook     string
	fetcher  Fetcher
	uploader Uploader
	watchdog *runtimeWatchdog
	// holdsSlot and parked are what the call holds of the engine's
	// semaphore and parked tokens; release returns exactly that.
	holdsSlot bool
	parked    bool
	fetches   int
	fetchTime time.Duration
}

func (c *runtimeCall) release() {
	if c.holdsSlot {
		<-c.engine.semaphore
		c.holdsSlot = false
	}
	if c.parked {
		<-c.engine.parked
		c.parked = false
	}
}

// fetch is utils.fetch. While the fetcher waits on the network the call's
// JavaScript clock is stopped and its execution slot is lent to other calls.
// Errors name the host only: hook errors reach API callers, and paths and
// queries can carry credentials.
func (c *runtimeCall) fetch(r *moejs.Realm, argument moejs.Value) (moejs.Value, error) {
	if c.fetcher == nil {
		return moejs.Undefined(), fmt.Errorf("utils.fetch is not available in %s", c.hook)
	}
	request, host, wantBytes, err := fetchRequest(r, argument)
	if err != nil {
		return moejs.Undefined(), err
	}
	request.Hook = c.hook
	status, headers, body, err := c.runNetwork(r, "utils.fetch", host, func(ctx context.Context) (int, map[string]string, []byte, error) {
		response, err := c.fetcher(ctx, request)
		return response.Status, response.Headers, response.Body, err
	})
	if err != nil {
		return moejs.Undefined(), err
	}
	bodyValue := moejs.String("")
	if wantBytes {
		// An ArrayBuffer over the response bytes, which belong to this call.
		if body == nil {
			body = []byte{}
		}
		if bodyValue, err = r.FromGo(body); err != nil {
			return moejs.Undefined(), err
		}
	} else if request.Method != http.MethodHead {
		text := string(body)
		if bodyValue, err = c.runtime.ParseJSONString(text); err != nil {
			bodyValue = moejs.String(text)
		}
	}
	return fetchResult(r, status, headers, bodyValue)
}

// uploadFile is utils.uploadFile. It sends one multipart file to the channel
// base URL host (or an allowed host) and returns the response, so a driver can
// upload reference media before it submits the billable task. It shares the
// utils.fetch budget.
func (c *runtimeCall) uploadFile(r *moejs.Realm, argument moejs.Value) (moejs.Value, error) {
	if c.uploader == nil {
		return moejs.Undefined(), fmt.Errorf("utils.uploadFile is not available in %s", c.hook)
	}
	request, host, err := uploadRequest(r, argument)
	if err != nil {
		return moejs.Undefined(), err
	}
	request.Hook = c.hook
	status, headers, body, err := c.runNetwork(r, "utils.uploadFile", host, func(ctx context.Context) (int, map[string]string, []byte, error) {
		response, err := c.uploader(ctx, request)
		return response.Status, response.Headers, response.Body, err
	})
	if err != nil {
		return moejs.Undefined(), err
	}
	bodyValue := moejs.String("")
	if text := string(body); text != "" {
		if parsed, parseErr := c.runtime.ParseJSONString(text); parseErr == nil {
			bodyValue = parsed
		} else {
			bodyValue = moejs.String(text)
		}
	}
	return fetchResult(r, status, headers, bodyValue)
}

// runNetwork accounts one host network request against the call's fetch budget
// and runs send while the call's JavaScript clock is stopped and its slot is
// lent to other calls. operation names the utility in errors. send returns the
// response status, headers and body; its context is bounded by the remaining
// budget.
func (c *runtimeCall) runNetwork(r *moejs.Realm, operation, host string, send func(ctx context.Context) (int, map[string]string, []byte, error)) (status int, headers map[string]string, body []byte, err error) {
	if c.fetches >= maxFetchesPerCall {
		return 0, nil, nil, fmt.Errorf("%s allows at most %d requests per hook call", operation, maxFetchesPerCall)
	}
	remaining := maxFetchTimePerCall - c.fetchTime
	if remaining <= 0 {
		return 0, nil, nil, fmt.Errorf("%s requests used up their %s per hook call", operation, maxFetchTimePerCall)
	}
	c.fetches++
	timeout, timeoutCause := fetchTimeout, fmt.Errorf("%s to %s timed out after %s", operation, host, fetchTimeout)
	if remaining < fetchTimeout {
		timeout, timeoutCause = remaining, fmt.Errorf("%s to %s ran past the %s of requests per hook call", operation, host, maxFetchTimePerCall)
	}

	if !c.watchdog.pause() {
		// The call timed out or its caller left; the interrupt ends it.
		return 0, nil, nil, r.CheckInterrupt()
	}
	c.yieldSlot()
	fetchContext, cancel := context.WithTimeoutCause(c.ctx, timeout, timeoutCause)
	started := time.Now()
	status, headers, body, err = send(fetchContext)
	c.fetchTime += time.Since(started)
	if err != nil && fetchContext.Err() != nil {
		err = context.Cause(fetchContext)
	}
	cancel()
	if !c.reclaimSlot() {
		// JavaScript must not run without a slot, and a thrown error could be
		// caught; interrupting discards the runtime as a timeout does.
		cause := context.Cause(c.ctx)
		if cause == nil {
			cause = fmt.Errorf("plugin call could not resume after %s: %w", operation, ErrCallAdmissionTimeout)
		}
		c.runtime.Interrupt(cause)
		return 0, nil, nil, r.CheckInterrupt()
	}
	c.watchdog.resume()
	if interrupted := r.CheckInterrupt(); interrupted != nil {
		return 0, nil, nil, interrupted
	}

	if err != nil {
		var transportErr *url.Error
		if errors.As(err, &transportErr) {
			err = transportErr.Err
		}
		return 0, nil, nil, fmt.Errorf("%s to %s failed: %v", operation, host, err)
	}
	if len(body) > MaxFetchBodyBytes {
		return 0, nil, nil, fmt.Errorf("%s response from %s exceeds %d bytes", operation, host, MaxFetchBodyBytes)
	}
	return status, headers, body, nil
}

// fetchResult wraps a network response's status, headers and body into the
// {status, headers, body} object the driver hooks return. The result is built
// from JavaScript objects, not a host map, so a hook may return it as it is.
func fetchResult(r *moejs.Realm, status int, headers map[string]string, body moejs.Value) (moejs.Value, error) {
	headersObject := r.NewObject()
	for name, value := range headers {
		if err := headersObject.CreateDataPropertyOrThrow(r, r.KeyFromGoString(name), moejs.String(value)); err != nil {
			return moejs.Undefined(), err
		}
	}
	headersValue, err := r.FromGo(headersObject)
	if err != nil {
		return moejs.Undefined(), err
	}
	result := r.NewObject()
	for _, member := range []struct {
		name  string
		value moejs.Value
	}{{"status", moejs.Int(int64(status))}, {"headers", headersValue}, {"body", body}} {
		if err = result.CreateDataPropertyOrThrow(r, r.KeyFromGoString(member.name), member.value); err != nil {
			return moejs.Undefined(), err
		}
	}
	return r.FromGo(result)
}

// fetchRequest reads the utils.fetch argument: {url, method?, headers?,
// responseType?}. wantBytes is a responseType of "bytes": the body is
// returned as an ArrayBuffer instead of parsed JSON or text.
func fetchRequest(r *moejs.Realm, argument moejs.Value) (request FetchRequest, host string, wantBytes bool, err error) {
	if !argument.IsObject() {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch request must be an object")
	}
	exported, err := r.ToGoStrict(argument)
	if err != nil {
		return FetchRequest{}, "", false, err
	}
	members, ok := exported.(map[string]any)
	if !ok {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch request must be an object")
	}
	request = FetchRequest{Method: http.MethodGet}
	for name, value := range members {
		switch name {
		case "url":
			if request.URL, ok = value.(string); !ok {
				return FetchRequest{}, "", false, r.TypeError("utils.fetch url must be a string")
			}
		case "method":
			if value == nil {
				continue
			}
			method, isString := value.(string)
			if !isString {
				return FetchRequest{}, "", false, r.TypeError("utils.fetch method must be a string")
			}
			if method = strings.ToUpper(strings.TrimSpace(method)); method != "" {
				request.Method = method
			}
		case "headers":
			if value == nil {
				continue
			}
			headers, isObject := value.(map[string]any)
			if !isObject {
				return FetchRequest{}, "", false, r.TypeError("utils.fetch headers must be an object")
			}
			request.Headers = make(map[string]string, len(headers))
			for headerName, headerValue := range headers {
				text, isString := headerValue.(string)
				if !isString {
					return FetchRequest{}, "", false, r.TypeError("utils.fetch header %q must be a string", headerName)
				}
				request.Headers[strings.TrimSpace(headerName)] = text
			}
		case "responseType":
			if value == nil {
				continue
			}
			responseType, isString := value.(string)
			if !isString || (responseType != "json" && responseType != "bytes") {
				return FetchRequest{}, "", false, r.TypeError(`utils.fetch responseType must be "json" or "bytes"`)
			}
			wantBytes = responseType == "bytes"
		default:
			return FetchRequest{}, "", false, r.TypeError("utils.fetch request has unsupported member %q", name)
		}
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch supports only GET and HEAD")
	}
	if len(request.URL) > maxFetchURLBytes {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch url exceeds %d bytes", maxFetchURLBytes)
	}
	parsed, err := url.Parse(request.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch url must be an absolute http(s) URL")
	}
	if err = ValidateRequestHeaders(request.Headers); err != nil {
		return FetchRequest{}, "", false, r.TypeError("utils.fetch: %s", err)
	}
	return request, parsed.Host, wantBytes, nil
}

// uploadRequest reads the utils.uploadFile argument: {url, fileRef, fieldName?,
// filename?, mimeType?, headers?}. fileRef names the request file to send, as
// request_file:<field> or request_file:<field>#<index>.
func uploadRequest(r *moejs.Realm, argument moejs.Value) (request UploadRequest, host string, err error) {
	if !argument.IsObject() {
		return UploadRequest{}, "", r.TypeError("utils.uploadFile request must be an object")
	}
	exported, err := r.ToGoStrict(argument)
	if err != nil {
		return UploadRequest{}, "", err
	}
	members, ok := exported.(map[string]any)
	if !ok {
		return UploadRequest{}, "", r.TypeError("utils.uploadFile request must be an object")
	}
	request = UploadRequest{FieldName: "file"}
	for name, value := range members {
		switch name {
		case "url":
			text, isString := value.(string)
			if !isString {
				return UploadRequest{}, "", r.TypeError("utils.uploadFile url must be a string")
			}
			request.URL = text
		case "fileRef":
			text, isString := value.(string)
			if !isString {
				return UploadRequest{}, "", r.TypeError("utils.uploadFile fileRef must be a string")
			}
			request.FileRef = text
		case "fieldName":
			if value == nil {
				continue
			}
			text, isString := value.(string)
			if !isString || strings.TrimSpace(text) == "" {
				return UploadRequest{}, "", r.TypeError("utils.uploadFile fieldName must be a non-empty string")
			}
			request.FieldName = text
		case "filename":
			if value == nil {
				continue
			}
			text, isString := value.(string)
			if !isString {
				return UploadRequest{}, "", r.TypeError("utils.uploadFile filename must be a string")
			}
			request.Filename = text
		case "mimeType":
			if value == nil {
				continue
			}
			text, isString := value.(string)
			if !isString {
				return UploadRequest{}, "", r.TypeError("utils.uploadFile mimeType must be a string")
			}
			request.MimeType = text
		case "headers":
			if value == nil {
				continue
			}
			headers, isObject := value.(map[string]any)
			if !isObject {
				return UploadRequest{}, "", r.TypeError("utils.uploadFile headers must be an object")
			}
			request.Headers = make(map[string]string, len(headers))
			for headerName, headerValue := range headers {
				text, isString := headerValue.(string)
				if !isString {
					return UploadRequest{}, "", r.TypeError("utils.uploadFile header %q must be a string", headerName)
				}
				request.Headers[strings.TrimSpace(headerName)] = text
			}
		default:
			return UploadRequest{}, "", r.TypeError("utils.uploadFile request has unsupported member %q", name)
		}
	}
	if strings.TrimSpace(request.FileRef) == "" {
		return UploadRequest{}, "", r.TypeError("utils.uploadFile fileRef is required")
	}
	if len(request.URL) > maxFetchURLBytes {
		return UploadRequest{}, "", r.TypeError("utils.uploadFile url exceeds %d bytes", maxFetchURLBytes)
	}
	parsed, err := url.Parse(request.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return UploadRequest{}, "", r.TypeError("utils.uploadFile url must be an absolute http(s) URL")
	}
	if err = ValidateRequestHeaders(request.Headers); err != nil {
		return UploadRequest{}, "", r.TypeError("utils.uploadFile: %s", err)
	}
	return request, parsed.Host, nil
}

// yieldSlot lends the call's execution slot to other calls while it waits on
// the network. Parked runtimes are bounded by the engine's parked tokens;
// without one the call keeps its slot and waits holding it.
func (c *runtimeCall) yieldSlot() {
	select {
	case c.engine.parked <- struct{}{}:
	default:
		return
	}
	c.parked = true
	<-c.engine.semaphore
	c.holdsSlot = false
}

// reclaimSlot takes an execution slot back for the call's JavaScript, waiting
// no longer than the caller and fetchTimeout allow.
func (c *runtimeCall) reclaimSlot() bool {
	if c.holdsSlot {
		return true
	}
	timer := time.NewTimer(fetchTimeout)
	defer timer.Stop()
	select {
	case c.engine.semaphore <- struct{}{}:
		c.holdsSlot = true
	case <-c.ctx.Done():
	case <-timer.C:
	}
	<-c.engine.parked
	c.parked = false
	return c.holdsSlot
}
