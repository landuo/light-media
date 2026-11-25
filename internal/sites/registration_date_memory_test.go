package sites

import (
	"testing"
	"time"
)

func TestRegistrationDatesThroughTrackerAdapters(t *testing.T) {
	local := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, local)
	for _, tc := range []struct{ raw, want string }{
		{"2024-04-13 09:17:03", "2024-04-13 09:17:03"},
		{"2024-04-13T01:17:03Z", "2024-04-13 01:17:03"},
		{"13 avril 2024", "2024-04-13 00:00:00"},
		{"not a registration date", "not a registration date"},
	} {
		if got := sourceNexusJoinedAt(tc.raw, now); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.raw, got, tc.want)
		}
	}
}
