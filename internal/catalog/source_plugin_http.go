package catalog

import (
	"errors"
	"io"
	"net"
	"net/url"
)

// Fixed RequestUtils.request retries an idempotent request on a session once
// for connection, chunked-body or read-timeout failures. A generic request
// error and a JSON decoding error do not qualify. Unwrap net/http's URL wrapper
// before testing net.Error: the wrapper itself implements it for every cause.
func sourceProviderConnectionFailure(err error) bool {
	for {
		wrapped, ok := err.(*url.Error)
		if !ok {
			break
		}
		err = wrapped.Err
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
