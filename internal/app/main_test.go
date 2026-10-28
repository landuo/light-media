package app

import (
	"os"
	"testing"
)

// Isolated tests must not register synthetic SDK installations with the public
// center. Explicit owned URLs in each test still override this empty default;
// child cmd/server fixtures inherit the same isolation boundary.
func TestMain(m *testing.M) {
	_ = os.Setenv("MP_SERVER_HOST", "")
	os.Exit(m.Run())
}
