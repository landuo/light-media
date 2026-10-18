package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	bf "light-media/internal/plugins/brushflow"
	"os"
	"testing"
	"time"
)

func TestBrushFlowActualLXMLRSSFallbackSource(t *testing.T) {
	b, e := os.ReadFile("../plugins/brushflow/reference/framework/rss-fallback-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var input struct {
		Cases []struct {
			Text     string
			Expected json.RawMessage
		}
	}
	if e = json.Unmarshal(b, &input); e != nil {
		t.Fatal(e)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	for i, c := range input.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, e := brushFlowRecoverRSS(c.Text, loc)
			if string(c.Expected) == "false" || string(c.Expected) == "null" {
				if e == nil && len(got) > 0 {
					t.Fatal("invalid source fallback accepted", got)
				}
				return
			}
			var want []bf.Row
			decoder := json.NewDecoder(bytes.NewReader(c.Expected))
			decoder.UseNumber()
			if decoder.Decode(&want) != nil {
				t.Fatal(string(c.Expected))
			}
			if e != nil || !brushFlowSameJSON(got, want) {
				t.Fatal("fallback source mismatch", e, string(mustCleanSeedJSON(got)), string(c.Expected))
			}
		})
	}
}
