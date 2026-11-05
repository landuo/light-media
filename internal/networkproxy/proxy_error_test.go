package networkproxy

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRoutedTransportErrorRetainsClassAndRedactsAllFormatting(t *testing.T) {
	err := routedTransportError{cause: fmt.Errorf("owned-user:owned-private-password: %w", io.ErrUnexpectedEOF)}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("network cause was erased")
	}
	for _, message := range []string{err.Error(), fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if message != "proxy_transport_error" || strings.Contains(message, "owned-") {
			t.Fatal("public proxy diagnostic leaked cause", message)
		}
	}
}
