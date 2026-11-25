package sitestatistic

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Independent prior source observations of the same Python str/repr builtins;
// these are formatter cases, not Site API/site-network acceptance.
func TestSourceRawConfigurationPythonText(t *testing.T) {
	raw, e := os.ReadFile("reference/python-repr-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			InputJSON  string   `json:"input_json"`
			InputRepr  string   `json:"input_repr"`
			WireValues []string `json:"wire_values"`
		} `json:"cases"`
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, c := range oracle.Cases {
		t.Run(c.InputJSON, func(t *testing.T) {
			got, e := sourceConfigText(json.RawMessage(c.InputJSON))
			want := c.InputRepr
			if len(c.WireValues) > 0 {
				want = c.WireValues[0]
			}
			if e != nil || got != want {
				t.Fatalf("got %q %v; source %q", got, e, want)
			}
		})
	}
	p, s := fixedPlugin(), newFixture()
	s.p = p
	if _, e = p.Handle(t.Context(), "init", json.RawMessage(`{"enabled":false,"notify_type":{"z":1,"a":[false,null],"z":2}}`), s.host); e != nil {
		t.Fatal(e)
	}
	invoke(t, p, s, "send_msg", map[string]any{"data": map[string]any{"site_id": "*"}})
	if len(s.logs) == 0 || !strings.Contains(s.logs[0].Message, "{'z': 2, 'a': [False, None]}") {
		t.Fatal("raw configuration log order/type changed", s.logs)
	}
}

func TestSourceOneShotConfigRetainsRawValuesAndKeySlots(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{`{"notify_type":{"z":1,"a":2},"onlyonce":true,"other":1267650600228229401496703205376}`, `{"notify_type":{"z":1,"a":2},"onlyonce":false,"other":1267650600228229401496703205376}`},
		{`{}`, `{"onlyonce":false}`},
		{`{"onlyonce":true,"x":1,"x":2}`, `{"onlyonce":false,"x":2}`},
		{`{"\ud800":1,"\ud801":2,"onlyonce":true}`, `{"\ud800":1,"\ud801":2,"onlyonce":false}`},
	} {
		got, e := onlyOnceConfig(json.RawMessage(c.input))
		if e != nil || string(got) != c.want {
			t.Fatal("source config mutation/order changed", string(got), e, c.want)
		}
	}
}
