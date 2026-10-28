package artifacts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyCatalogIndex(t *testing.T, index Index, id string) Index {
	t.Helper()
	raw, _ := json.Marshal(index)
	var copy Index
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	copy.PluginID = id
	for i := range copy.Releases {
		copy.Releases[i].Manifest.PluginID = id
	}
	return copy
}

func TestRepositoryCatalogDiscoverSelectAcquireAndMalformedDocuments(t *testing.T) {
	req, client, first, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
	second := copyCatalogIndex(t, *first, "Second")
	unsupported := copyCatalogIndex(t, *first, "Unsupported")
	unsupported.Releases[0].Artifacts[0].Platform = "unsupported-arch"
	repository := RepositoryIndex{SchemaVersion: 1, Plugins: []Index{*first, second, unsupported}}
	document, _ := json.Marshal(repository)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(document) }))
	defer server.Close()
	req.RepositoryURL = server.URL
	items, err := DiscoverRepository(context.Background(), server.URL, req.Platform, req.Validate, client)
	if err != nil || len(items) != 3 || items[0].PluginID != "Owned" || items[1].PluginID != "Second" || items[0].UnavailableReason != "" || items[2].UnavailableReason == "" || *hits != 0 {
		t.Fatalf("catalog discovery %+v %v downloads=%d", items, err, *hits)
	}
	if items[0].AddTime != 3 || items[1].AddTime != 2 {
		t.Fatal("author order lost")
	}
	for _, id := range []string{"Owned", "Second"} {
		req.PluginID = id
		got, err := Acquire(context.Background(), req, client)
		if err != nil || got.Manifest.PluginID != id || got.RepositoryURL != server.URL {
			t.Fatalf("exact ID acquisition %s %+v %v", id, got, err)
		}
	}
	if *hits != 2 {
		t.Fatal("wrong payload downloads", *hits)
	}
	for _, id := range []string{"Missing", "Unsupported"} {
		req.PluginID = id
		if _, err := Acquire(context.Background(), req, client); err == nil {
			t.Fatal("unavailable catalog ID acquired", id)
		}
		if _, err := os.Stat(filepath.Join(req.Root, id)); !os.IsNotExist(err) {
			t.Fatal("failed preflight wrote code")
		}
	}
	if *hits != 2 {
		t.Fatal("unavailable ID downloaded a payload")
	}
	for _, raw := range []string{
		`{"schema_version":1,"plugins":null}`,
		`{"schema_version":2,"plugins":[]}`,
		`{"schema_version":1,"plugin_id":"Mixed","plugins":[]}`,
		`{"schema_version":1,"plugins":[{"schema_version":1,"plugin_id":"Same"},{"schema_version":1,"plugin_id":"same"}]}`,
		`{"schema_version":1,"plugins":[{"schema_version":1,"plugin_id":"Nested","plugins":[]}]}`,
	} {
		if _, err := parseRepository([]byte(raw)); err == nil {
			t.Fatal("malformed catalog accepted", raw)
		}
	}
}

func TestRepositoryLocalCatalogSiblingIsolationAndExactIdentity(t *testing.T) {
	req, index, root := localFixture(t)
	second := copyCatalogIndex(t, index, "Second")
	second.Releases[0].Artifacts[0].URL = "second"
	data, _ := os.ReadFile(filepath.Join(root, "payload"))
	if err := os.WriteFile(filepath.Join(root, "second"), data, 0600); err != nil {
		t.Fatal(err)
	}
	publish := func(indexes []Index) {
		t.Helper()
		raw, _ := json.Marshal(RepositoryIndex{SchemaVersion: 1, Plugins: indexes})
		if err := os.WriteFile(filepath.Join(root, IndexName), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	publish([]Index{index, second})
	first, err := LocalFingerprint(context.Background(), req.RepositoryURL, req.Platform, req.LocalRoots, req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	second.History = map[string]string{"1.0": "sibling publication"}
	publish([]Index{second, index})
	after, err := LocalFingerprint(context.Background(), req.RepositoryURL, req.Platform, req.LocalRoots, req.Validate)
	if err != nil || first != after {
		t.Fatal("sibling metadata/order changed unchanged plugin fingerprint", err)
	}
	discovered, err := DiscoverLocalRepository(context.Background(), root, req.Platform, req.Validate)
	if err != nil || len(discovered) != 2 || !strings.HasPrefix(discovered[0].RepositoryURL, "local://Second?") || !strings.HasPrefix(discovered[1].RepositoryURL, "local://Owned?") {
		t.Fatal(discovered, err)
	}
	if _, err := Acquire(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	req.PluginID = "Second" // retaining local://Owned must not select the sibling
	if _, err := Acquire(context.Background(), req, nil); err == nil {
		t.Fatal("local source ID mismatch accepted")
	}
}

func TestRepositoryPythonMetadataCannotAcquireOrInspect(t *testing.T) {
	raw := []byte(`{"OriginalOne":{"name":"原插件一","version":"1.0","description":"说明","labels":["工具","开发"],"v2":true},"OriginalTwo":{"name":"原插件二","version":"2.0","history":{"2.0":"作者说明"}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	items, err := DiscoverRepository(context.Background(), server.URL, req.Platform, req.Validate, client)
	if err != nil || len(items) != 2 || items[0].PluginID != "OriginalOne" || items[0].Current.Manifest.Runtime != "python" || items[0].UnavailableReason == "" || items[1].UnavailableReason == "" {
		t.Fatalf("Python metadata %+v %v", items, err)
	}
	req.RepositoryURL, req.PluginID = server.URL, "OriginalOne"
	if _, err := Acquire(context.Background(), req, client); err == nil {
		t.Fatal("Python metadata acquired")
	}
	if _, err := InspectCompatible(context.Background(), req, client); err == nil {
		t.Fatal("Python metadata admitted by existing-class inspection")
	}
	if _, err := os.Stat(filepath.Join(req.Root, req.PluginID)); !os.IsNotExist(err) {
		t.Fatal("Python metadata wrote runtime code")
	}
}
