package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/sites"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyRousiLoadsNativeWithoutRewritingConfiguration(t *testing.T) {
	a := newTestApp(t)
	legacy := a.Settings()
	legacy.Sites = []SiteConfig{{Config: sites.Config{ID: "stable-id", Kind: "nexusphp", Name: "Rousi Pro", BaseURL: "https://rousi.pro", APIKey: "synthetic-passkey", Cookie: "synthetic-old-cookie"}, Enabled: true}}
	sealed, err := a.Store.Seal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?)`, sealed, now()); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	got := cold.Settings().Sites[0]
	if got.ID != "stable-id" || got.Kind != "rousi" || got.APIKey != "synthetic-passkey" || got.Cookie != "synthetic-old-cookie" || got.BaseURL != "https://rousi.pro" {
		t.Fatal("legacy identity or credential changed")
	}
	var stored []byte
	if err = cold.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&stored); err != nil || !bytes.Equal(stored, sealed) {
		t.Fatal("load rewrote persistent config", err)
	}
}

type rousiTestTransport func(*http.Request) (*http.Response, error)

func (f rousiTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestManagedRousiImportRefreshHistoryAndSecretPreservation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	report, err := a.importManagedSites(ctx, json.RawMessage(`[{"name":"肉丝","url":"https://www.rousi.pro/","apikey":"synthetic-rousi-key","is_active":true}]`))
	if err != nil || report.Created != 1 || report.Failed != 0 || len(report.Results[0].Warnings) != 0 {
		t.Fatal(report, err)
	}
	site := a.Settings().Sites[0]
	if site.Kind != "rousi" || site.BaseURL != "https://rousi.pro" || site.APIKey != "synthetic-rousi-key" {
		t.Fatal("adapter not selected")
	}
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	failed := false
	http.DefaultTransport = rousiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "rousi.pro" || r.URL.Path != "/api/v1/profile" || r.Header.Get("Authorization") != "Bearer synthetic-rousi-key" || r.Header.Get("Cookie") != "" {
			return nil, fmt.Errorf("unexpected synthetic request")
		}
		body := `{"code":0,"data":{"id":7,"username":"owned","level_text":"Lv.5","uploaded":123,"downloaded":0,"karma":5,"seeding_leeching_data":{"seeding_count":0,"seeding_size":0}}}`
		if failed {
			body = `{"code":401,"message":"synthetic-rousi-key"}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	result, err := a.refreshManagedSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "synthetic-rousi-key") || !strings.Contains(string(raw), `"status":"ok"`) {
		t.Fatal("refresh failed or leaked key")
	}
	history, err := a.managedSiteHistory(ctx, site.ID)
	if err != nil || len(history.(map[string]any)["items"].([]siteSnapshot)) != 1 {
		t.Fatal("history", err)
	}
	failed = true
	result, err = a.refreshManagedSite(ctx, site.ID)
	raw, _ = json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), `"status":"failed"`) || !strings.Contains(string(raw), `"uploaded":123`) || strings.Contains(string(raw), "synthetic-rousi-key") {
		t.Fatal("failed refresh erased stats or leaked secret", err)
	}
	site.APIKey = ""
	if _, err = a.saveManagedSite(ctx, site); err != nil || a.Settings().Sites[0].APIKey != "synthetic-rousi-key" {
		t.Fatal("blank edit lost key", err)
	}
}
