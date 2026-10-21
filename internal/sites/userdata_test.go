package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
)

func TestSourceUserStatsInstalledOutcomeOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/site-account-outcomes-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Kind, Body string
			Data             map[string]any
			Success          bool
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, item := range fixture.Cases {
		if item.Kind == "throws" || item.Kind == "unsupported" {
			continue
		} // Module dispatch is tested in the source oracle, not a native parser.
		t.Run(item.Name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, item.Body) }))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			kind := item.Kind
			if kind == "nexus" {
				kind = "jiayuan"
			}
			c := &Client{config: Config{ID: "owned", Kind: kind, Cookie: "synthetic-cookie", APIKey: "synthetic-key"}, base: base, http: server.Client()}
			s, d, e := c.SourceUserStats(context.Background(), UserStatsOptions{})
			if (d != nil) != item.Success {
				t.Fatal("DTO/no DTO differs from installed chain", e)
			}
			if d == nil {
				if e == nil {
					t.Fatal("parser exception lost native failure")
				}
				return
			}
			d.Domain, d.Day = "owned.example", "<day>"
			encoded, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			json.Unmarshal(encoded, &actual)
			if !reflect.DeepEqual(actual, item.Data) {
				t.Fatalf("source DTO differs\nactual %s\nexpected %v", encoded, item.Data)
			}
			if s.Uploaded != nil || s.LeechingBytes != nil {
				t.Fatal("constructor defaults polluted unobserved native values")
			}
		})
	}
}

func TestSourceUserStatsStatusBodiesAndAdmissionBoundary(t *testing.T) {
	for _, status := range []int{403, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `<a href="logout.php">out</a><div>上传：1 GB 下载：3 GB</div>`)
			}))
			defer server.Close()
			c, err := New(Config{ID: "owned", Kind: "jiayuan", BaseURL: server.URL, Cookie: "synthetic-cookie"})
			if err != nil {
				t.Fatal(err)
			}
			_, d, e := c.SourceUserStats(context.Background(), UserStatsOptions{})
			if d == nil || e == nil {
				t.Fatal("native failure or DTO lost", e)
			}
			if status == 503 {
				if d.Error == nil || *d.Error != "未检测到已登陆，请检查cookies是否过期" {
					t.Fatal("failed page not a source error DTO")
				}
			} else if d.Error != nil || d.Upload != 1073741824 || d.Ratio != 0.333 {
				t.Fatal("allowed body not parsed as source DTO", d)
			}
			if _, e = c.UserStats(context.Background()); e == nil {
				t.Fatal("source account status exception escaped into native protocol")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("denied request reached network") }))
	defer server.Close()
	c, _ := New(Config{ID: "owned", Kind: "jiayuan", BaseURL: server.URL, Cookie: "synthetic-cookie"})
	denied := errors.New("owned_authority_revoked")
	c.SetRequestGuard(func(context.Context) error { return denied })
	_, d, e := c.SourceUserStats(context.Background(), UserStatsOptions{})
	if d != nil || !errors.Is(e, denied) {
		t.Fatal("denial manufactured a DTO", e)
	}
}

func TestSourceUserStatsMTeamCountAndRole(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/member/profile":
			fmt.Fprint(w, `{"code":"0","data":{"id":7,"username":"owned","role":"18"}}`)
		case "/api/tracker/myPeerStatus":
			fmt.Fprint(w, `{"code":"0","data":{"seeder":0,"leecher":0}}`)
		case "/api/member/getUserTorrentList":
			fmt.Fprint(w, `{"code":"0","data":{"total":1,"data":[{"torrent":{"id":1,"size":123,"source":2}}]}}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	c := &Client{config: Config{ID: "owned", Kind: "mteam", APIKey: "synthetic-key"}, base: base, http: server.Client()}
	_, d, e := c.SourceUserStats(context.Background(), UserStatsOptions{})
	if e != nil || d == nil || d.UserID != int64(7) || d.UserLevel == nil || *d.UserLevel != "Bet memberStaff" || d.Seeding != 1 || d.SeedingSize != 123 {
		t.Fatal("source role/count/id lost", e, d)
	}
}
