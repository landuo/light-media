package autosignin

import (
	"context"
	_ "embed"
	"encoding/json"
	"testing"
)

//go:embed testdata/source_page_surrogate_original9.json
var sourceSurrogateOriginal9 []byte

func TestIndependentRichExistingSurrogateConsumers(t *testing.T) {
	raw := sourceSurrogateOriginal9
	var e error
	var oracle struct {
		Rows []struct {
			Name, Caller, InputJSON string `json:"-"`
			Result                  struct{ Value json.RawMessage }
			Error                   *struct{ Class, Message string }
		}
	}
	// Explicit field names retain the Python-produced quoted source string, never
	// decoding it through encoding/json before the existing source parser.
	var rows struct {
		Rows []struct {
			Name      string `json:"name"`
			Caller    string `json:"caller"`
			InputJSON string `json:"input_json"`
			Result    struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
			Error *struct{ Class, Message string } `json:"error"`
		}
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e)
	}
	_ = oracle
	for _, r := range rows.Rows {
		t.Run(r.Name+"/"+r.Caller, func(t *testing.T) {
			value, e := parseSourceHTTPJSON(r.InputJSON)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			h := SiteVisitHooks{SourcePageValue: func(context.Context, PageRequest) (any, error) { calls++; return value, nil }}
			site := Object{"id": 7, "name": "OwnedUnicode", "url": "http://127.0.0.1:1/", "cookie": "a=owned", "ua": "owned-UA", "render": true, "timeout": 60}
			if r.Caller == "core-signin" {
				got := GenericSignin(context.Background(), site, h)
				var want struct {
					Message  string `json:"message"`
					Success  bool   `json:"success"`
					LoggedIn bool   `json:"logged_in"`
				}
				if e = json.Unmarshal(r.Result.Value, &want); e != nil {
					t.Fatal(e)
				}
				if got.Message != want.Message || got.Success != want.Success || got.LoggedIn != want.LoggedIn {
					t.Fatalf("actual original consumer differs target=%+v source=%s", got, r.Result.Value)
				}
			} else {
				var got AdapterResult
				var err error
				if r.Caller == "core-login" {
					got, err = GenericLogin(context.Background(), site, h)
				} else {
					got, err = FixedSignin(AdapterPTTime)(context.Background(), site, h)
				}
				if r.Error != nil {
					if err == nil || err.Error() != r.Error.Message {
						t.Fatalf("actual original exception target=%v source=%+v", err, r.Error)
					}
				} else {
					var want []any
					if e = json.Unmarshal(r.Result.Value, &want); e != nil {
						t.Fatal(e)
					}
					if err != nil || got.State != want[0] || got.Message != want[1] {
						t.Fatalf("actual original consumer differs target=%+v error=%v source=%s", got, err, r.Result.Value)
					}
				}
			}
			if calls != 1 {
				t.Fatal("source one physical FS fetch", calls)
			}
		})
	}
	if len(rows.Rows) != 9 {
		t.Fatal(len(rows.Rows))
	}
}
