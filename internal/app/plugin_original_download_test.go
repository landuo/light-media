package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"light-media/internal/download"
)

// The oracle executes the fixed wrapper, dispatcher, signature check and the
// exact download_single result-unpacking statements. Only the core Add tuple
// is controlled here; the separate protocol/main gate proves actual tasks.
func TestOriginalDownloadActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/downloader-add-oracle.json")
	var oracle struct {
		Cases []struct {
			Name          string
			PluginResult  json.RawMessage `json:"plugin_result"`
			PluginFailure bool            `json:"plugin_failure"`
			CoreFailure   bool            `json:"core_failure"`
			Accepted      bool
			CallerHash    string `json:"caller_hash"`
			Observations  []struct {
				Kind   string
				Kwargs json.RawMessage
			}
			Roles []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 {
		t.Fatal("fixed download wrapper/caller source oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			method := "module_execute"
			if sample.PluginFailure {
				method = "module_fail"
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"download": method}, "result": sample.PluginResult})
			saveOwnedModule(t, a, cookie, object, string(config))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			for len(a.systemMessages.get("system")) > 0 {
			}
			coreCalls, marked := 0, 0
			out, err := a.originalDownload(context.Background(), DownloaderConfig{Config: download.Config{ID: "owned-downloader", Kind: "qb"}, Name: "Owned downloader"}, download.AddRequest{Torrent: []byte{'o', 'w', 'n', 'e', 'd', 0, 255}, SavePath: "/owned/downloads"}, originalDownloadOptions{Cookie: "owned-cookie", Episodes: []int{2, 4}, Category: "华语电视剧", Downloader: json.RawMessage(`"Owned downloader"`), BeforeInvoke: func() error { marked++; return nil }}, func(options originalDownloadOptions) (download.AddResult, error) {
				coreCalls++
				if sample.CoreFailure {
					return download.AddResult{}, errors.New("owned core failure")
				}
				return download.AddResult{Hash: "0123456789012345678901234567890123456789"}, nil
			})
			if (err == nil) != sample.Accepted || sample.Accepted && out.Added.Hash != sample.CallerHash {
				t.Fatal("fixed caller truth/unpack/hash changed", out, err)
			}
			wantCore := 0
			var want json.RawMessage
			for _, ob := range sample.Observations {
				if ob.Kind == "core" {
					wantCore++
				} else if ob.Kind == "plugin" {
					want = ob.Kwargs
				}
			}
			after := ownedModuleRead(t, object)
			var actual struct {
				Args   []json.RawMessage
				Kwargs json.RawMessage
			}
			if marked != 1 || coreCalls != wantCore || out.CoreCalled != (wantCore > 0) || after.Calls[method]-before.Calls[method] != 1 || json.Unmarshal(after.Inputs[method], &actual) != nil || actual.Args == nil || len(actual.Args) != 0 || !originalBaseJSONEqual(actual.Kwargs, want) {
				t.Fatal("actual SDK seven kwargs/core branch changed", marked, coreCalls, wantCore, string(actual.Kwargs), string(want))
			}
			a.wg.Wait()
			for _, role := range sample.Roles {
				var message systemMessage
				if json.Unmarshal(a.systemMessages.get("system"), &message) != nil || message.Type != role {
					t.Fatal("source exception role", role, message.Type)
				}
			}
			if len(a.systemMessages.get("system")) != 0 {
				t.Fatal("unexpected source exception helper message")
			}
		})
	}
}

func TestOriginalDownloadSelectionActualSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/downloader-add-oracle.json")
	var oracle struct {
		Cases []struct {
			Explicit, Site *string
			Selected       json.RawMessage
		} `json:"selection_cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 3 {
		t.Fatal("actual downloader selection source oracle", err)
	}
	a := newTestApp(t)
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "default-id", Kind: "qb"}, Name: "Default downloader"}, {Config: download.Config{ID: "site-id", Kind: "qb"}, Name: "Site downloader"}, {Config: download.Config{ID: "explicit-id", Kind: "qb"}, Name: "Explicit downloader"}}
	a.config.DefaultDownloader = "default-id"
	for _, sample := range oracle.Cases {
		request := ""
		site := SiteConfig{}
		if sample.Site != nil {
			site.Downloader = *sample.Site
		}
		if sample.Explicit != nil {
			request = "explicit-id"
		}
		d, choice, err := a.originalDownloadSelection(request, site)
		if err != nil || !originalBaseJSONEqual(choice, sample.Selected) {
			t.Fatal("source explicit-or-site choice", d.ID, string(choice), string(sample.Selected), err)
		}
		want := "default-id"
		if sample.Site != nil {
			want = "site-id"
		}
		if sample.Explicit != nil {
			want = "explicit-id"
		}
		if d.ID != want {
			t.Fatal("module name and actual native downloader selection diverged", d.ID, want)
		}
	}
}
