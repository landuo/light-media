package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func localFixture(t *testing.T) (Request, Index, string) {
	t.Helper()
	root := t.TempDir()
	repository, err := LocalRepositoryURL("Owned", root)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("inert-local-owned-bytes")
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "Owned", Name: "Local owned fixture", Version: "1.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"stop"}, Capabilities: []string{}}
	index := Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: "1.0", Releases: []Release{{Version: "1.0", Manifest: manifest, Artifacts: []Artifact{{Platform: "linux-amd64", URL: "payload", Format: "executable", Size: int64(len(payload)), SHA256: digest(payload), ExecutableSHA256: digest(payload), HostContract: "owned/v1"}}}}}
	writeLocalIndex(t, root, index)
	if err := os.WriteFile(filepath.Join(root, "payload"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	req := Request{Root: t.TempDir(), RepositoryURL: repository, PluginID: "Owned", Platform: "linux-amd64", LocalRoots: []string{root}, Validate: func(m plugins.Manifest, contract string) error {
		if m.PluginID != "Owned" || contract != "owned/v1" {
			return errors.New("untrusted contract")
		}
		return nil
	}}
	return req, index, root
}

func writeLocalIndex(t *testing.T, root string, index Index) {
	t.Helper()
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, IndexName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalArtifactDiscoveryAcquireCloneAndDetachedProvenance(t *testing.T) {
	req, _, root := localFixture(t)
	ctx := context.Background()
	discovery, err := DiscoverLocal(ctx, root, req.Platform, req.Validate)
	if err != nil || discovery.RepositoryURL != req.RepositoryURL || discovery.CurrentVersion != "1.0" {
		t.Fatalf("discovery: %+v %v", discovery, err)
	}
	if _, err = os.Stat(filepath.Join(req.Root, req.PluginID)); !os.IsNotExist(err) {
		t.Fatal("discovery installed code")
	}
	got, err := Acquire(ctx, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepositoryURL != req.RepositoryURL {
		t.Fatal("local provenance missing")
	}
	clone, err := CloneInstalled(req.Root, "Owned", "OwnedClone", req.Validate)
	if err != nil || clone.RepositoryURL != req.RepositoryURL || clone.SHA256 != got.SHA256 {
		t.Fatalf("clone: %+v %v", clone, err)
	}
	if err = os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"Owned", "OwnedClone"} {
		if _, err = ReadInstalled(req.Root, id, req.Validate); err != nil {
			t.Fatalf("receipt incorrectly grants/requires live source authority: %v", err)
		}
	}
}

func TestLocalArtifactUntrustedRootsAndTraversalRejectedBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"no-config", "other-root", "parent-config", "wrong-id", "extra-query", "absolute", "traversal", "file-url", "http-url", "index-link"} {
		t.Run(scenario, func(t *testing.T) {
			req, index, root := localFixture(t)
			req.Force = true
			oldTree(t, req)
			switch scenario {
			case "no-config":
				req.LocalRoots = nil
			case "other-root":
				req.LocalRoots = []string{t.TempDir()}
			case "parent-config":
				req.LocalRoots = []string{filepath.Dir(root)}
			case "wrong-id":
				req.RepositoryURL = strings.Replace(req.RepositoryURL, "local://Owned?", "local://Other?", 1)
			case "extra-query":
				req.RepositoryURL += "&path=/another"
			case "absolute":
				index.Releases[0].Artifacts[0].URL = filepath.Join(root, "payload")
			case "traversal":
				index.Releases[0].Artifacts[0].URL = "../payload"
			case "file-url":
				index.Releases[0].Artifacts[0].URL = "file:///payload"
			case "http-url":
				index.Releases[0].Artifacts[0].URL = "https://example.invalid/payload"
			}
			writeLocalIndex(t, root, index)
			if scenario == "index-link" {
				if err := os.Rename(filepath.Join(root, IndexName), filepath.Join(root, "index-real")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("index-real", filepath.Join(root, IndexName)); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Acquire(context.Background(), req, nil)
			var stage *StageError
			if !errors.As(err, &stage) || stage.Stage != "preflight" {
				t.Fatalf("not preflight: %v", err)
			}
			if _, err = os.Stat(filepath.Join(req.Root, req.PluginID, "old")); err != nil {
				t.Fatal("preflight changed installed tree", err)
			}
		})
	}
}

func TestLocalArtifactBrokenPublicationRestoresInstalledCode(t *testing.T) {
	for _, scenario := range []string{"symlink", "digest", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			req, _, root := localFixture(t)
			initial, err := Acquire(context.Background(), req, nil)
			if err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(root, "payload")
			switch scenario {
			case "symlink":
				if err = os.Rename(payload, filepath.Join(root, "real")); err == nil {
					err = os.Symlink("real", payload)
				}
			case "digest":
				err = os.WriteFile(payload, []byte("incorrect-owned-payload"), 0600)
			case "missing":
				err = os.Remove(payload)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = Acquire(context.Background(), req, nil)
			var stage *StageError
			if !errors.As(err, &stage) || stage.Stage != "prepare" || !stage.Restored {
				t.Fatalf("not restored: %v", err)
			}
			retained, err := ReadInstalled(req.Root, req.PluginID, req.Validate)
			if err != nil || retained.SHA256 != initial.SHA256 {
				t.Fatalf("code lost: %+v %v", retained, err)
			}
		})
	}
}

func TestLocalArtifactFingerprintTracksIndexAndPayloadPublication(t *testing.T) {
	req, index, root := localFixture(t)
	fingerprint := func() string {
		t.Helper()
		value, err := LocalFingerprint(context.Background(), req.RepositoryURL, req.Platform, req.LocalRoots, req.Validate)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := fingerprint()
	if again := fingerprint(); first != again {
		t.Fatal("unchanged publication changed identity")
	}
	at := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "payload"), at, at); err != nil {
		t.Fatal(err)
	}
	second := fingerprint()
	if second == first {
		t.Fatal("payload publication was missed")
	}
	index.Releases[0].Manifest.Description = "updated source metadata"
	writeLocalIndex(t, root, index)
	if fingerprint() == second {
		t.Fatal("index publication was missed")
	}
	if _, err := LocalFingerprint(context.Background(), req.RepositoryURL, req.Platform, nil, req.Validate); err == nil {
		t.Fatal("fingerprinting escaped configured roots")
	}
}
