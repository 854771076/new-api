package channel

import (
	"errors"

	"github.com/QuantumNous/new-api/relaykit/types"

	"golang.org/x/net/http2"
)

// classifyHTTP2UpstreamFailure recognizes the two HTTP/2 shapes the outbound
// client can surface when the upstream tears the connection down: a GOAWAY
// frame, which closes the whole connection, and a RST_STREAM, which kills a
// single stream. Neither is a socket error nor a timeout, so the generic
// classifier cannot see them and they would fall into the catch-all code.
//
// The match walks the error chain with errors.As and never looks at the error
// text. The standard library keeps its own HTTP/2 error types unexported, so
// the GOAWAY and RST_STREAM errors that reach this classifier come from a
// connection built on golang.org/x/net/http2.
func classifyHTTP2UpstreamFailure(err error) (types.UpstreamFailure, bool) {
	var goAway http2.GoAwayError
	if errors.As(err, &goAway) {
		return types.UpstreamFailureForCode(types.ErrorCodeUpstreamHTTP2GoAway), true
	}
	var goAwayPointer *http2.GoAwayError
	if errors.As(err, &goAwayPointer) {
		return types.UpstreamFailureForCode(types.ErrorCodeUpstreamHTTP2GoAway), true
	}
	var streamError http2.StreamError
	if errors.As(err, &streamError) {
		return types.UpstreamFailureForCode(types.ErrorCodeUpstreamHTTP2StreamError), true
	}
	var streamErrorPointer *http2.StreamError
	if errors.As(err, &streamErrorPointer) {
		return types.UpstreamFailureForCode(types.ErrorCodeUpstreamHTTP2StreamError), true
	}
	return types.UpstreamFailure{}, false
}
