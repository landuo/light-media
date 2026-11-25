package autosignin

import (
	"context"
	"encoding/json"
	sdk "light-media/builtinplugins/internal/sdk"
	"math"
	"strconv"
	"strings"
	"testing"
)

// Fresh actual original Cloak+JS positive infinity oracle returns HTML after
// one GET. Valid strict JSON exponent overflow reaches that value on restore.
// This diagnostic expects the source transport to reach render, and is kept
// red until an explicit faithful value adaptation is implemented.
func TestSourceBrowserValidJSONOverflowTimeoutBoundary(t *testing.T) {
	value, err := sourceRestoreSavedJSON([]byte(`{"id":7,"url":"https://hdsky.me/","render":true,"timeout":1e309}`))
	if err != nil {
		t.Fatal(err)
	}
	site := value.(Object)
	n, ok := site["timeout"].(json.Number)
	if !ok || n.String() != "1e309" {
		t.Fatal("strict decoder did not preserve original exponent", site["timeout"])
	}
	f, _ := strconv.ParseFloat(n.String(), 64)
	if !math.IsInf(f, 1) {
		t.Fatal("source binary64 overflow differs", n)
	}
	ordinal := 0
	ref := sdk.SiteSourceReferenceV1{CustomOrdinal: &ordinal, Snapshot: strings.Repeat("a", 64)}
	site["_host_source_reference"] = ref
	calls := 0
	b := sdkBackend{host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != sdk.SiteRenderCapability {
			t.Fatal(method)
		}
		return json.RawMessage(`{"html":"owned actual JS result"}`), nil
	}}
	got, err := (sdkSiteTransport{backend: b, site: site}).Page(context.Background(), PageRequest{URL: "https://hdsky.me/attendance.php", Timeout: site["timeout"]})
	if err != nil || got != "owned actual JS result" || calls != 1 {
		t.Fatalf("source +Inf reaches browser navigation; target rejected before host: err=%v got=%q render_calls=%d", err, got, calls)
	}
}
