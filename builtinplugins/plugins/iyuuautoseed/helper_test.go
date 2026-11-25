package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type helperOracle struct {
	Source string       `json:"source_sha256"`
	Cases  []helperCase `json:"cases"`
}
type helperCase struct {
	Name      string `json:"name"`
	Responses []any  `json:"responses"`
	Actions   []struct {
		Op   string `json:"op"`
		Args any    `json:"args"`
	} `json:"actions"`
	Requests  []map[string]any `json:"requests"`
	Results   []map[string]any `json:"results"`
	Remaining int              `json:"remaining"`
}
type oracleAPI struct {
	t         *testing.T
	responses []any
	calls     []map[string]any
}

func (a *oracleAPI) Call(_ context.Context, r APIRequest) (*APIResponse, error) {
	a.t.Helper()
	paths := map[Operation]string{SitesIndex: "/reseed/sites/index", ReportExisting: "/reseed/sites/reportExisting", ReseedIndex: "/reseed/index/index"}
	method := "post"
	if r.Operation == SitesIndex {
		method = "get"
	}
	var params any
	if r.Params != nil {
		params = r.Params
	}
	cp, e := cloneJSON(map[string]any{"method": method, "path": paths[r.Operation], "params": params})
	if e != nil {
		a.t.Fatal(e)
	}
	a.calls = append(a.calls, cp.(map[string]any))
	if len(a.responses) == 0 {
		a.t.Fatal("unexpected API call")
	}
	raw := a.responses[0]
	a.responses = a.responses[1:]
	if raw == nil {
		return nil, nil
	}
	m := raw.(map[string]any)
	status := 200
	if x, ok := m["status"].(json.Number); ok {
		n, _ := x.Int64()
		status = int(n)
	}
	reason := "OK"
	if x, ok := m["reason"].(string); ok {
		reason = x
	}
	return &APIResponse{Status: status, Reason: reason, Body: []byte(m["body"].(string))}, nil
}
func TestInstalledHelperOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/helper-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	var oracle helperOracle
	if e = d.Decode(&oracle); e != nil {
		t.Fatal(e)
	}
	if oracle.Source != "74f0cbde2789eab6a6c0d0edcc6fe3364a5e4ccceb909b5c8842492dc11cdcf0" {
		t.Fatal("wrong source")
	}
	if len(oracle.Cases) != 24 {
		t.Fatalf("case count %d", len(oracle.Cases))
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			api := &oracleAPI{t: t, responses: c.Responses}
			h, e := NewHelper(api, func() time.Time { return time.Unix(1720000000, 875000000) })
			if e != nil {
				t.Fatal(e)
			}
			for i, a := range c.Actions {
				want := c.Results[i]
				got := map[string]any{}
				var err error
				switch a.Op {
				case "url":
					var first, second any
					first, second, err = h.TorrentURL(context.Background(), a.Args)
					if err == nil {
						got["value"] = []any{first, second}
					}
				case "seed":
					hashes := make([]string, 0)
					for _, v := range a.Args.([]any) {
						hashes = append(hashes, v.(string))
					}
					var r APIResult
					r, err = h.SeedInfo(context.Background(), hashes)
					if err == nil {
						msg := ""
						if r.Failure != nil {
							msg = r.Failure.sourceMessage
						}
						got["value"] = []any{r.Data, msg}
						got["mutated"] = hashes
					}
				default:
					t.Fatal("bad oracle action")
				}
				if err != nil {
					var f *SourceFault
					if !errors.As(err, &f) {
						t.Fatalf("unclassified error %v", err)
					}
					got = map[string]any{"exception": f.Class}
				}
				clean, e := cloneJSON(got)
				if e != nil {
					t.Fatal(e)
				}
				if !reflect.DeepEqual(clean, want) {
					gb, _ := json.Marshal(clean)
					wb, _ := json.Marshal(want)
					t.Fatalf("action %d\n got %s\nwant %s", i, gb, wb)
				}
			}
			for _, r := range c.Requests {
				delete(r, "headers")
			}
			if !reflect.DeepEqual(api.calls, c.Requests) && !(len(api.calls) == 0 && len(c.Requests) == 0) {
				t.Fatalf("calls\ngot %#v\nwant %#v", api.calls, c.Requests)
			}
			if len(api.responses) != c.Remaining {
				t.Fatal("remaining response count")
			}
		})
	}
}

func TestHelperInputIsolation(t *testing.T) {
	api := &oracleAPI{t: t, responses: []any{map[string]any{"body": `{"code":0,"data":{"sites":[{"id":1,"base_url":{"nested":[9]},"download_page":"x"}]}}`}}}
	h, _ := NewHelper(api, nil)
	a, _, e := h.TorrentURL(context.Background(), json.Number("1"))
	if e != nil {
		t.Fatal(e)
	}
	a.(map[string]any)["nested"].([]any)[0] = "changed"
	b, _, e := h.TorrentURL(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	if b.(map[string]any)["nested"].([]any)[0] != json.Number("9") {
		t.Fatal("cache alias leaked")
	}
}
