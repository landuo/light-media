package mpserverstatus

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
)

func normalizeSourceFloat(v any) any {
	switch x := v.(type) {
	case float64:
		label := ""
		switch {
		case math.IsInf(x, 1):
			label = "inf"
		case math.IsInf(x, -1):
			label = "-inf"
		case math.IsNaN(x):
			label = "nan"
		}
		if label != "" {
			return Object{"source_float": label}
		}
		return x
	case Object:
		out := Object{}
		for k, v := range x {
			out[k] = normalizeSourceFloat(v)
		}
		return out
	}
	return v
}
func invokeEdge(c Object) (got any, err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", p)
			}
		}
	}()
	switch c["kind"] {
	case "date":
		return ResponseDate(c["input"].(string)), nil
	case "parse":
		return Parse(c["input"].(string))
	case "metrics":
		input := c["input"].(Object)
		m := New(nil, func() float64 { return number(input["at"]) })
		if last, ok := input["last"].(Object); ok {
			m.Last = &Sample{Timestamp: number(last["timestamp"]), Accepts: last["accepts"].(json.Number), Handled: last["handled"].(json.Number), Requests: last["requests"].(json.Number)}
		}
		got, err = m.Metrics(input["status"].(Object), number(input["seconds"]))
		return normalizeSourceFloat(got), err
	case "nonfinite-format":
		input := c["input"].(Object)
		label := input["value"].(Object)["source_float"]
		v := math.NaN()
		if label == "inf" {
			v = math.Inf(1)
		} else if label == "-inf" {
			v = math.Inf(-1)
		}
		return callFormat(input["method"].(string), []any{v}), nil
	}
	return nil, fmt.Errorf("unknown edge")
}
func TestActualSourceAdditionalEdges(t *testing.T) {
	raw, e := os.ReadFile("reference/extra-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []Object `json:"cases"`
	}
	if e = decode(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, c := range fixture.Cases {
		t.Run(c["name"].(string), func(t *testing.T) {
			got, e := invokeEdge(c)
			if expected, ok := c["error"]; ok {
				if e == nil || e.Error() != expected {
					t.Fatalf("error=%v want %s", e, expected)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if !equivalent(canonical(got), c["expected"]) {
				t.Errorf("source mismatch got=%v want=%v", got, c["expected"])
			}
		})
	}
}
