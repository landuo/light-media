package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/url"
	"strings"
	"testing"
)

func TestOriginalCloneFrontendActualSDKLiteralRuntimeCSSAndColdClass(t *testing.T) {
	a, cookie, _ := originalFrontendFixture(t)
	source := originalFrontendFixtureID
	clone := source + "copy"
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	descriptor := originalFrontendReadDescriptor(t, a, cookie, clone)
	chunk := strings.TrimSuffix("/api"+descriptor.EntryPath, "remoteEntry.js") + "chunk.js"
	response = callTest(t, a.Handler(), "GET", chunk, "", cookie, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "plugin/"+clone+"/status") || !strings.Contains(response.Body.String(), "const id='OwnedFront'+'endFixture'") {
		t.Fatal("actual clone assets failed literal/global/runtime source distinction", response.Code, response.Body.String())
	}
	root := strings.TrimSuffix("/api"+descriptor.EntryPath, "remoteEntry.js")
	response = callTest(t, a.Handler(), "GET", root+strings.ToLower(clone)+"-theme.css", "", cookie, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "--"+strings.ToLower(clone)+"-class") {
		t.Fatal("actual clone CSS class/filename not derived", response.Code, response.Body.String())
	}
	response = callTest(t, a.Handler(), "GET", root+strings.ToLower(source)+"-theme.css", "", cookie, "")
	if response.Code != 404 {
		t.Fatal("old source filename unexpectedly retained", response.Code)
	}
	code, err := artifacts.ReadInstalled(a.pluginPackageRoot(), clone, validatePluginArtifactContract)
	if err != nil || code.FrontendClass != clone || code.Manifest.PluginID != source {
		t.Fatal("cold class/compiled identity mismatch", err)
	}
	// Re-running the real resolver over a persistent receipt does not apply the
	// textual replacement twice and retains the real immutable content pin.
	installed, err := a.pluginStore.Installation(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := a.resolvePluginImplementation(context.Background(), installed)
	if err != nil || resolved.PackageSHA256 != descriptor.PackageSHA256 {
		t.Fatal("cold resolver re-derived already cloned resources", err)
	}
}

// Actual copied bytes and HTTP descriptors exercise Unicode URL encoding as
// well as the fixed full lowercase class/CSS/file replacement.
func TestOriginalCloneFrontendUnicodeHTTPGraph(t *testing.T) {
	a, cookie, _ := originalFrontendFixture(t)
	source := originalFrontendFixtureID
	for _, suffix := range []string{"备份", "İCopy", "ΟΣ"} {
		clone := source + plugins.OriginalPluginLower(suffix)
		body, _ := json.Marshal(map[string]string{"suffix": suffix})
		response := callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		descriptor := originalFrontendReadDescriptor(t, a, cookie, clone)
		if !strings.HasPrefix(descriptor.EntryPath, "/plugins/"+url.PathEscape(clone)+"/") {
			t.Fatal("Unicode instance descriptor URL was not encoded")
		}
		root := strings.TrimSuffix("/api"+descriptor.EntryPath, "remoteEntry.js")
		response = callTest(t, a.Handler(), "GET", root+"chunk.js", "", cookie, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), "plugin/"+clone+"/status") {
			t.Fatal("actual Unicode copied global API literal", response.Code)
		}
		response = callTest(t, a.Handler(), "GET", root+url.PathEscape(plugins.OriginalPluginLower(clone)+"-theme.css"), "", cookie, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), "--"+plugins.OriginalPluginLower(clone)+"-class") {
			t.Fatal("actual Unicode CSS filename/full case map", response.Code)
		}
	}
}

func TestOriginalCloneFrontendPhysicalSymbolsSourceCode(t *testing.T) {
	a, cookie, _ := originalFrontendFixture(t)
	source := originalFrontendFixtureID
	for _, suffix := range []string{"💫", "!", "a?b", "a#b", "a%b", "a:b", "a'b", "a\"b", "a+b", "a;b"} {
		t.Run(suffix, func(t *testing.T) {
			clone := source + plugins.OriginalPluginLower(suffix)
			body, _ := json.Marshal(map[string]string{"suffix": suffix})
			response := callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", string(body), cookie, "")
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			if _, ok := a.pluginRunningInstances.Load(clone); ok {
				t.Fatal("source symbol clone must not gain a callable class")
			}
			installed, e := artifacts.ReadInstalled(a.pluginPackageRoot(), clone, validatePluginArtifactContract)
			if e != nil || installed.FrontendClass != clone {
				t.Fatal("physical symbol clone inventory", e)
			}
			base := "/api/v1/plugin/file/" + url.PathEscape(clone) + "/frontend/assets/"
			file := callTest(t, a.Handler(), "GET", base+url.PathEscape(plugins.OriginalPluginLower(clone)+"-theme.css"), "", cookie, "")
			if suffix == "💫" {
				if file.Code != 500 || !strings.Contains(file.Body.String(), "Internal Server Error") {
					t.Fatal("fixed Source inline filename header must fail before streaming", file.Code)
				}
				asset, e := artifacts.ReadFrontendAsset(a.pluginPackageRoot(), clone, installed.PackageSHA256, plugins.OriginalPluginLower(clone)+"-theme.css", validatePluginArtifactContract)
				if e != nil || !strings.Contains(string(asset.Data), "--"+plugins.OriginalPluginLower(clone)+"-class") {
					t.Fatal("nonLatin1 header error must not corrupt physical CSS", e)
				}
			} else if file.Code != 200 || !strings.Contains(file.Body.String(), "--"+plugins.OriginalPluginLower(clone)+"-class") {
				t.Fatal("source symbol CSS file/name bytes", file.Code)
			}
			file = callTest(t, a.Handler(), "GET", base+"chunk.js", "", cookie, "")
			if file.Code != 200 || !strings.Contains(file.Body.String(), "plugin/"+clone+"/status") {
				t.Fatal("source symbol JS literal bytes", file.Code)
			}
			response = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+url.PathEscape(clone), "", cookie, "")
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			if file = callTest(t, a.Handler(), "GET", base+"chunk.js", "", cookie, ""); file.Code != 200 {
				t.Fatal("unloaded source class uninstall must retain copied frontend code", file.Code)
			}
		})
	}
}
