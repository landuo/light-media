package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUntrustedItemScopeStopsDependentReads(t *testing.T) {
	cases := []struct {
		name, key string
		value     any
	}{{"foreign_ancestor", "ancestor_guid", "production-library"}, {"missing_ancestor", "ancestor_guid", nil}, {"foreign_parent", "parent_guid", "production-parent"}, {"missing_parent", "parent_guid", nil}, {"wrong_type", "type", "Person"}, {"missing_type", "type", nil}}
	for _, tc := range cases {
		for _, action := range []string{"observe", "images"} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var d any
					switch r.URL.Path {
					case "/v/api/v1/item/list":
						d = map[string]any{"list": []any{map[string]string{"guid": "item", "type": "Movie"}}}
					case "/v/api/v1/item/item":
						m := map[string]any{"guid": "item", "type": "Movie", "ancestor_guid": "owned", "parent_guid": "", "posters": "/opaque.jpg"}
						if tc.value == nil {
							delete(m, tc.key)
						} else {
							m[tc.key] = tc.value
						}
						d = m
					default:
						t.Errorf("scope failure followed to dependent endpoint %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": d})
				}))
				defer srv.Close()
				a := api{host: srv.URL + "/v", client: srv.Client()}
				r := receipt{OwnedGUID: "owned"}
				var result []map[string]any
				var e error
				if action == "observe" {
					result, e = a.observe(context.Background(), r)
				} else {
					result, e = a.images(context.Background(), r, false, "")
				}
				if e == nil || calls != 2 {
					t.Fatalf("scope did not fail closed: %v calls=%d", e, calls)
				}
				if len(result) != 1 || result[0]["scope_verified"] != false {
					t.Fatal("missing safe diagnostic")
				}
			})
		}
	}
}
func TestChildParentAndAncestorAreRealFields(t *testing.T) {
	r := receipt{OwnedGUID: "owned"}
	n := observedNode{guid: "episode", parent: "season-a", kind: "Episode", depth: 2}
	base := map[string]string{"guid": "episode", "type": "Episode", "parent_guid": "season-a", "ancestor_guid": "owned"}
	check := func(m map[string]string) error {
		b, _ := json.Marshal(m)
		var d map[string]json.RawMessage
		json.Unmarshal(b, &d)
		return verifyItemScope(d, r, n)
	}
	if e := check(base); e != nil {
		t.Fatal(e)
	}
	base["parent_guid"] = "season-b"
	if check(base) == nil {
		t.Fatal("wrong same-library parent accepted")
	}
	base["parent_guid"] = "season-a"
	base["ancestor_guid"] = "foreign"
	if check(base) == nil {
		t.Fatal("foreign descendant accepted")
	}
}

func TestScopeDiagnosticExactFieldShape(t *testing.T) {
	cases := []struct{ raw, state string }{{"", "absent"}, {"null", "null"}, {`""`, "empty_string"}, {`123`, "nonstring"}, {`{"secret":"never-output"}`, "nonstring"}, {`"https://secret.invalid/token"`, "invalid_string"}, {`"accepted-guid"`, "valid_guid"}}
	for _, key := range []string{"guid", "ancestor_guid", "parent_guid"} {
		for _, tc := range cases {
			d := map[string]json.RawMessage{}
			if tc.raw != "" {
				d[key] = json.RawMessage(tc.raw)
			}
			out := scopeDiagnostic(d, observedNode{guid: "requested", parent: "expected"})
			if out[key+"_state"] != tc.state {
				t.Fatalf("%s shape %s: %v", key, tc.state, out)
			}
			if tc.state != "valid_guid" {
				if _, ok := out[key]; ok {
					t.Fatalf("unknown value exposed: %s", key)
				}
			} else if out[key] != "accepted-guid" {
				t.Fatal("valid GUID lost")
			}
		}
	}

}

func TestLiveRootEmptyParentContract(t *testing.T) {
	for _, kind := range []string{"Movie", "TV", "Video"} {
		for _, tc := range []struct {
			name, raw string
			pass      bool
		}{{"explicit_empty", `""`, true}, {"absent", "", false}, {"null", `null`, false}, {"nonstring", `12`, false}, {"own_library_guid", `"owned"`, false}, {"other_guid", `"other"`, false}} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				b, _ := json.Marshal(kind)
				d := map[string]json.RawMessage{"guid": json.RawMessage(`"item"`), "type": b, "ancestor_guid": json.RawMessage(`"owned"`)}
				if tc.raw != "" {
					d["parent_guid"] = json.RawMessage(tc.raw)
				}
				e := verifyItemScope(d, receipt{OwnedGUID: "owned"}, observedNode{guid: "item", parent: "owned", kind: kind})
				if (e == nil) != tc.pass {
					t.Fatalf("unexpected validation %v", e)
				}
				if tc.pass {
					d["ancestor_guid"] = json.RawMessage(`"foreign"`)
					if verifyItemScope(d, receipt{OwnedGUID: "owned"}, observedNode{guid: "item", parent: "owned", kind: kind}) == nil {
						t.Fatal("empty root parent bypassed ancestor")
					}
				}
			})
		}
	}
	for depth, kind := range map[int]string{1: "Season", 2: "Episode"} {
		for _, raw := range []string{`""`, `null`, ``} {
			b, _ := json.Marshal(kind)
			d := map[string]json.RawMessage{"guid": json.RawMessage(`"item"`), "type": b, "ancestor_guid": json.RawMessage(`"owned"`)}
			if raw != "" {
				d["parent_guid"] = json.RawMessage(raw)
			}
			if verifyItemScope(d, receipt{OwnedGUID: "owned"}, observedNode{guid: "item", parent: "real-parent", kind: kind, depth: depth}) == nil {
				t.Fatal("nested empty parent accepted")
			}
		}
	}
}
