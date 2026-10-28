package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOriginalMediaCategoriesSourceAndActualRuleAPI(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Name     string
			Plugin   json.RawMessage
			Fail     bool
			Result   json.RawMessage
			Error    *string
			Calls    []string
			Messages int
		}
	}
	raw, err := os.ReadFile("../../testdata/standard-module-host/media-category-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("fixed oracle", err)
	}
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "category")
	if err = os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte("movie:\n  原生电影: {}\ntv:\n  原生剧集: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			method := "module_execute"
			if c.Fail {
				method = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"media_category": method}, "result": c.Plugin})
			saveOwnedModule(t, a, cookie, source, string(cfg))
			saveOwnedModule(t, a, cookie, clone, `{"enabled":true,"modules":{"media_category":"module_execute"},"result":null}`)
			beforeSource, beforeClone := ownedModuleRead(t, source), ownedModuleRead(t, clone)
			r := callTest(t, a.Handler(), "GET", "/api/settings/rules", "", cookie, "")
			if c.Error != nil {
				if r.Code != 500 {
					t.Fatal("source response-model rejection", r.Code, r.Body.String())
				}
			} else {
				var response struct {
					Data struct{ Categories json.RawMessage }
				}
				if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || !originalBaseJSONEqual(response.Data.Categories, c.Result) {
					t.Fatal("source category result", r.Code, r.Body.String(), string(c.Result))
				}
			}
			afterSource, afterClone := ownedModuleRead(t, source), ownedModuleRead(t, clone)
			wantClone := 0
			if slices.Contains(c.Calls, "clone") {
				wantClone = 1
			}
			if afterSource.Calls[method]-beforeSource.Calls[method] != 1 || afterClone.Calls["module_execute"]-beforeClone.Calls["module_execute"] != wantClone {
				t.Fatal("source/clone dispatch", c.Calls, afterSource.Calls, afterClone.Calls)
			}
			if !originalBaseJSONEqual(afterSource.Inputs[method], json.RawMessage(`{"args":[],"kwargs":{}}`)) {
				t.Fatal("no media_category arguments", string(afterSource.Inputs[method]))
			}
		})
	}
	// A terminal dict must not read the native policy (nor misclassify it as a title).
	if err = os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte("!unsupported\n"), 0600); err != nil {
		t.Fatal(err)
	}
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":{"media_category":"module_execute"},"result":{"电影":["独立分类"]}}`)
	r := callTest(t, a.Handler(), "GET", "/api/settings/rules", "", cookie, "")
	if r.Code != 200 {
		t.Fatal("terminal override must skip policy", r.Body.String())
	}
}
