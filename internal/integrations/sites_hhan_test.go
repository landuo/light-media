package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Synthetic fixtures based only on the authorized tag/class shape summary.
// They contain no captured private page, torrent title, ID or credential.
const syntheticHhanAuth = `<a href="logout.php">Exit</a><a href="userdetails.php?id=1">Synthetic user</a>`
const syntheticHhanSearchInput = `<input name='keyword' class='w-full' id='searchinput'>`
const syntheticHhanGrid = `<div class='torrents-table flex flex-col items-center'><div class='torrent-title'>Synthetic header</div><div class='flex flex-col torrent-table-for-spider'><div class='w-full torrent-table-sub-info'><div class='flex torrent-table-for-spider-info torrent-title'><a href='details.php?id=101'>Synthetic result</a><a href='details.php?id=101'>Duplicate UI link</a></div></div></div></div>`

func TestHhanSyntheticKeywordAndDivGrid(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.Header.Get("Cookie") != "synthetic-cookie" {
			t.Error("unexpected request")
		}
		if r.URL.Path == "/index.php" {
			fmt.Fprint(w, syntheticHhanAuth)
			return
		}
		if r.URL.Path != "/torrents.php" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("keyword") != "合成 query & +" || q.Get("page") != "0" || q.Has("search") || q.Has("search_area") || q.Has("search_mode") {
			t.Errorf("unexpected query %v", q)
		}
		fmt.Fprint(w, syntheticHhanAuth+syntheticHhanSearchInput+syntheticHhanGrid)
	}))
	defer s.Close()
	r := ProbeSite(context.Background(), Site{ID: "synthetic-hhan", Kind: "hhanclub", BaseURL: s.URL, Cookie: "synthetic-cookie", Query: "合成 query & +"})
	if r.Status != "ok" || r.SampleCount != 1 || r.Protocol != "hhanclub_html_candidate" || requests != 2 {
		t.Fatalf("%+v requests=%d", r, requests)
	}
}

func TestHhanSyntheticTemplateChangesAndEmpty(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"explicit-empty", syntheticHhanSearchInput + `<div class='torrents-table'>No torrents found</div>`, "empty_result"},
		{"grid-alone-not-empty", syntheticHhanSearchInput + `<div class='torrents-table'></div>`, "protocol_changed"},
		{"missing-input", syntheticHhanGrid, "protocol_changed"},
		{"wrong-input-name", strings.Replace(syntheticHhanSearchInput, "keyword", "search", 1) + syntheticHhanGrid, "protocol_changed"},
		{"old-table-not-grid", syntheticHhanSearchInput + `<table class='torrents'><a href='details.php?id=101'>Synthetic</a></table>`, "protocol_changed"},
		{"class-prefix-not-token", syntheticHhanSearchInput + strings.Replace(syntheticHhanGrid, "torrents-table flex", "torrents-table-new flex", 1), "protocol_changed"},
		{"missing-row-template", syntheticHhanSearchInput + strings.Replace(syntheticHhanGrid, "torrent-table-sub-info", "changed-row", 1), "protocol_changed"},
		{"missing-info-template", syntheticHhanSearchInput + strings.Replace(syntheticHhanGrid, "torrent-table-for-spider-info", "changed-info", 1), "protocol_changed"},
		{"missing-detail-link", syntheticHhanSearchInput + strings.ReplaceAll(syntheticHhanGrid, "details.php?id=101", "changed.php"), "protocol_changed"},
		{"outside-detail-not-result", syntheticHhanSearchInput + `<div class='torrents-table'></div><a href='details.php?id=101'>Outside link</a>`, "protocol_changed"},
		{"outside-empty-marker", syntheticHhanSearchInput + `<div class='torrents-table'></div><div>No torrents found</div>`, "protocol_changed"},
		{"malformed-row-even-with-empty", syntheticHhanSearchInput + `<div class='torrents-table'><div class='torrent-table-sub-info'>No torrents found</div></div>`, "protocol_changed"},
		{"unclosed-grid", syntheticHhanSearchInput + `<div class='torrents-table'>No torrents found`, "protocol_changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/index.php" {
					fmt.Fprint(w, syntheticHhanAuth)
				} else {
					fmt.Fprint(w, syntheticHhanAuth+tc.body)
				}
			}))
			defer s.Close()
			r := ProbeSite(context.Background(), Site{ID: "synthetic-hhan", Kind: "hhanclub", BaseURL: s.URL, Cookie: "synthetic", Query: "synthetic"})
			if r.Status != tc.want || r.SampleCount != 0 {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestHhanDivGridDoesNotBroadenOtherHTMLKinds(t *testing.T) {
	for _, kind := range []string{"ubits", "jiayuan", "1ptba"} {
		t.Run(kind, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, syntheticHhanAuth+syntheticHhanSearchInput+syntheticHhanGrid)
			}))
			defer s.Close()
			r := ProbeSite(context.Background(), Site{ID: "synthetic-site", Kind: kind, BaseURL: s.URL, Cookie: "synthetic", Query: "synthetic"})
			if r.Status != "protocol_changed" {
				t.Fatalf("%+v", r)
			}
		})
	}
}
