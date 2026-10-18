package seedrescue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type detailTransportFunc func(context.Context, DetailRequest) (*DetailResponse, error)

func (f detailTransportFunc) DoDetail(c context.Context, r DetailRequest) (*DetailResponse, error) {
	return f(c, r)
}
func TestDetailTransportMTeamRuntimeOnlyTokenAndPolicy(t *testing.T) {
	ctx := context.Background()
	site := Object{"url": "https://kp.m-team.cc/", "apikey": "fixture-api-key", "ua": "FixtureUA", "proxy": true, "timeout": 30}
	calls := 0
	r := FetchDetail(ctx, site, "42", "fallback", detailTransportFunc(func(_ context.Context, q DetailRequest) (*DetailResponse, error) {
		calls++
		if q.Method != "POST" || q.URL != "https://api.m-team.cc/api/torrent/detail" || q.Form["id"] != "42" || q.Headers["x-api-key"] != "fixture-api-key" || q.Timeout != 20 || !q.Proxy || q.AllowRedirects || q.Cookie != nil {
			t.Fatalf("request %+v", q)
		}
		return &DetailResponse{Status: 200, JSON: Object{"code": 0, "data": Object{"id": 42, "name": "Fixture", "status": Object{"discount": "FREE", "seeders": 2}}}}, nil
	}))
	if r.Status != "ready" || calls != 1 {
		t.Fatal(r, calls)
	}
	enclosure := pyString(r.Torrent["enclosure"])
	end := strings.Index(enclosure, "]")
	b, e := base64.StdEncoding.DecodeString(enclosure[1:end])
	if e != nil {
		t.Fatal(e)
	}
	var params Object
	if e = json.Unmarshal(b, &params); e != nil {
		t.Fatal(e)
	}
	if params["result"] != "data" || params["method"] != "post" || params["cookie"] != false || params["proxy"] != true || params["params"].(map[string]any)["id"] != "42" || !strings.HasSuffix(enclosure, "https://api.m-team.cc/api/torrent/genDlToken") {
		t.Fatal(params, enclosure)
	}
	candidate := Candidate{Match: Match{2, "42", strings.Repeat("a", 40)}, SiteID: 2, SiteName: "MTeam", Torrent: r.Torrent, MetadataStatus: "ready"}
	record := CandidateRecord(candidate)
	serialized, _ := json.Marshal(record)
	if strings.Contains(string(serialized), "fixture-api-key") || strings.Contains(string(serialized), "genDlToken") {
		t.Fatal("runtime secret leaked", string(serialized))
	}
}
func TestDetailAuthenticationFailureVsFallbackAndPartial(t *testing.T) {
	ctx := context.Background()
	site := Object{"url": "https://source.test/", "schema": "nexus", "cookie": "fixture-cookie"}
	for _, tc := range []struct {
		name     string
		response *DetailResponse
		err      error
		status   string
	}{
		{"network", nil, errors.New("URL?passkey=fixture-secret"), "unavailable"},
		{"rate", &DetailResponse{Status: 429}, nil, "unavailable"},
		{"404", &DetailResponse{Status: 404}, nil, "unsupported"},
		{"login", &DetailResponse{Status: 200, Text: `<input type="password">`}, nil, "unavailable"},
		{"challenge", &DetailResponse{Status: 200, Text: `cf-chl-challenge`}, nil, "unavailable"},
		{"structure", &DetailResponse{Status: 200, Text: `<p>other page</p>`}, nil, "unsupported"},
		{"partial", &DetailResponse{Status: 200, Text: `<h1>Fixture <span>[FREE]</span></h1><a href="download.php?id=42">D</a>`}, nil, "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r := FetchDetail(ctx, site, "42", "fallback", detailTransportFunc(func(_ context.Context, q DetailRequest) (*DetailResponse, error) {
				calls++
				if q.Cookie != "fixture-cookie" || q.Method != "GET" || q.AllowRedirects || q.Proxy {
					t.Fatal(q)
				}
				return tc.response, tc.err
			}))
			if r.Status != tc.status || calls != 1 {
				t.Fatal(r, calls)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "fixture-secret") {
				t.Fatal("raw error leaked", string(b))
			}
		})
	}
	calls := 0
	site = Object{"url": "https://kp.m-team.io/"}
	r := FetchDetail(ctx, site, "42", "", detailTransportFunc(func(context.Context, DetailRequest) (*DetailResponse, error) { calls++; return nil, nil }))
	if calls != 0 || r.Status != "unavailable" || *r.Error != "站点未配置 API Key" {
		t.Fatal(r, calls)
	}
}
func TestNexusOwnHeadingAndDownloadIdentityOnly(t *testing.T) {
	page := `<h1 id="top">Film <span class="twoupfree">审核通过</span></h1><div><h2>[FREE]</h2></div><a href="https://evil.test/download.php?id=42">evil</a><a href="download.php?id=43">other</a><a href="download.php?id=42">own</a><div id="peercount">0 / 3</div>`
	r, e := ParseNexus(page, "https://source.test/details.php?id=42", "42")
	if e != nil || r["downloadvolumefactor"] != float64(1) || r["enclosure"] != "https://source.test/download.php?id=42" || *intPtr(r["seeders"]) != 0 {
		t.Fatal(r, e)
	}
}
