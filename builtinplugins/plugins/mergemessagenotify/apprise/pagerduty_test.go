package apprise

import "testing"

func TestSourcePagerDuty199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/pagerduty-1.9.9.json")
}
