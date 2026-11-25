package autosignin

import (
	"context"
	_ "embed"
	"encoding/json"
	"testing"
	"time"
)

type sourceRichCache struct{}

func (sourceRichCache) Ensure(context.Context) error         { return nil }
func (sourceRichCache) Read(context.Context) ([]byte, error) { return nil, nil }
func (sourceRichCache) Write(context.Context, []byte) error  { return nil }

type sourceRichHTTP struct{ calls int }

func (p *sourceRichHTTP) Do(context.Context, PageRequest) (*PageResponse, error) {
	p.calls++
	return nil, nil
}

//go:embed testdata/source_page_adapters_original210.json
var sourceRichAdapters []byte

func TestSourceRichPageAllAdapterConsumers(t *testing.T) {
	var oracle struct {
		Rows []struct {
			Name, Caller, Batch string
			SourceHour          int `json:"source_hour"`
			ExpectedSiteHTTP    int `json:"expected_site_http"`
			Input               struct{ Value json.RawMessage }
			Result              struct{ Value json.RawMessage }
			Error               *struct{ Class, Message string }
			Wire                []struct{ Method string }
		}
	}
	if e := json.Unmarshal(sourceRichAdapters, &oracle); e != nil {
		t.Fatal("actual original adapter210", e)
	}
	kinds := map[string]FixedAdapter{"btschool": AdapterBTSchool, "hdupt": AdapterHDUpt, "pttime": AdapterPTTime, "haidan": AdapterHaiDan, "hdcity": AdapterHDCity, "hares": AdapterHares, "zhuque": AdapterZhuQue, "hdsky": AdapterHDSky, "opencd": AdapterOpencd, "ttg": AdapterTTG, "chdbits": AdapterCHDBits, "52pt": AdapterPt52, "tjupt": AdapterTjupt, "u2": AdapterU2}
	for _, r := range oracle.Rows {
		t.Run(r.Batch+"/"+r.Caller+"/"+r.Name, func(t *testing.T) {
			value, e := parseSourceHTTPJSON(string(r.Input.Value))
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			transport := &sourceRichHTTP{}
			h := SiteVisitHooks{HTTP: transport, SourcePageValue: func(context.Context, PageRequest) (any, error) { calls++; return value, nil }, Now: func() time.Time { return time.Date(2026, 11, 7, r.SourceHour, 0, 0, 0, time.UTC) }, Tjupt: &TjuptDependencies{Cache: sourceRichCache{}}}
			site := Object{"id": 7, "name": "Owned", "url": "http://127.0.0.1:1/", "cookie": "a=owned", "ua": "owned-UA", "render": true, "timeout": 60}
			got, err := FixedSignin(kinds[r.Caller])(context.Background(), site, h)
			if r.Error != nil {
				if err == nil || err.Error() != r.Error.Message {
					t.Fatalf("original %s operation differs got=%+v err=%v expected=%+v", r.Error.Class, got, err, r.Error)
				}
			} else {
				var expected []any
				if json.Unmarshal(r.Result.Value, &expected) != nil || len(expected) != 2 {
					t.Fatalf("original tuple %s", r.Result.Value)
				}
				if err != nil || got.State != expected[0] || got.Message != expected[1] {
					t.Fatalf("original tuple differs got=%+v err=%v expected=%s", got, err, r.Result.Value)
				}
			}
			if transport.calls != r.ExpectedSiteHTTP {
				t.Fatalf("original ordinary HTTP path got=%d source=%d", transport.calls, r.ExpectedSiteHTTP)
			}
			if calls != len(r.Wire) {
				t.Fatalf("Page/FS call order/count differs got=%d sourceHTTP=%d", calls, len(r.Wire))
			}
		})
	}
	if len(oracle.Rows) != 210 {
		t.Fatal("source210", len(oracle.Rows))
	}
}
