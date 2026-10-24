package artifacts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func releaseCopy(base Release, version string) Release {
	r := base
	r.Version = version
	r.Manifest.Version = version
	r.Artifacts = append([]Artifact(nil), base.Artifacts...)
	return r
}
func TestDiscoverActualIdentityCurrentAndHistoryMetadataOnly(t *testing.T) {
	req, client, idx, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
	oldTree(t, req)
	before := snapshotTree(t, req.Root)
	idx.Releases = append(idx.Releases, releaseCopy(idx.Releases[0], "1.1.0"), releaseCopy(idx.Releases[0], "1.0.0"), releaseCopy(idx.Releases[0], "0.9.0"), releaseCopy(idx.Releases[0], ""))
	idx.Releases[2].Artifacts[0].Platform = "linux-arm64"
	idx.Releases[3].Artifacts[0].HostContract = "unknown/v1"
	gets := 0
	base := client.Transport
	client.Transport = countedTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Error("non-GET discovery")
		}
		gets++
		return base.RoundTrip(r)
	})
	got, err := Discover(context.Background(), req.RepositoryURL, req.Platform, req.Validate, client)
	if err != nil {
		t.Fatal(err)
	}
	if got.PluginID != "Owned" || got.CurrentVersion != "1.2.3" || got.RepositoryURL != req.RepositoryURL || got.Current.Manifest.Version != "1.2.3" {
		t.Fatalf("invented identity/current: %+v", got)
	}
	if len(got.Releases) != 2 || got.Releases[0].Manifest.Version != "1.2.3" || got.Releases[1].Manifest.Version != "1.1.0" {
		t.Fatalf("wrong supported history: %+v", got.Releases)
	}
	if len(got.Rejected) != 3 || got.Rejected[0].Version != "1.0.0" || got.Rejected[1].Version != "0.9.0" {
		t.Fatalf("missing rejection diagnostics: %+v", got.Rejected)
	}
	if gets != 1 || *hits != 0 {
		t.Fatalf("GET count %d payload %d", gets, *hits)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, req.Root)) {
		t.Fatal("discovery changed installed files")
	}
}
func TestDiscoverRejectsInvalidCurrentWithoutPayload(t *testing.T) {
	for _, kind := range []string{"schema", "missing_current", "empty_current", "duplicate_release", "wrong_identity", "wrong_version", "host_api", "platform", "contract", "nil_validator", "bad_digest", "invalid_id"} {
		t.Run(kind, func(t *testing.T) {
			req, client, idx, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
			switch kind {
			case "schema":
				idx.SchemaVersion = 2
			case "missing_current":
				idx.CurrentVersion = "99.0"
			case "empty_current":
				idx.CurrentVersion = ""
			case "duplicate_release":
				idx.Releases = append(idx.Releases, idx.Releases[0])
			case "wrong_identity":
				idx.Releases[0].Manifest.PluginID = "Other"
			case "wrong_version":
				idx.Releases[0].Manifest.Version = "2.0.0"
			case "host_api":
				idx.Releases[0].Manifest.HostAPIMin = 99
				idx.Releases[0].Manifest.HostAPIMax = 99
			case "platform":
				req.Platform = "linux-arm64"
			case "contract":
				idx.Releases[0].Artifacts[0].HostContract = "unknown"
			case "nil_validator":
				req.Validate = nil
			case "bad_digest":
				idx.Releases[0].Artifacts[0].SHA256 = "bad"
			case "invalid_id":
				idx.PluginID = "../bad"
			}
			if _, err := Discover(context.Background(), req.RepositoryURL, req.Platform, req.Validate, client); err == nil {
				t.Fatal("invalid current index accepted")
			}
			if *hits != 0 {
				t.Fatal("payload fetched")
			}
		})
	}
}
func TestDiscoverStrictParserRejectsPythonHTMLAndTrailingData(t *testing.T) {
	for _, body := range []string{`{"Owned":{"name":"Python package"}}`, `<html>repository page</html>`, `{"schema_version":1,"plugin_id":"Owned","current_version":"1.0","releases":[]} {}`} {
		req, _, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		if _, err := Discover(context.Background(), srv.URL, req.Platform, req.Validate, srv.Client()); err == nil {
			t.Fatal("non-index accepted")
		}
		srv.Close()
	}
}
