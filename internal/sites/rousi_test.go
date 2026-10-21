package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRousiSearchAndMaterialSeparateCredentials(t *testing.T) {
	c, e := New(Config{ID: "rousi-owned", Kind: "rousi", BaseURL: "https://rousi.pro", APIKey: "synthetic-profile-key", MessageToken: "synthetic-message-token", Cookie: "must-not-send"})
	if e != nil {
		t.Fatal(e)
	}
	calls := []string{}
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		if r.Header.Get("Cookie") != "" || strings.Contains(r.Header.Get("Authorization"), "message-token") {
			t.Error("mailbox/cookie used in torrent flow")
		}
		switch r.URL.Path {
		case "/api/v1/torrents":
			if r.Header.Get("Authorization") != "Bearer synthetic-profile-key" || r.URL.Query().Get("keyword") != "合成 & +" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "100" {
				t.Error("search request changed")
			}
			return response([]byte(`{"code":0,"data":{"torrents":[{"id":7,"title":"Owned.2160p.WEB-DL.H265","subtitle":"sub","uuid":"a-b","size":1024,"seeders":3,"leechers":1,"created_at":"2026-10-21T03:04:05Z","promotion":{"is_active":true,"down_multiplier":0,"up_multiplier":2,"until":"2026-10-22T03:04:05Z"}}]}}`), 200), nil
		case "/api/v1/torrents/7":
			if r.Header.Get("Authorization") != "Bearer synthetic-profile-key" {
				t.Error("detail credential missing")
			}
			return response([]byte(`{"code":0,"data":{"download_url":"https://rousi.pro/signed/material?passkey=synthetic-material-secret"}}`), 200), nil
		case "/signed/material":
			if r.Header.Get("Authorization") != "" || r.URL.Query().Get("passkey") != "synthetic-material-secret" {
				t.Error("signed material received API credentials")
			}
			return response(validTorrent(), 200), nil
		}
		t.Error("unexpected owned request")
		return nil, errors.New("owned unexpected")
	})
	result, e := c.Search(context.Background(), "合成 & +", 2)
	if e != nil || len(result.Resources) != 1 {
		t.Fatal(e)
	}
	item := result.Resources[0]
	if result.Page != 2 || result.HasMore || !item.Free || item.UploadFactor != 2 || item.Resolution != "2160p" || item.Codec != "HEVC" || item.Source != "WEB-DL" || item.PublishedAt != "2026-10-21T03:04:05Z" || item.DetailsURL != "https://rousi.pro/torrent/a-b" {
		t.Fatal("search result lost original fields", item)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "synthetic-") {
		t.Fatal("public result leaked credentials")
	}
	torrent, e := c.Torrent(context.Background(), "7")
	if e != nil || !bytes.Equal(torrent, validTorrent()) || len(calls) != 3 {
		t.Fatal("torrent flow failed", e, calls)
	}
}

func TestRousiMaterialAndBusinessFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"foreign-material", "redirect", "invalid-metainfo", "business", "invalid-json"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := New(Config{ID: "owned", Kind: "rousi", BaseURL: "https://rousi.pro", APIKey: "synthetic-profile"})
			calls := 0
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "rousi.pro" {
					t.Fatal("foreign request")
				}
				if strings.HasPrefix(r.URL.Path, "/api/") {
					switch mode {
					case "foreign-material":
						return response([]byte(`{"code":0,"data":{"download_url":"https://foreign.invalid/leak?passkey=secret"}}`), 200), nil
					case "business":
						return response([]byte(`{"code":4,"message":"synthetic-secret"}`), 200), nil
					case "invalid-json":
						return response([]byte(`{`), 200), nil
					}
					return response([]byte(`{"code":0,"data":{"download_url":"/signed/material"}}`), 200), nil
				}
				if mode == "redirect" {
					res := response(nil, 302)
					res.Header.Set("Location", "https://foreign.invalid/")
					return res, nil
				}
				return response([]byte("<input type=password>"), 200), nil
			})
			var e error
			if mode == "business" || mode == "invalid-json" {
				_, e = c.Search(context.Background(), "owned", 1)
			} else {
				_, e = c.Torrent(context.Background(), "7")
			}
			if e == nil || strings.Contains(e.Error(), "secret") || calls > 2 {
				t.Fatal("business/material failure hidden", e, calls)
			}
		})
	}
}

func TestSourceRousiCancellationAndRevocationBeforeReadAll(t *testing.T) {
	for _, mode := range []string{"cancel-before-list", "revoke-before-mark", "page-limit", "native-unknown"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := New(Config{ID: "owned", Kind: "rousi", BaseURL: "https://rousi.pro", APIKey: "synthetic-profile", MessageToken: "synthetic-token"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			revoked := false
			c.SetRequestGuard(func(context.Context) error {
				if revoked {
					return errors.New("owned_revoked")
				}
				return nil
			})
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path == "/api/messages/read-all" {
					t.Fatal("unexpected mark read")
				}
				body := `{"code":0,"data":{"id":7,"username":"owned"}}`
				if r.URL.Path == "/api/v1/profile" && mode == "cancel-before-list" {
					cancel()
				}
				if r.URL.Path == "/api/messages" {
					body = `{"code":0,"data":{"messages":[],"total_pages":999}}`
					if mode == "revoke-before-mark" {
						body = `{"code":0,"data":{"messages":[],"total_pages":1}}`
						revoked = true
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			stats, d, e := c.SourceUserStats(ctx, UserStatsOptions{ReadMessages: mode != "native-unknown"})
			if mode == "native-unknown" {
				if e != nil || d == nil || stats.UserID != "7" || d.Upload != 0 || stats.Uploaded != nil || stats.Seeding != nil {
					t.Fatal("source defaults confused with observed zero", e, stats)
				}
				return
			}
			want := map[string]string{"cancel-before-list": "context canceled", "revoke-before-mark": "owned_revoked", "page-limit": "site_account_pagination_limit"}[mode]
			if e == nil || e.Error() != want || d != nil {
				t.Fatal("control failure published DTO", e)
			}
			expected := map[string]int{"cancel-before-list": 1, "revoke-before-mark": 2, "page-limit": 51}[mode]
			if calls != expected {
				t.Fatal("control boundary IO mismatch", calls, expected)
			}
		})
	}
}
