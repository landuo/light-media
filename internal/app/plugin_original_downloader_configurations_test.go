package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestDownloaderConfigurationsActualSDKPreservesSettingsOrderWithoutCredentials(t *testing.T) {
	a, object := sourceDownloaderFixture(t)
	var network atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { network.Add(1) }))
	defer peer.Close()
	no := false
	cfg := a.Settings()
	cfg.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "z-tr", Kind: "transmission", BaseURL: peer.URL, Username: "OWNED_USER", Password: "OWNED_PASSWORD"}, Name: "B"},
		{Config: download.Config{ID: "a-qb", Kind: "qbittorrent", BaseURL: peer.URL, APIKey: "OWNED_KEY"}, Name: "A"},
		{Config: download.Config{ID: "b-qb", Kind: "qbittorrent", BaseURL: peer.URL}, Name: "B"},
		{Config: download.Config{ID: "c-off", Kind: "unknown"}, Name: "off", Enabled: &no},
	}
	if err := a.saveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "configurations", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal("actual SDK ordered configurations", err)
	}
	var got plugins.DownloaderConfigurationsV1
	if json.Unmarshal(result.Result, &got) != nil {
		t.Fatal("invalid ordered configurations", string(result.Result))
	}
	want := []plugins.BrushDownloaderReferenceV1{{ID: "z-tr", Name: "B", Kind: "transmission", Enabled: true}, {ID: "a-qb", Name: "A", Kind: "qbittorrent", Enabled: true}, {ID: "b-qb", Name: "B", Kind: "qbittorrent", Enabled: true}, {ID: "c-off", Name: "off", Kind: "unknown", Enabled: false}}
	if got.SchemaVersion != 1 || !reflect.DeepEqual(got.Downloaders, want) {
		t.Fatal("saved configuration order, duplicate name or disabled record changed", got)
	}
	for _, secret := range [][]byte{[]byte("OWNED_USER"), []byte("OWNED_PASSWORD"), []byte("OWNED_KEY"), []byte(peer.URL)} {
		if bytes.Contains(result.Result, secret) {
			t.Fatal("ordered configuration leaked connection material")
		}
	}
	if network.Load() != 0 {
		t.Fatal("configuration read contacted daemons")
	}
	old, err := a.originalBrushInventory(context.Background())
	if err != nil || len(old.Downloaders) != 4 || old.Downloaders[0].ID != "a-qb" || old.Downloaders[3].ID != "z-tr" {
		t.Fatal("legacy sorted inventory contract changed", old, err)
	}
	for _, input := range []string{`{"downloader_id":"a-qb"}`, `[]`} {
		if _, err := a.originalDownloaderConfigurations(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatal("nonempty/nonobject configuration request accepted", input)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.originalDownloaderConfigurations(ctx, json.RawMessage(`{}`)); err == nil {
		t.Fatal("canceled configuration read accepted")
	}
}
