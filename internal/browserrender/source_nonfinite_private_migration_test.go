package browserrender

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Fixed original nonfinite6: FS never sends request.get, Cloak's +Inf keeps
// waiting for quiet; negativeInf/NaN timer fires after goto. Actual App test
// separately proves the real navigation/HTML/log effects with the same browser.
func TestSourceNonfiniteTimeoutPrivateBranches(t *testing.T) {
	for _, r := range []struct {
		raw      string
		negative bool
	}{{"Infinity", false}, {"-Infinity", true}, {"NaN", true}} {
		t.Run(r.raw, func(t *testing.T) {
			var hits atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
			defer peer.Close()
			levels := []string{}
			solution, e := (Config{FlareSolverrURL: peer.URL}).flareSolverr(context.Background(), Request{URL: "https://hdsky.me/", SourceTimeout: json.RawMessage(r.raw), SourceLog: func(level, code string) { levels = append(levels, level) }})
			if e == nil || solution != nil || hits.Load() != 0 || len(levels) != 1 || levels[0] != "ERROR" {
				t.Fatal("original FS nonfinite branch", e, solution, hits.Load(), levels)
			}
			duration, e := sourceCloakTimeout(json.RawMessage(r.raw))
			if e != nil || (duration < 0) != r.negative || !r.negative && duration != 0 || duration > time.Second {
				t.Fatal("original Cloak timer", duration, e)
			}
		})
	}
}
