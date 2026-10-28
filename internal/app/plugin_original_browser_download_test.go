package app

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOriginalBrowserDownloadActualSDKSource(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Action, Name string
			Plugin       json.RawMessage
			Fail         bool
			Calls        []string
			Body         *string
			Success      *bool
			Error        *string
		}
	}
	raw, err := os.ReadFile("../../testdata/standard-module-host/browser-download-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 16 {
		t.Fatal("source oracle", err)
	}
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "download")
	dir := t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = []string{dir}
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"native.txt": "owned native content", "override.txt": "owned plugin content"} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	uri := "/api/files/content?" + url.Values{"path": {filepath.Join(dir, "native.txt")}, "root_id": {a.browserRoots()[0].ID}}.Encode()
	for _, c := range oracle.Cases {
		t.Run(c.Action+"-"+c.Name, func(t *testing.T) {
			value := c.Plugin
			var name string
			if string(value) != "null" && json.Unmarshal(value, &name) == nil {
				value, _ = json.Marshal(filepath.Join(dir, name))
			}
			method := "module_execute"
			if c.Fail {
				method = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"download_file": method}, "result": value})
			saveOwnedModule(t, a, cookie, source, string(cfg))
			saveOwnedModule(t, a, cookie, clone, `{"enabled":true,"modules":{"download_file":"module_execute"},"result":null}`)
			s0, c0 := ownedModuleRead(t, source), ownedModuleRead(t, clone)
			result := callTest(t, a.Handler(), "GET", uri, "", cookie, "")
			if c.Body != nil {
				if result.Code != 200 || result.Body.String() != *c.Body {
					t.Fatal("source bytes", result.Code, result.Body.String(), *c.Body)
				}
			} else if result.Code == 200 {
				t.Fatal("native GET must fail instead of downloading a failure envelope", result.Body.String())
			}
			s1, c1 := ownedModuleRead(t, source), ownedModuleRead(t, clone)
			n := 0
			if slices.Contains(c.Calls, "clone") {
				n = 1
			}
			if s1.Calls[method]-s0.Calls[method] != 1 || c1.Calls["module_execute"]-c0.Calls["module_execute"] != n {
				t.Fatal("source call order", c.Calls, s1.Calls, c1.Calls)
			}
			var input struct {
				Args   []any
				Kwargs map[string]json.RawMessage
			}
			if json.Unmarshal(s1.Inputs[method], &input) != nil || len(input.Kwargs) != 2 || string(input.Kwargs["path"]) != "null" {
				t.Fatal("fixed fileitem/path args", string(s1.Inputs[method]))
			}
			var item map[string]any
			if json.Unmarshal(input.Kwargs["fileitem"], &item) != nil || item["path"] != filepath.Join(dir, "native.txt") || item["storage"] != "local" {
				t.Fatal("fileitem", item)
			}
		})
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("private outside"), 0600)
	link := filepath.Join(dir, "link.txt")
	if err = os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, link, dir} {
		cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"download_file": "module_execute"}, "result": path})
		saveOwnedModule(t, a, cookie, source, string(cfg))
		response := callTest(t, a.Handler(), "GET", uri, "", cookie, "")
		if response.Code == 200 {
			t.Fatal("native path/regular file boundary lost", path)
		}
	}
}
