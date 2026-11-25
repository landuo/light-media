package apprise

import "testing"

func TestSourceWebexTeams199(t *testing.T) {
	testSourceHTTPProvider(t, "testdata/webexteams-1.9.9.json")
}
