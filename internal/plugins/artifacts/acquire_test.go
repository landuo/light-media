package artifacts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func fixture(t *testing.T, payload []byte, format string) (Request, *http.Client, *Index, *int) {
	t.Helper()
	hits := 0
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "Owned", Name: "Owned inert fixture", Version: "1.2.3", Runtime: "go-executable", Entry: "bin/worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"stop"}, Capabilities: []string{}}
	idx := &Index{SchemaVersion: 1, PluginID: "Owned", CurrentVersion: "1.2.3", Releases: []Release{{Version: "1.2.3", Manifest: m, Artifacts: []Artifact{{Platform: "linux-amd64", Format: format, SHA256: digest(payload), ExecutableSHA256: digest([]byte("inert-owned-bytes")), Size: int64(len(payload)), HostContract: "owned/v1"}}}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			json.NewEncoder(w).Encode(idx)
			return
		}
		hits++
		w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	idx.Releases[0].Artifacts[0].URL = srv.URL + "/payload"
	req := Request{Root: t.TempDir(), RepositoryURL: srv.URL + "/index", PluginID: "Owned", Platform: "linux-amd64", Validate: func(m plugins.Manifest, c string) error {
		if c != "owned/v1" {
			return errors.New("untrusted contract")
		}
		return nil
	}}
	return req, srv.Client(), idx, &hits
}
func oldTree(t *testing.T, req Request) {
	t.Helper()
	p := filepath.Join(req.Root, req.PluginID)
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "old"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestAcquireInertPayloadNeverExecuted(t *testing.T) {
	req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	got, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.Version != "1.2.3" || got.HostContract != "owned/v1" || got.Command.Path != got.ExecutablePath {
		t.Fatalf("bad result: %+v", got)
	}
	b, err := os.ReadFile(got.ExecutablePath)
	if err != nil || string(b) != "inert-owned-bytes" {
		t.Fatalf("payload %q %v", b, err)
	}
	if _, err = os.Stat(filepath.Join(req.Root, req.PluginID, ".acquired.json")); err != nil {
		t.Fatal(err)
	}
}
func TestPreflightPreservesOldEvenForce(t *testing.T) {
	for _, change := range []string{"release", "platform", "hash", "manifest", "contract", "validator", "python", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			req, client, idx, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
			req.Force = true
			oldTree(t, req)
			switch change {
			case "release":
				req.ReleaseVersion = "99.0"
			case "platform":
				req.Platform = "linux-arm64"
			case "hash":
				idx.Releases[0].Artifacts[0].SHA256 = "invalid"
			case "manifest":
				idx.Releases[0].Manifest.Version = "1.2.4"
			case "contract":
				idx.Releases[0].Artifacts[0].HostContract = "arbitrary/unsafe"
			case "validator":
				req.Validate = nil
			case "python":
				idx.Releases[0].Manifest.Runtime = "python"
			case "duplicate":
				idx.Releases = append(idx.Releases, idx.Releases[0])
			}
			_, err := Acquire(context.Background(), req, client)
			var stage *StageError
			if !errors.As(err, &stage) || stage.Stage != "preflight" {
				t.Fatalf("%v", err)
			}
			if _, err = os.Stat(filepath.Join(req.Root, req.PluginID, "old")); err != nil {
				t.Fatal(err)
			}
			if *hits != 0 {
				t.Fatal("download before metadata acceptance")
			}
		})
	}
}
func TestPrepareFailureRestorationMatchesOriginal(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{true: "force", false: "nonforce"}[force], func(t *testing.T) {
			req, client, idx, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
			req.Force = force
			oldTree(t, req)
			idx.Releases[0].Artifacts[0].SHA256 = strings.Repeat("0", 64)
			_, err := Acquire(context.Background(), req, client)
			var stage *StageError
			if !errors.As(err, &stage) || stage.Stage != "prepare" || stage.Restored == force {
				t.Fatalf("%+v", err)
			}
			_, statErr := os.Stat(filepath.Join(req.Root, req.PluginID, "old"))
			if force && !os.IsNotExist(statErr) {
				t.Fatalf("force restored old: %v", statErr)
			}
			if !force && statErr != nil {
				t.Fatal(statErr)
			}
		})
	}
}
func TestOldRemovedBeforeHTTPPayload(t *testing.T) {
	for _, force := range []bool{false, true} {
		req, client, idx, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
		req.Force = force
		oldTree(t, req)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := os.Stat(filepath.Join(req.Root, req.PluginID)); !os.IsNotExist(err) {
				t.Error("old code present during download")
			}
			http.Error(w, "owned failure", 503)
		}))
		idx.Releases[0].Artifacts[0].URL = srv.URL
		_, err := Acquire(context.Background(), req, client)
		srv.Close()
		if err == nil {
			t.Fatal("expected failure")
		}
	}
}
func zipBytes(t *testing.T, name string, mode os.FileMode) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := z.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("inert-owned-bytes"))
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestZIPPathsAndTypes(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/absolute", "bin/../escape", "bin\\escape", "./bin/worker", "bin//worker"} {
		t.Run(name, func(t *testing.T) {
			if err := unpack(zipBytes(t, name, 0600), "zip", t.TempDir(), "bin/worker"); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	if err := unpack(zipBytes(t, "bin/worker", os.ModeSymlink|0777), "zip", t.TempDir(), "bin/worker"); err == nil {
		t.Fatal("symlink accepted")
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 2; i++ {
		w, _ := z.Create("bin/worker")
		w.Write([]byte("x"))
	}
	z.Close()
	if err := unpack(b.Bytes(), "zip", t.TempDir(), "bin/worker"); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestZIPAndTARSuccess(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	data := []byte("inert-owned-bytes")
	tw.WriteHeader(&tar.Header{Name: "bin/worker", Mode: 0600, Size: int64(len(data))})
	tw.Write(data)
	tw.Close()
	gz.Close()
	for format, payload := range map[string][]byte{"zip": zipBytes(t, "bin/worker", 0600), "tar.gz": b.Bytes()} {
		t.Run(format, func(t *testing.T) {
			req, client, _, _ := fixture(t, payload, format)
			if _, err := Acquire(context.Background(), req, client); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestTARRejectsLinksAndSize(t *testing.T) {
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar} {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "bin/worker", Typeflag: kind, Linkname: "../outside"})
		tw.Close()
		gz.Close()
		if err := unpack(b.Bytes(), "tar.gz", t.TempDir(), "bin/worker"); err == nil {
			t.Fatal("special TAR member accepted")
		}
	}
	x := extraction{root: t.TempDir(), seen: map[string]bool{}}
	if err := x.write("large", false, MaxExpandedBytes+1, strings.NewReader("")); err == nil {
		t.Fatal("oversize accepted")
	}
}
func TestRootAndInstalledSymlinkRejected(t *testing.T) {
	req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(req.Root, req.PluginID)); err != nil {
		t.Skip(err)
	}
	if _, err := Acquire(context.Background(), req, client); err == nil {
		t.Fatal("installed symlink accepted")
	}
}
func TestDownloadBoundsAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("12345")) }))
	defer srv.Close()
	if _, err := download(context.Background(), srv.Client(), srv.URL, 4); err == nil {
		t.Fatal("oversize accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := download(ctx, srv.Client(), srv.URL, 10); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestOptionalPersistentBackup(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
		req.RefreshBackup = enabled
		got, err := Acquire(context.Background(), req, client)
		if err != nil || got.BackupWarning != "" {
			t.Fatalf("%v %+v", err, got)
		}
		_, err = os.Stat(filepath.Join(req.Root, ".backups", req.PluginID, "bin", "worker"))
		if enabled && err != nil {
			t.Fatal(err)
		}
		if !enabled && !os.IsNotExist(err) {
			t.Fatalf("unexpected backup: %v", err)
		}
	}
	req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	req.RefreshBackup = true
	if err := os.WriteFile(filepath.Join(req.Root, ".backups"), []byte("owned obstacle"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Acquire(context.Background(), req, client)
	if err != nil || got.BackupWarning == "" {
		t.Fatalf("must succeed with warning: %v %+v", err, got)
	}
}
