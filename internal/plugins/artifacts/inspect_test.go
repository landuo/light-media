package artifacts

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type countedTransport func(*http.Request) (*http.Response, error)

func (f countedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.Walk(root, func(p string, i os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		s := fmt.Sprintf("%v %d %d", i.Mode(), i.Size(), i.ModTime().UnixNano())
		if i.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			s += fmt.Sprintf(" %x", sha256.Sum256(b))
		}
		out[rel] = s
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestInspectCompatibleIgnoresReleaseWithoutChangingFilesOrFetchingArtifact(t *testing.T) {
	req, client, idx, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
	if _, err := Acquire(context.Background(), req, client); err != nil {
		t.Fatal(err)
	}
	// The current repository metadata moves ahead while installed bytes stay old.
	idx.CurrentVersion = "2.0.0"
	idx.Releases[0].Version = "2.0.0"
	idx.Releases[0].Manifest.Version = "2.0.0"
	req.ReleaseVersion = "999.0.0"
	req.RefreshBackup = true
	before := snapshotTree(t, req.Root)
	indexGets := 0
	transport := client.Transport
	client.Transport = countedTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/index" {
			if r.Method != http.MethodGet {
				t.Error("index request was not GET")
			}
			indexGets++
		}
		return transport.RoundTrip(r)
	})
	payloadGets := *hits
	got, err := InspectCompatible(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.Version != "2.0.0" || got.Platform != "linux-amd64" {
		t.Fatalf("not current metadata: %+v", got)
	}
	if indexGets != 1 {
		t.Fatalf("expected one index GET, got %d", indexGets)
	}
	if *hits != payloadGets {
		t.Fatal("compatibility check fetched artifact")
	}
	if after := snapshotTree(t, req.Root); !reflect.DeepEqual(before, after) {
		t.Fatalf("filesystem changed: before=%v after=%v", before, after)
	}
	req.Root = filepath.Join(t.TempDir(), "must-not-create")
	if _, err = InspectCompatible(context.Background(), req, client); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(req.Root); !os.IsNotExist(err) {
		t.Fatalf("inspection created root: %v", err)
	}
}
func TestInspectCompatibleRejectsCurrentIncompatibilityWithoutMutation(t *testing.T) {
	for _, kind := range []string{"api", "identity", "platform", "contract", "parser"} {
		t.Run(kind, func(t *testing.T) {
			req, client, idx, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
			oldTree(t, req)
			before := snapshotTree(t, req.Root)
			switch kind {
			case "api":
				idx.Releases[0].Manifest.HostAPIMin = 99
				idx.Releases[0].Manifest.HostAPIMax = 99
			case "identity":
				idx.PluginID = "Other"
			case "platform":
				req.Platform = "linux-arm64"
			case "contract":
				idx.Releases[0].Artifacts[0].HostContract = "unknown"
			case "parser":
				idx.SchemaVersion = 2
			}
			if _, err := InspectCompatible(context.Background(), req, client); err == nil {
				t.Fatal("incompatible current declaration accepted")
			}
			if *hits != 0 {
				t.Fatal("artifact fetched")
			}
			if !reflect.DeepEqual(before, snapshotTree(t, req.Root)) {
				t.Fatal("inspection mutated files")
			}
		})
	}
}
