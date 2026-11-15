package app

import (
	"fmt"
	"os"
	"testing"
)

// Isolated tests must not register synthetic SDK installations with the public
// center. Explicit owned URLs in each test still override this empty default;
// child cmd/server fixtures inherit the same isolation boundary.
func TestMain(m *testing.M) {
	_ = os.Setenv("MP_SERVER_HOST", "")
	// Most tests in this package drive real SDK fixture workers supplied through
	// the OWNED_*_BINARY variables. Without them a bare `go test ./...` reports
	// fixture-missing failures rather than meaningful results, so skip the
	// package and point at the documented fixture-driven command instead.
	if os.Getenv("OWNED_TRANSFER_EVENTS_BINARY") == "" && os.Getenv("OWNED_MODULE_OVERRIDE_BINARY") == "" {
		fmt.Fprintln(os.Stderr, "skipping light-media/internal/app: set OWNED_*_BINARY fixture workers (see docs/history-mp-final-delivery-20261115.md)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
