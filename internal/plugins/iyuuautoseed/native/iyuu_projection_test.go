package download

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func iyuuOracle(t *testing.T) map[string]any {
	t.Helper()
	raw, e := os.ReadFile("../reference/native-projection-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := iyuuJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	return v.(map[string]any)
}
func iyuuOracleEqual(t *testing.T, a, b any) {
	t.Helper()
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	va, e := iyuuJSON(aa)
	if e != nil {
		t.Fatal(e)
	}
	vb, e := iyuuJSON(bb)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(va, vb) {
		t.Fatalf("got %s\nwant%s", aa, bb)
	}
}
func TestIYUUInstalledNativeProjectionOracle(t *testing.T) {
	doc := iyuuOracle(t)
	if len(doc["qb_completed"].([]any)) != 28 || len(doc["qb_add_response"].([]any)) != 14 || len(doc["tr_projection"].([]any)) != 9 || len(doc["versions"].([]any)) != 23 {
		t.Fatal("oracle counts")
	}
	for i, raw := range doc["qb_completed"].([]any) {
		row := raw.(map[string]any)
		want := row["result"].(map[string]any)
		got, e := iyuuCompletedQB([]map[string]any{row["input"].(map[string]any)})
		if want["exception"] != nil {
			if e == nil {
				t.Fatalf("completed case %d accepted source fault", i)
			}
			continue
		}
		if e != nil {
			t.Fatalf("completed %d %v", i, e)
		}
		v, e := iyuuJSON(got)
		if e != nil {
			t.Fatal(e)
		}
		iyuuOracleEqual(t, v, want["value"])
	}
	for i, raw := range doc["qb_add_response"].([]any) {
		row := raw.(map[string]any)
		body, e := json.Marshal(row["input"])
		if e != nil {
			t.Fatal(e)
		}
		ok, ids, e := iyuuAddResponse(body)
		if e != nil {
			t.Fatalf("add case %d %v", i, e)
		}
		iyuuOracleEqual(t, []any{ok, ids}, row["result"].(map[string]any)["value"])
	}
	for _, raw := range doc["tr_projection"].([]any) {
		row := raw.(map[string]any)
		want := row["result"].(map[string]any)
		if want["exception"] != nil {
			continue
		}
		fields := row["input"].(map[string]any)
		if e := iyuuTRProperties(fields); e != nil {
			t.Fatal(e)
		}
		view := map[string]any{}
		for k := range want["view"].(map[string]any) {
			view[k] = fields[k]
		}
		// Python float(1) and JSON integer 1 are the same consumed numeric value.
		expected := want["view"].(map[string]any)
		if n, ok := expected["percent_done"].(json.Number); ok {
			f, _ := n.Float64()
			actual, _ := fields["percent_done"].(json.Number).Float64()
			if f != actual {
				t.Fatal("float projection")
			}
			delete(view, "percent_done")
			cp := map[string]any{}
			for k, v := range expected {
				if k != "percent_done" {
					cp[k] = v
				}
			}
			iyuuOracleEqual(t, view, cp)
		}
		actualCompleted := []any{}
		if fields["status"] == json.Number("6") {
			actualCompleted = append(actualCompleted, row["input"])
		}
		for _, v := range actualCompleted {
			m := v.(map[string]any)
			for _, k := range []string{"download_dir", "total_size", "percent_done", "status_stopped"} {
				delete(m, k)
			}
		}
		iyuuOracleEqual(t, actualCompleted, want["completed"])
	}
	for _, raw := range doc["properties"].([]any) {
		row := raw.(map[string]any)
		want := row["result"].(map[string]any)
		got, e := iyuuTRPercent(row["input"].(map[string]any)["percentDone"])
		if want["exception"] != nil {
			if e == nil {
				t.Fatal("invalid float property accepted")
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		a, _ := got.Float64()
		b, _ := want["value"].(json.Number).Float64()
		if a != b {
			t.Fatalf("float property got%v want%v", a, b)
		}
	}
	for _, raw := range doc["versions"].([]any) {
		row := raw.(map[string]any)
		want := row["result"].(map[string]any)
		got, e := iyuuVersionAtLeast(row["input"].(string), 2, 11)
		if want["exception"] != nil {
			if e == nil {
				t.Fatal("invalid version accepted", row["input"])
			}
			continue
		}
		if e != nil || got != want["value"] {
			t.Fatalf("version %v got %v/%v want%v", row["input"], got, e, want)
		}
	}
}
