package app

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPluginNoticeV2TMDBMediaInfoOriginalAST(t *testing.T) {
	raw, e := os.ReadFile("testdata/notice-media-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Name, Kind, Category string
			Raw, Expected        json.RawMessage
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Cases) != 80 {
		t.Fatal("original AST fixture truncated")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, e := sourceTMDBMediaInfo(c.Raw, c.Kind, "", c.Category)
			if e != nil {
				t.Fatal(e)
			}
			if !larkSameRaw(mustCleanSeedJSON(got), mustCleanSeedJSON(c.Expected)) {
				var g, w map[string]any
				json.Unmarshal(got, &g)
				json.Unmarshal(c.Expected, &w)
				for k, v := range w {
					a, b := mustCleanSeedJSON(g[k]), mustCleanSeedJSON(v)
					if !larkSameRaw(a, b) {
						t.Errorf("source field %s differs native=%s original=%s", k, a, b)
					}
				}
				for k := range g {
					if _, ok := w[k]; !ok {
						t.Errorf("native invented source attribute %s", k)
					}
				}
			}
		})
	}
}
