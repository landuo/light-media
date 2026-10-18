package iyuuautoseed

import (
	"context"
	"encoding/json"
	"testing"
)

type rescueFailureAPI struct {
	code int
	msg  string
}

func (a rescueFailureAPI) Call(_ context.Context, r APIRequest) (*APIResponse, error) {
	var data any
	switch r.Operation {
	case SitesIndex:
		data = map[string]any{"sites": []any{map[string]any{"id": 1, "base_url": "https://source.example/", "download_page": "download.php?id={}"}}}
	case ReportExisting:
		data = map[string]any{"sid_sha1": "owned-sites"}
	case ReseedIndex:
		raw, _ := json.Marshal(map[string]any{"code": a.code, "msg": a.msg})
		return &APIResponse{Status: 200, Body: raw}, nil
	}
	raw, _ := json.Marshal(map[string]any{"code": 0, "data": data})
	return &APIResponse{Status: 200, Body: raw}, nil
}
func TestSeedRescueNoDataPredicateUsesActualHelperFailures(t *testing.T) {
	if SourceNoReseedData(nil) {
		t.Fatal("nil grants no-data")
	}
	for _, c := range []struct {
		name    string
		code    int
		message string
		want    bool
	}{{"404 only", 404, "other", false}, {"message only", 503, "未查询到可辅种数据", false}, {"both", 404, "未查询到可辅种数据", true}, {"other", 500, "temporary", false}} {
		t.Run(c.name, func(t *testing.T) {
			h, e := NewHelper(rescueFailureAPI{c.code, c.message}, nil)
			if e != nil {
				t.Fatal(e)
			}
			r, e := h.SeedInfo(context.Background(), []string{"owned"})
			if e != nil || r.Failure == nil {
				t.Fatal(r, e)
			}
			if SourceNoReseedData(r.Failure) != c.want {
				t.Fatal(r.Failure)
			}
		})
	}
}
