package sites

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestRousiProfileSearchAndMaterial(t *testing.T) {
	c, err := New(Config{ID: "owned", Kind: "rousi", BaseURL: "https://rousi.pro", APIKey: "synthetic-passkey", Cookie: "must-not-send"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "rousi.pro" || r.Header.Get("Cookie") != "" {
			t.Fatal("credential boundary")
		}
		if r.URL.Path != "/signed" && r.Header.Get("Authorization") != "Bearer synthetic-passkey" {
			t.Fatal("missing passkey")
		}
		switch r.URL.Path {
		case "/api/v1/profile":
			if r.URL.Query().Get("include_fields[user]") != "seeding_leeching_data" {
				t.Fatal("missing seeding data request")
			}
			return response([]byte(`{"code":0,"data":{"id":7,"username":"owned","role_text":"VIP","registered_at":"2024-01-02T00:00:00Z","uploaded":1073741824,"downloaded":0,"ratio":3.456,"karma":123.5,"seeding_leeching_data":{"seeding_count":4,"seeding_size":2147483648,"leeching_count":0}}}`), 200), nil
		case "/api/v1/torrents":
			if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("keyword") != "Example" || r.URL.Query().Get("page_size") != "100" {
				t.Fatal("search parameters")
			}
			return response([]byte(`{"code":0,"data":{"torrents":[{"id":9,"uuid":"owned-uuid","title":"Example.2160p.WEB-DL.H265","subtitle":"example","created_at":"2026-10-21T03:04:05Z","size":1073741824,"seeders":12,"leechers":0,"promotion":{"is_active":true,"down_multiplier":0,"up_multiplier":2}}]}}`), 200), nil
		case "/api/v1/torrents/9":
			return response([]byte(`{"code":0,"data":{"download_url":"https://rousi.pro/signed?passkey=synthetic-material"}}`), 200), nil
		case "/signed":
			if r.Header.Get("Authorization") != "" {
				t.Fatal("credential forwarded to signed URL")
			}
			return response([]byte("d4:infod6:lengthi1e4:name1:x12:piece lengthi16384e6:pieces20:12345678901234567890ee"), 200), nil
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
			return nil, nil
		}
	})
	s, err := c.UserStats(context.Background())
	if err != nil || s.UserID != "7" || s.Level != "VIP" || s.Downloaded == nil || *s.Downloaded != 0 || s.Bonus == nil || *s.Bonus != 123.5 || s.Ratio == nil || *s.Ratio != 3.46 || s.Seeding == nil || *s.Seeding != 4 || s.SeedingInfo != nil {
		t.Fatal("profile", s, err)
	}
	result, err := c.Search(context.Background(), "Example", 2)
	if err != nil || len(result.Resources) != 1 || result.HasMore {
		t.Fatal(result, err)
	}
	item := result.Resources[0]
	if !item.Free || item.UploadFactor != 2 || item.Resolution != "2160p" || item.DetailsURL != "https://rousi.pro/torrent/owned-uuid" {
		t.Fatal(item)
	}
	if _, err = c.Torrent(context.Background(), "9"); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal(calls)
	} // profile, search, detail, material
}

func TestRousiFailuresNeverBecomeSuccessfulEmptyData(t *testing.T) {
	for _, body := range []string{`{"code":401,"message":"synthetic-passkey"}`, `{"code":0,"data":null}`, `{"code":0,"data":{"id":7}}`, `{"code":0,"data":{"id":7,"username":"owned","uploaded":-1}}`, `{"code":0,"data":{"id":7,"username":"owned","karma":"NaN"}}`, `<html>login</html>`} {
		c, _ := New(Config{ID: "owned", Kind: "rousi", BaseURL: "https://rousi.pro", APIKey: "synthetic-passkey"})
		c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return response([]byte(body), 200), nil })
		if _, err := c.UserStats(context.Background()); err == nil || err.Error() == "synthetic-passkey" {
			t.Fatal("accepted failure or leaked key", err)
		}
	}
	c, _ := New(Config{ID: "owned", Kind: "rousi", BaseURL: "https://rousi.pro", APIKey: "synthetic-passkey"})
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response([]byte(`{"code":0,"data":{"id":7,"username":"owned","uploaded":0}}`), 200), nil
	})
	s, err := c.UserStats(context.Background())
	if err != nil || s.Uploaded == nil || *s.Uploaded != 0 || s.Downloaded != nil || s.Seeding != nil {
		t.Fatal("unknown values became zero", s, err)
	}
	for _, raw := range []string{"https://unrelated.invalid/signed", "//evil.invalid/file", "https://rousi.pro@evil.invalid/file", "http://rousi.pro/file", "https://rousi.pro:444/file"} {
		c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]string{"download_url": raw}})
			return response(b, 200), nil
		})
		if _, err = c.Torrent(context.Background(), "9"); err == nil {
			t.Fatal("unsafe material URL accepted", raw)
		}
	}
	for _, raw := range []string{"https://rousi.pro.evil.invalid", "https://evil.invalid", "https://rousi.pro:444", "https://rousi.pro/path"} {
		if _, err := New(Config{ID: "owned", Kind: "rousi", BaseURL: raw, APIKey: "synthetic-passkey"}); err == nil {
			t.Fatal("unreviewed Rousi origin accepted", raw)
		}
	}
}
