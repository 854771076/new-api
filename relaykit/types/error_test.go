package types

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// timeoutError is a socket deadline error. net/http reports its response
// header timeout through an unexported type, so the tests use the exported
// interface the classifier relies on.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// osSyscallError mirrors the shape the net package uses for a failed socket
// syscall, so the errno stays reachable through the error chain.
func osSyscallError(errno syscall.Errno) error {
	return &os.SyscallError{Syscall: "connect", Err: errno}
}

// TestMaskSensitiveErrorWithStatusCodeIncludesTheErrorCode pins the content
// format of an error log record: the status code, the fine-grained error code
// and the masked message. The message alone did not say what went wrong.
func TestMaskSensitiveErrorWithStatusCodeIncludesTheErrorCode(t *testing.T) {
	err := NewError(errors.New("upstream error: do request failed"), ErrorCodeUpstreamTimeout)
	assert.Equal(t,
		"status_code=500, error_code=upstream_timeout, upstream error: do request failed",
		err.MaskSensitiveErrorWithStatusCode())

	withoutStatus := NewError(errors.New("upstream error: do request failed"), ErrorCodeUpstreamTimeout,
		ErrOptionWithStatusCode(0))
	assert.Equal(t,
		"error_code=upstream_timeout, upstream error: do request failed",
		withoutStatus.MaskSensitiveErrorWithStatusCode())
}

// TestAdminReasonStaysOffTheClientResponse pins that the operator-facing
// reason rides the error for the log record only: it never becomes the
// message a client receives.
func TestAdminReasonStaysOffTheClientResponse(t *testing.T) {
	err := NewError(
		errors.New("upstream error: do request failed"),
		ErrorCodeUpstreamConnectionRefused,
		ErrOptionWithAdminReason("connection refused by upstream"),
	)
	assert.Equal(t, "connection refused by upstream", err.AdminReason())
	assert.Equal(t, "upstream error: do request failed", err.ToOpenAIError().Message)
	assert.Equal(t, "upstream error: do request failed", err.ToClaudeError().Message)
	assert.NotContains(t, err.ToOpenAIError().Message, "connection refused")
}

func TestClassifyUpstreamErrorMapsTransportFailuresToFineGrainedCodes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantCode   ErrorCode
		wantReason string
	}{
		{
			name:       "nil error falls back to the catch-all code",
			err:        nil,
			wantCode:   ErrorCodeDoRequestFailed,
			wantReason: upstreamReasonUnknown,
		},
		{
			name:       "context deadline exceeded is an upstream timeout",
			err:        context.DeadlineExceeded,
			wantCode:   ErrorCodeUpstreamTimeout,
			wantReason: upstreamReasonTimeout,
		},
		{
			name:       "wrapped context deadline exceeded is an upstream timeout",
			err:        fmt.Errorf("do request failed: %w", context.DeadlineExceeded),
			wantCode:   ErrorCodeUpstreamTimeout,
			wantReason: upstreamReasonTimeout,
		},
		{
			name:       "context canceled is a client cancellation",
			err:        context.Canceled,
			wantCode:   ErrorCodeUpstreamClientCanceled,
			wantReason: upstreamReasonClientCanceled,
		},
		{
			name: "url error wrapping a canceled context is a client cancellation",
			err: &url.Error{
				Op:  "Get",
				URL: "https://upstream.example.com/v1/chat/completions",
				Err: context.Canceled,
			},
			wantCode:   ErrorCodeUpstreamClientCanceled,
			wantReason: upstreamReasonClientCanceled,
		},
		{
			name: "dial refused by the upstream",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &net.OpError{Op: "dial", Net: "tcp", Err: osSyscallError(syscall.ECONNREFUSED)},
			},
			wantCode:   ErrorCodeUpstreamConnectionRefused,
			wantReason: upstreamReasonConnectionRefused,
		},
		{
			name: "connection reset while reading the upstream response",
			err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: osSyscallError(syscall.ECONNRESET),
			},
			wantCode:   ErrorCodeUpstreamConnectionReset,
			wantReason: upstreamReasonConnectionReset,
		},
		{
			name:       "broken pipe is a reset connection",
			err:        osSyscallError(syscall.EPIPE),
			wantCode:   ErrorCodeUpstreamConnectionReset,
			wantReason: upstreamReasonConnectionReset,
		},
		{
			name:       "unreachable network",
			err:        osSyscallError(syscall.ENETUNREACH),
			wantCode:   ErrorCodeUpstreamNetworkUnreachable,
			wantReason: upstreamReasonNetworkUnreachable,
		},
		{
			name:       "socket timeout",
			err:        timeoutError{},
			wantCode:   ErrorCodeUpstreamTimeout,
			wantReason: upstreamReasonTimeout,
		},
		{
			name:       "socket timeout reported through a url error",
			err:        &url.Error{Op: "Get", URL: "https://upstream.example.com", Err: timeoutError{}},
			wantCode:   ErrorCodeUpstreamTimeout,
			wantReason: upstreamReasonTimeout,
		},
		{
			name:       "name resolution failure",
			err:        &net.DNSError{Err: "no such host", Name: "upstream.example.com", IsNotFound: true},
			wantCode:   ErrorCodeUpstreamDNSError,
			wantReason: upstreamReasonDNS,
		},
		{
			name: "name resolution failure wrapped in a socket error",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &net.DNSError{Err: "no such host", Name: "upstream.example.com", IsNotFound: true},
			},
			wantCode:   ErrorCodeUpstreamDNSError,
			wantReason: upstreamReasonDNS,
		},
		{
			name:       "unknown certificate authority",
			err:        x509.UnknownAuthorityError{},
			wantCode:   ErrorCodeUpstreamTLSError,
			wantReason: upstreamReasonTLS,
		},
		{
			name: "certificate verification failure",
			err: &tls.CertificateVerificationError{
				Err: x509.UnknownAuthorityError{},
			},
			wantCode:   ErrorCodeUpstreamTLSError,
			wantReason: upstreamReasonTLS,
		},
		{
			name:       "the upstream did not answer with a TLS handshake",
			err:        &tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"},
			wantCode:   ErrorCodeUpstreamTLSError,
			wantReason: upstreamReasonTLS,
		},
		{
			name:       "the upstream closed the connection without answering",
			err:        io.EOF,
			wantCode:   ErrorCodeUpstreamUnexpectedEOF,
			wantReason: upstreamReasonUnexpectedEOF,
		},
		{
			name:       "the upstream closed the connection in the middle of the response",
			err:        io.ErrUnexpectedEOF,
			wantCode:   ErrorCodeUpstreamUnexpectedEOF,
			wantReason: upstreamReasonUnexpectedEOF,
		},
		{
			name:       "an unknown failure keeps the catch-all code",
			err:        errors.New("something went wrong"),
			wantCode:   ErrorCodeDoRequestFailed,
			wantReason: upstreamReasonUnknown,
		},
		{
			name: "a socket error without a known errno still names the phase",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New("no route"),
			},
			wantCode:   ErrorCodeUpstreamConnectionRefused,
			wantReason: upstreamReasonDialFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := ClassifyUpstreamError(tc.err)
			assert.Equal(t, tc.wantCode, failure.Code)
			assert.Equal(t, tc.wantReason, failure.Reason)
		})
	}
}

// TestIsUpstreamTransportErrorCoversTheWholeUpstreamFamily pins the semantic
// transport check: every fine-grained upstream code and the catch-all code are
// transport failures, while a local or a channel code is not. A caller that
// compared the code with a single constant dropped the codes it did not know,
// which turned upstream faults into local rejections.
func TestIsUpstreamTransportErrorCoversTheWholeUpstreamFamily(t *testing.T) {
	for _, code := range []ErrorCode{
		ErrorCodeUpstreamTimeout,
		ErrorCodeUpstreamConnectionRefused,
		ErrorCodeUpstreamConnectionReset,
		ErrorCodeUpstreamDNSError,
		ErrorCodeUpstreamTLSError,
		ErrorCodeUpstreamNetworkUnreachable,
		ErrorCodeUpstreamUnexpectedEOF,
		ErrorCodeUpstreamClientCanceled,
		ErrorCodeUpstreamIdleTimeout,
		ErrorCodeDoRequestFailed,
	} {
		assert.True(t, IsUpstreamTransportError(code), "%s is a transport failure", code)
	}

	for _, code := range []ErrorCode{
		"",
		ErrorCodeInvalidRequest,
		ErrorCodeBadResponseStatusCode,
		ErrorCodeChannelInvalidKey,
		ErrorCodeChannelNoAvailableKey,
	} {
		assert.False(t, IsUpstreamTransportError(code), "%s is not a transport failure", code)
	}
}

// TestClassifyUpstreamErrorKeepsSecretsOutOfTheReason pins the sanitization
// boundary: the summary describes the failure type, it never repeats the
// upstream URL, the query or the body of the request that failed.
func TestClassifyUpstreamErrorKeepsSecretsOutOfTheReason(t *testing.T) {
	err := &url.Error{
		Op:  "Post",
		URL: "https://api-key.example.com/v1/chat/completions?token=super-secret",
		Err: io.EOF,
	}
	failure := ClassifyUpstreamError(err)
	assert.Equal(t, ErrorCodeUpstreamUnexpectedEOF, failure.Code)
	assert.NotContains(t, failure.Reason, "super-secret")
	assert.NotContains(t, failure.Reason, "api-key.example.com")
	assert.NotContains(t, failure.Reason, "token=")
}

// TestUpstreamFailureWithElapsed pins that the duration reaches the summary
// as the diagnostic fact, and that an immediate failure keeps the plain
// summary.
func TestUpstreamFailureWithElapsed(t *testing.T) {
	failure := ClassifyUpstreamError(io.EOF).WithElapsed(360 * time.Second)
	assert.Equal(t, ErrorCodeUpstreamUnexpectedEOF, failure.Code)
	assert.Equal(t, "upstream closed the connection unexpectedly after 360s", failure.Reason)

	immediate := ClassifyUpstreamError(io.EOF).WithElapsed(120 * time.Millisecond)
	assert.Equal(t, "upstream closed the connection unexpectedly", immediate.Reason)
}
