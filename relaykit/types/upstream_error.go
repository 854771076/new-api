package types

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"
)

// The operator-facing summaries of the upstream transport failures. They only
// describe the failure type and the timing facts an operator needs; they
// never carry credentials, upstream URLs, request bodies or response bodies.
const (
	upstreamReasonTimeout            = "upstream did not answer before the gateway gave up"
	upstreamReasonConnectionRefused  = "connection refused by upstream"
	upstreamReasonConnectionReset    = "upstream reset the connection"
	upstreamReasonDNS                = "upstream hostname could not be resolved"
	upstreamReasonTLS                = "TLS handshake with upstream failed"
	upstreamReasonNetworkUnreachable = "network to upstream is unreachable"
	upstreamReasonUnexpectedEOF      = "upstream closed the connection unexpectedly"
	upstreamReasonClientCanceled     = "client canceled the request"
	upstreamReasonDialFailed         = "could not open a connection to the upstream"
	upstreamReasonConnectionBroken   = "the connection to the upstream broke mid-request"
	upstreamReasonHTTP2GoAway        = "upstream closed the HTTP/2 connection with a GOAWAY frame"
	upstreamReasonHTTP2StreamError   = "upstream reset the HTTP/2 stream"
	upstreamReasonUnknown            = "upstream request failed for an unknown reason"
)

// upstreamErrorCodePrefix marks the error codes that name a failure of the
// upstream transport layer. The codes share the prefix so a new transport
// failure joins the family without touching the callers that classify them.
const upstreamErrorCodePrefix = "upstream_"

// IsUpstreamTransportError reports whether the code identifies a failure of
// the upstream transport layer, meaning the request never produced an
// upstream response.
//
// It covers the whole "upstream_" family plus the catch-all code
// ErrorCodeDoRequestFailed, which stays the fallback for a transport failure
// that matches no known shape. Callers must not compare the code with a
// single constant: the family grows with every new transport failure, and a
// per-code comparison silently drops the codes it does not know.
func IsUpstreamTransportError(code ErrorCode) bool {
	if code == ErrorCodeDoRequestFailed {
		return true
	}
	return strings.HasPrefix(string(code), upstreamErrorCodePrefix)
}

// UpstreamFailure is the classified result of a transport-level failure.
type UpstreamFailure struct {
	// Code is the fine-grained error code that identifies the failure.
	Code ErrorCode
	// Reason is a sanitized, operator-facing summary of the cause.
	Reason string
}

// WithElapsed appends how long the failure took to the summary. The duration
// is the diagnostic fact for an upstream that only gives up on its own
// timeout, so it is part of the reason there. Failures that happened
// immediately keep the plain summary.
func (f UpstreamFailure) WithElapsed(elapsed time.Duration) UpstreamFailure {
	if elapsed < time.Second {
		return f
	}
	f.Reason = fmt.Sprintf("%s after %ds", f.Reason, int(elapsed.Seconds()))
	return f
}

// UpstreamFailureForCode returns the sanitized operator-facing summary of a
// fine-grained upstream transport failure code.
//
// The host module uses it when it recognizes a transport failure shape that
// this module cannot see by itself — an HTTP/2 protocol error raised by the
// outbound HTTP client, for example. The summary stays here so the vocabulary
// of the operator-facing reasons has a single owner.
func UpstreamFailureForCode(code ErrorCode) UpstreamFailure {
	switch code {
	case ErrorCodeUpstreamHTTP2GoAway:
		return UpstreamFailure{Code: code, Reason: upstreamReasonHTTP2GoAway}
	case ErrorCodeUpstreamHTTP2StreamError:
		return UpstreamFailure{Code: code, Reason: upstreamReasonHTTP2StreamError}
	default:
		return UpstreamFailure{Code: ErrorCodeDoRequestFailed, Reason: upstreamReasonUnknown}
	}
}

// ClassifyUpstreamError maps a transport-level error onto a fine-grained error
// code and an operator-facing summary.
//
// The classification walks the error chain with errors.Is / errors.As; it
// never matches on the error text, so a provider that rewords its errors
// cannot change the classification. ErrorCodeDoRequestFailed stays the
// fallback for a failure that matches none of the known shapes.
func ClassifyUpstreamError(err error) UpstreamFailure {
	if err == nil {
		return UpstreamFailure{Code: ErrorCodeDoRequestFailed, Reason: upstreamReasonUnknown}
	}

	// A canceled context means the caller went away before the upstream
	// answered, a deadline means the gateway ran out of time. Both are
	// checked first because a canceled context is also reported as a generic
	// transport failure and a deadline also satisfies net.Error.Timeout.
	if errors.Is(err, context.Canceled) {
		return UpstreamFailure{Code: ErrorCodeUpstreamClientCanceled, Reason: upstreamReasonClientCanceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return UpstreamFailure{Code: ErrorCodeUpstreamTimeout, Reason: upstreamReasonTimeout}
	}

	// A TLS failure is wrapped in a *net.OpError, so it has to be recognized
	// before the socket errors below.
	if isTLSFailure(err) {
		return UpstreamFailure{Code: ErrorCodeUpstreamTLSError, Reason: upstreamReasonTLS}
	}

	// A name resolution failure is wrapped in a *net.OpError as well.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return UpstreamFailure{Code: ErrorCodeUpstreamDNSError, Reason: upstreamReasonDNS}
	}

	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return UpstreamFailure{Code: ErrorCodeUpstreamConnectionRefused, Reason: upstreamReasonConnectionRefused}
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED), errors.Is(err, syscall.EPIPE):
		return UpstreamFailure{Code: ErrorCodeUpstreamConnectionReset, Reason: upstreamReasonConnectionReset}
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.ENETDOWN):
		return UpstreamFailure{Code: ErrorCodeUpstreamNetworkUnreachable, Reason: upstreamReasonNetworkUnreachable}
	case errors.Is(err, syscall.ETIMEDOUT):
		return UpstreamFailure{Code: ErrorCodeUpstreamTimeout, Reason: upstreamReasonTimeout}
	}

	// The upstream closed the connection without answering. This is checked
	// before the generic socket handling because an EOF may travel inside a
	// *net.OpError, and the "upstream hung up" diagnosis is more precise
	// than "the connection broke".
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return UpstreamFailure{Code: ErrorCodeUpstreamUnexpectedEOF, Reason: upstreamReasonUnexpectedEOF}
	}
	if errors.Is(err, io.EOF) {
		return UpstreamFailure{Code: ErrorCodeUpstreamUnexpectedEOF, Reason: upstreamReasonUnexpectedEOF}
	}

	// A socket deadline (dial timeout, response header timeout) is reported
	// through net.Error. The concrete error type is unexported in net/http,
	// so the interface is the only way to recognize it.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return UpstreamFailure{Code: ErrorCodeUpstreamTimeout, Reason: upstreamReasonTimeout}
	}

	// A socket error without a recognized errno still says in which phase the
	// request failed: dialing never produced a connection, anything else
	// broke on an established connection.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" {
			return UpstreamFailure{Code: ErrorCodeUpstreamConnectionRefused, Reason: upstreamReasonDialFailed}
		}
		return UpstreamFailure{Code: ErrorCodeUpstreamConnectionReset, Reason: upstreamReasonConnectionBroken}
	}

	return UpstreamFailure{Code: ErrorCodeDoRequestFailed, Reason: upstreamReasonUnknown}
}

// isTLSFailure reports whether the error chain carries a TLS or certificate
// validation failure. The concrete types are inspected instead of the error
// text, so a handshake failure stays recognizable across Go versions.
func isTLSFailure(err error) bool {
	var recordHeaderErr *tls.RecordHeaderError
	if errors.As(err, &recordHeaderErr) {
		return true
	}
	var verificationErr *tls.CertificateVerificationError
	if errors.As(err, &verificationErr) {
		return true
	}
	var alertErr tls.AlertError
	if errors.As(err, &alertErr) {
		return true
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return true
	}
	var invalidCertErr x509.CertificateInvalidError
	if errors.As(err, &invalidCertErr) {
		return true
	}
	return false
}
