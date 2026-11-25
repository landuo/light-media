package apprise

import "testing"

func TestSourcePushMe199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/pushme-1.9.9.json")
}
