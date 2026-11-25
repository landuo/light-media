package sourceinterfaces

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	plugins "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/categoryeditor"
	"light-media/builtinplugins/plugins/mpserverstatus"
	"light-media/builtinplugins/plugins/pluginuninstall"
	"light-media/builtinplugins/plugins/removelink"
	"light-media/builtinplugins/plugins/sitestatistic"
	"light-media/builtinplugins/plugins/torrenttransfer"
)

// The installed Python Class+Base+ObjectUtils AST was executed independently in
// fixed Python3.12. These declarations reflect check_method at each source
// discovery gate. Auth keeps its distinct state-first order on the host; API
// and page are direct nullable entries and are excluded from this predicate.
func TestSourceCheckedManifestDeclarations(t *testing.T) {
	raw, e := os.ReadFile("testdata/source-oracle.json")
	if e != nil {
		t.Fatal("executed source oracle required", e)
	}
	var source struct {
		Inventory []struct {
			Plugin     string
			Interfaces map[string]struct {
				CheckMethod bool `json:"check_method"`
			}
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Inventory) != 7 {
		t.Fatal("source7 inventory", e)
	}
	manifests := map[string]plugins.Manifest{"categoryeditor": categoryeditor.Manifest(), "torrenttransfer": torrenttransfer.Manifest(), "torrenttransfermusic": torrenttransfer.Manifest(), "removelink": removelink.Manifest(), "sitestatistic": sitestatistic.Manifest(), "mpserverstatus": mpserverstatus.Manifest(), "pluginuninstall": pluginuninstall.Manifest()}
	for _, row := range source.Inventory {
		t.Run(row.Plugin, func(t *testing.T) {
			m, ok := manifests[row.Plugin]
			if !ok {
				t.Fatal("unmapped source instance")
			}
			for _, method := range []string{"get_module", "get_service", "get_command", "get_dashboard", "get_dashboard_meta", "get_auth_providers"} {
				want, ok := row.Interfaces[method]
				if !ok {
					t.Fatal("source field absent", method)
				}
				if got := slices.Contains(m.Methods, method); got != want.CheckMethod {
					t.Errorf("%s advertised=%v, executed source check_method=%v", method, got, want.CheckMethod)
				}
			}
		})
	}
}
