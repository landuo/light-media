package autosignin

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"testing"
	"time"
)

func fixtureInventory(input Object) Inventory {
	rows := func(key string, defaults []Object) []Object {
		value, exists := input[key]
		if !exists {
			return defaults
		}
		out := []Object{}
		for _, row := range value.([]any) {
			out = append(out, row.(Object))
		}
		return out
	}
	configured := rows("configured", []Object{{"id": json.Number("1"), "name": "Alpha"}, {"id": json.Number("2"), "name": "Beta"}})
	return Inventory{Configured: configured, Indexers: rows("indexers", []Object{{"id": json.Number("1"), "name": "Alpha", "url": "https://alpha.invalid/", "domain": "alpha.invalid"}, {"id": json.Number("2"), "name": "Beta", "url": "https://beta.invalid/", "domain": "beta.invalid"}}), Custom: rows("custom", nil), CustomEnabled: input["custom_enabled"]}
}
func canonical(value any) any {
	raw, _ := json.Marshal(value)
	var out any
	Decode(raw, &out)
	return out
}
func TestActualInstalledConfigResultAndFormCore(t *testing.T) {
	raw, err := readAutoSignInFixture("oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Name, Kind, Error string
			Input             Object
			Expected          any
			PartialTrace      any `json:"partial_trace"`
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	executed := 0
	for _, c := range source.Cases {
		if c.Kind != "init" && c.Kind != "form" && c.Kind != "result" && c.Kind != "status" {
			continue
		}

		executed++
		t.Run(c.Name, func(t *testing.T) {
			var got any
			var runErr error
			var engine *Engine
			switch c.Kind {
			case "init":
				timezone := "UTC"
				if value, ok := c.Input["timezone"].(string); ok {
					timezone = value
				}
				engine, runErr = NewAtZone(fixtureInventory(c.Input), func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }, timezone)
				if runErr != nil {
					t.Fatal(runErr)
				}
				random := []int{120, 360}
				if supplied, ok := c.Input["random"].([]any); ok {
					random = nil
					for _, n := range supplied {
						x, _ := n.(json.Number).Int64()
						random = append(random, int(x))
					}
				}
				for _, step := range c.Input["steps"].([]any) {
					if runErr = engine.Init(step.(Object)["config"]); runErr != nil {
						break
					}
					if Truth(step.(Object)["service"]) {
						services, err := engine.Services(func(lo, hi int) (int, error) {
							if len(random) == 0 {
								return 0, fmt.Errorf("missing random oracle input")
							}
							value := random[0]
							random = random[1:]
							return value, nil
						}, "UTC")
						if err != nil {
							t.Fatal(err)
						}
						engine.trace(Object{"op": "service", "value": services})
					}
				}
				got = engine.Snapshot()
			case "form":
				form, err := Form(fixtureInventory(c.Input))
				runErr = err
				got = []any{form["conf"], form["model"]}
			case "result":
				got = SiteResult("Synthetic", c.Input["state"], fmt.Sprint(c.Input["message"]), fmt.Sprint(c.Input["action"]))
			case "status":
				got = StatusMeta(c.Input["message"])
			}
			if c.Error != "" {
				if runErr == nil || runErr.Error() != c.Error {
					t.Fatal("source exception differs", runErr, c.Error)
				}
				if engine != nil && !equivalent(canonical(engine.Trace), c.PartialTrace) {
					t.Fatal("pre-error side effects differ")
				}
				return
			}
			if runErr != nil || !equivalent(canonical(got), c.Expected) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(c.Expected)
				t.Fatalf("actual installed source differs err=%v\ngot=%s\nwant=%s", runErr, a, b)
			}
		})
	}
	if executed != 60 {
		t.Fatal("core reference inventory changed", executed)
	}
}

func TestFalsyInitKeepsInstanceStateFullReloadStartsFresh(t *testing.T) {
	inventory := fixtureInventory(Object{})
	engine := New(inventory, nil)
	config := Object{"enabled": true, "sign_sites": []any{json.Number("1")}, "retry_keyword": "错误|失败"}
	if err := engine.Init(config); err != nil {
		t.Fatal(err)
	}
	engine.StartTime, engine.EndTime = json.Number("9"), json.Number("23")
	if err := engine.Init(nil); err != nil || !Truth(engine.Enabled) || !engine.SchemaLoaded || engine.StartTime != json.Number("9") {
		t.Fatal("falsy init erased retained source state", err)
	}
	if err := engine.Init(Object{"enabled": false}); err != nil || engine.SchemaLoaded != true || engine.StartTime != json.Number("9") {
		t.Fatal("disabled config init erased prior state", err)
	}
	fresh := New(inventory, nil)
	if err := fresh.Init(Object{"enabled": false}); err != nil || fresh.SchemaLoaded || fresh.StartTime != nil || fresh.EndTime != nil {
		t.Fatal("full reload reused old instance state", err)
	}
}

// JSON integer and float spellings carry the same mathematical value at the
// original Python equality/UI number boundary. Booleans remain distinct.
func equivalent(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		p, ok := new(big.Rat).SetString(string(x))
		if !ok {
			return false
		}
		q, ok := new(big.Rat).SetString(string(y))
		return ok && p.Cmp(q) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !equivalent(v, y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			other, exists := y[k]
			if !exists || !equivalent(v, other) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
