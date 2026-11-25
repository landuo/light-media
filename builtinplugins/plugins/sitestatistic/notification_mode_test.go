package sitestatistic

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
)

func TestOriginalNotificationModeReadStream(t *testing.T) {
	raw, e := os.ReadFile("reference/notification-mode-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Name              string `json:"case"`
			Day               string
			Current, Previous []Data
			ModeReads         []struct{ Value any } `json:"mode_reads"`
			Notices           []struct{ Text string }
			Last              json.RawMessage `json:"last_notify"`
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			p, s := fixedPlugin(), newFixture()
			index := 2 // Entry gate and debug reads precede sendSelected.
			mode := func() any {
				if index >= len(c.ModeReads) {
					t.Fatal("extra mode read")
				}
				v := c.ModeReads[index].Value
				index++
				return v
			}
			if e := sendSelected(context.Background(), sdkHost{caller: s.host}, Selected{Day: c.Day, Current: c.Current, Previous: c.Previous}, mode, p.now, new(sync.Mutex)); e != nil {
				t.Fatal(e)
			}
			if index != len(c.ModeReads) || len(s.notices) != 1 || s.notices[0]["text"] != c.Notices[0].Text {
				t.Fatal("source mixed-mode message/read sequence changed", index, s.notices)
			}
			sameSourceJSON(t, "source mixed-mode receipt", s.store["last_notify"], c.Last)
		})
	}
}
