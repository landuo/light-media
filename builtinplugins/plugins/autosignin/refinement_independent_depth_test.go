package autosignin

import (
	"context"
	"os"
	"strconv"
	"testing"
)

func TestRefinementActualHTTPJSONDepthBoundary(t *testing.T) {
	for _, name := range []string{"depth-source333.json", "depth-near-limit-source333.json", "depth-minimal-limit-source333.json"} {
		raw, e := os.ReadFile(edgeReviewRoot + name)
		if e != nil {
			t.Fatal(e)
		}
		var f struct {
			Rows []struct {
				Depth int
				Text  string
				Hares struct {
					Result []any
					Error  *string
				}
			}
		}
		if e = Decode(raw, &f); e != nil {
			t.Fatal(e)
		}
		for _, c := range f.Rows {
			t.Run(name+"/"+strconv.Itoa(c.Depth), func(t *testing.T) {
				p := &edgeHaresPeer{text: c.Text}
				v, e := JSONSignin(AdapterHares, context.Background(), Object{"name": "Owned", "cookie": "owned", "ua": "Owned/1", "timeout": 20}, SiteVisitHooks{HTTP: p})
				if c.Hares.Error != nil {
					if e == nil || e.Error() != *c.Hares.Error {
						t.Fatalf("depth=%d targetstate=%v err=%v source=%s", c.Depth, v.State, e, *c.Hares.Error)
					}
				} else if e != nil || v.State != true {
					t.Fatalf("source accepts depth%d target=%v/%v", c.Depth, v, e)
				}
			})
		}
	}
}
