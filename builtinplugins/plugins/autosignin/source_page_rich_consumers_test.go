package autosignin

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
)

// Expectations come from actual original generic/Pter callers, not new helpers.
//
//go:embed testdata/source_page_generic_original48.json
var sourceRichGeneric []byte

func TestSourceRichPageOriginalConsumers(t *testing.T) {
	var oracle struct {
		Rows []struct {
			Category, Name, Caller string
			Input                  struct{ Value json.RawMessage }
			Result                 struct{ Value json.RawMessage }
			Error                  *struct{ Class, Message string }
		}
	}
	if e := json.Unmarshal(sourceRichGeneric, &oracle); e != nil {
		t.Fatal("original source48", e)
	}
	count := 0
	for _, r := range oracle.Rows {
		if r.Category != "response" || (r.Caller != "core-signin" && r.Caller != "core-login" && r.Caller != "pter-selected-leaf") {
			continue
		}
		count++
		t.Run(r.Caller+"/"+r.Name, func(t *testing.T) {
			value, e := parseSourceHTTPJSON(string(r.Input.Value))
			if e != nil {
				t.Fatal(e)
			}
			hooks := SiteVisitHooks{SourcePageValue: func(context.Context, PageRequest) (any, error) { return value, nil }}
			site := Object{"id": 7, "name": "Owned", "url": "http://127.0.0.1:1/", "cookie": "a=owned", "ua": "owned-UA", "render": true, "timeout": 60}
			if r.Caller == "core-signin" {
				got := GenericSignin(context.Background(), site, hooks)
				var expected struct {
					Message  string
					Success  bool
					LoggedIn bool `json:"logged_in"`
				}
				if json.Unmarshal(r.Result.Value, &expected) != nil {
					t.Fatal("sourceResult")
				}
				if got.Message != expected.Message || got.Success != expected.Success || got.LoggedIn != expected.LoggedIn {
					t.Fatalf("original GenericSignin differs got=%+v expected=%s", got, r.Result.Value)
				}
				return
			}
			var got AdapterResult
			var err error
			if r.Caller == "core-login" {
				got, err = GenericLogin(context.Background(), site, hooks)
			} else {
				got, err = JSONSignin(AdapterPTerClub, context.Background(), site, hooks)
			}
			if r.Error != nil {
				if err == nil || err.Error() != r.Error.Message {
					t.Fatalf("original operation error differs got=%v expected=%+v", err, r.Error)
				}
				return
			}
			var expected []any
			d := json.NewDecoder(strings.NewReader(string(r.Result.Value)))
			d.UseNumber()
			if d.Decode(&expected) != nil || len(expected) != 2 {
				t.Fatal("source tuple")
			}
			if err != nil || got.State != expected[0] || got.Message != expected[1] {
				t.Fatalf("original caller differs got=%+v err=%v expected=%s", got, err, r.Result.Value)
			}
		})
	}
	if count != 48 {
		t.Fatal("source48consumer cells", count)
	}
}
