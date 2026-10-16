package autosignin

import (
	"testing"
	"time"
)

func TestActualInstalledFullPageTreeAndRawPinnedStyles(t *testing.T) {
	raw, err := readAutoSignInFixture("oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Cases []struct {
			Name, Kind string
			Input      Object
			Expected   any
		}
	}
	if Decode(raw, &reference) != nil {
		t.Fatal("source page reference invalid")
	}
	count := 0
	for _, c := range reference.Cases {
		if c.Kind != "page" {
			continue
		}
		count++
		t.Run(c.Name, func(t *testing.T) {
			engine := New(fixtureInventory(c.Input), func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
			config := c.Input["config"]
			if config == nil {
				config = Object{}
			}
			if err := engine.Init(config); err != nil {
				t.Fatal(err)
			}
			if history, ok := c.Input["history"].(Object); ok {
				engine.Data = clone(history).(Object)
			}
			before := canonical(engine.Data)
			got, err := engine.Page()
			if err != nil || !equivalent(canonical(got), c.Expected) {
				t.Fatalf("original full tree differs err=%v\ngot=%s\nwant=%s", err, mustJSON(got), mustJSON(c.Expected))
			}
			if !equivalent(canonical(engine.Data), before) {
				t.Fatal("read-only page mutated stored history")
			}
		})
	}
	if count != 5 {
		t.Fatal("page reference inventory changed", count)
	}
}
