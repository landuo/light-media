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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"light-media/internal/plugins"
)

func frontendFixtureFiles() map[string][]byte {
	return map[string][]byte{
		"bin/worker":                       []byte("inert-owned-bytes"),
		"frontend/assets/remoteEntry.js":   []byte("export const init=()=>{}; export const get=()=>import('./chunk.js');"),
		"frontend/assets/chunk.js":         []byte("export default {name:'OwnedVueFixture'};"),
		"frontend/assets/style.css":        []byte(".owned{color:purple}"),
		"frontend/assets/fonts/test.woff2": []byte("owned-font-bytes"),
		"private.txt":                      []byte("not a public frontend file"),
	}
}

func frontendArchive(t *testing.T, format string, files map[string][]byte) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var output bytes.Buffer
	if format == "zip" {
		writer := zip.NewWriter(&output)
		for _, name := range names {
			file, err := writer.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = file.Write(files[name]); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&output)
		writer := tar.NewWriter(compressed)
		for _, name := range names {
			if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(files[name]); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return output.Bytes()
}

func frontendFixture(t *testing.T, format string) (Request, *http.Client, *Index, map[string][]byte) {
	t.Helper()
	files := frontendFixtureFiles()
	req, client, index, _ := fixture(t, frontendArchive(t, format, files), format)
	m := &index.Releases[0].Manifest
	m.Frontend = &plugins.Frontend{Contract: plugins.FrontendFederationContract, Root: "frontend/assets", Entry: "remoteEntry.js", Exposes: []string{"Config", "Page", "Dashboard", "AppPage"}, Shared: []string{"vue"}, APIContract: plugins.FrontendAPIContract}
	for name, data := range files {
		index.Releases[0].Artifacts[0].Files = append(index.Releases[0].Artifacts[0].Files, PackageFile{Path: name, Size: int64(len(data)), SHA256: digest(data)})
	}
	return req, client, index, files
}

func TestFrontendPackageAcquireReadAndClone(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			req, client, _, files := frontendFixture(t, format)
			original, err := Acquire(context.Background(), req, client)
			if err != nil {
				t.Fatal(err)
			}
			if !validDigest(original.PackageSHA256) || len(original.Files) != len(files) {
				t.Fatal("no complete package identity")
			}
			reopened, err := ReadInstalled(req.Root, req.PluginID, req.Validate)
			if err != nil || reopened.PackageSHA256 != original.PackageSHA256 {
				t.Fatal("restart changed content identity", err)
			}
			clone, err := CloneInstalled(req.Root, req.PluginID, "OwnedClone", req.Validate)
			if err != nil || clone.PackageSHA256 != original.PackageSHA256 {
				t.Fatal("clone changed package identity", err)
			}
			for name, data := range files {
				one, err := os.Stat(filepath.Join(original.Command.Dir, filepath.FromSlash(name)))
				if err != nil {
					t.Fatal(err)
				}
				two, err := os.Stat(filepath.Join(clone.Command.Dir, filepath.FromSlash(name)))
				if err != nil || os.SameFile(one, two) {
					t.Fatal("clone aliases source", name, err)
				}
				if strings.HasPrefix(name, "frontend/assets/") {
					resource, err := ReadFrontendAsset(req.Root, "OwnedClone", clone.PackageSHA256, strings.TrimPrefix(name, "frontend/assets/"), req.Validate)
					if err != nil || !bytes.Equal(resource.Data, data) || resource.SHA256 != digest(data) || resource.ContentType == "" {
						t.Fatal("asset mismatch", name, err)
					}
				}
			}
			if err := os.WriteFile(filepath.Join(original.Command.Dir, "frontend/assets/chunk.js"), []byte("tampered-source"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadInstalled(req.Root, req.PluginID, req.Validate); err == nil {
				t.Fatal("tampered source accepted")
			}
			if _, err := ReadFrontendAsset(req.Root, "OwnedClone", clone.PackageSHA256, "chunk.js", req.Validate); err != nil {
				t.Fatal("source mutation leaked into clone", err)
			}
		})
	}
}

func TestFrontendPackageMetadataAndContentRejections(t *testing.T) {
	for _, change := range []string{"missing_inventory", "bad_digest", "bad_size", "missing_member", "extra_member", "duplicate", "case_collision", "reserved", "traversal", "front_worker", "wrong_format", "unsupported_contract"} {
		t.Run(change, func(t *testing.T) {
			req, client, index, _ := frontendFixture(t, "zip")
			artifact := &index.Releases[0].Artifacts[0]
			switch change {
			case "missing_inventory":
				artifact.Files = nil
			case "bad_digest":
				artifact.Files[0].SHA256 = strings.Repeat("0", 64)
			case "bad_size":
				artifact.Files[0].Size++
			case "missing_member":
				artifact.Files = artifact.Files[1:]
			case "extra_member":
				artifact.Files = append(artifact.Files, PackageFile{Path: "extra.js", Size: 1, SHA256: digest([]byte("x"))})
			case "duplicate":
				artifact.Files = append(artifact.Files, artifact.Files[0])
			case "case_collision":
				artifact.Files = append(artifact.Files, PackageFile{Path: "Frontend/assets/extra.js", Size: 1, SHA256: digest([]byte("x"))})
			case "reserved":
				artifact.Files[0].Path = ".acquired.json"
			case "traversal":
				artifact.Files[0].Path = "frontend/../outside.js"
			case "front_worker":
				index.Releases[0].Manifest.Frontend.Root = "bin"
			case "wrong_format":
				artifact.Format = "executable"
			case "unsupported_contract":
				index.Releases[0].Manifest.Frontend.Contract = "arbitrary/v1"
			}
			oldTree(t, req)
			if _, err := Acquire(context.Background(), req, client); err == nil {
				t.Fatal("invalid frontend package accepted")
			}
			if _, err := os.Stat(filepath.Join(req.Root, req.PluginID, "old")); err != nil {
				t.Fatal("failure did not preserve/restore previous tree", err)
			}
		})
	}
}

func TestFrontendInstalledTamperAndPathRejections(t *testing.T) {
	for _, change := range []string{"chunk", "css", "font", "extra", "missing", "symlink", "receipt_pin", "receipt_inventory"} {
		t.Run(change, func(t *testing.T) {
			req, client, _, _ := frontendFixture(t, "zip")
			installed, err := Acquire(context.Background(), req, client)
			if err != nil {
				t.Fatal(err)
			}
			root := installed.Command.Dir
			switch change {
			case "chunk", "css", "font":
				name := map[string]string{"chunk": "chunk.js", "css": "style.css", "font": "fonts/test.woff2"}[change]
				p := filepath.Join(root, "frontend/assets", name)
				b, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				b[0] ^= 1
				err = os.WriteFile(p, b, 0600)
			case "extra":
				err = os.WriteFile(filepath.Join(root, "frontend/assets/extra.js"), []byte("extra"), 0600)
			case "missing":
				err = os.Remove(filepath.Join(root, "frontend/assets/style.css"))
			case "symlink":
				p := filepath.Join(root, "frontend/assets/chunk.js")
				if err = os.Remove(p); err == nil {
					err = os.Symlink("remoteEntry.js", p)
				}
			case "receipt_pin", "receipt_inventory":
				p := filepath.Join(root, ".acquired.json")
				b, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				var receipt installedReceipt
				if e = json.Unmarshal(b, &receipt); e != nil {
					t.Fatal(e)
				}
				if change == "receipt_pin" {
					receipt.PackageSHA256 = strings.Repeat("0", 64)
				} else {
					receipt.Files = nil
				}
				b, e = json.Marshal(receipt)
				if e != nil {
					t.Fatal(e)
				}
				err = os.WriteFile(p, b, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadInstalled(req.Root, req.PluginID, req.Validate); err == nil {
				t.Fatal("installed tamper accepted")
			}
			if _, err := ReadFrontendAsset(req.Root, req.PluginID, installed.PackageSHA256, "remoteEntry.js", req.Validate); err == nil {
				t.Fatal("asset served from invalid graph")
			}
		})
	}
	req, client, _, _ := frontendFixture(t, "zip")
	installed, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"../bin/worker", ".acquired.json", "../../private.txt", "//remoteEntry.js", "remoteEntry.js?x=1", "%2e%2e/bin/worker", "missing.js", "fonts/../chunk.js"} {
		if _, err := ReadFrontendAsset(req.Root, req.PluginID, installed.PackageSHA256, relative, req.Validate); err == nil {
			t.Fatal("unsafe asset path accepted", relative)
		}
	}
	if _, err := ReadFrontendAsset(req.Root, req.PluginID, strings.Repeat("0", 64), "remoteEntry.js", req.Validate); err == nil {
		t.Fatal("wrong immutable pin accepted")
	}
	if _, err := ReadFrontendAsset(req.Root, req.PluginID, installed.PackageSHA256, "remoteEntry.js", func(plugins.Manifest, string) error { return errors.New("host denies contract") }); err == nil {
		t.Fatal("manifest bypassed host authority")
	}
}

func TestFrontendContentIdentityIncludesFullGraphAndVersion(t *testing.T) {
	req, client, index, _ := frontendFixture(t, "zip")
	first, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(index.Releases[0].Artifacts[0].Files)
	second, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	if first.PackageSHA256 != second.PackageSHA256 {
		t.Fatal("inventory order changed content identity")
	}
	m := first.Manifest
	m.Version = "1.2.4"
	versionPin, err := packageDigest(m, first.HostContract, first.Files)
	if err != nil || versionPin == first.PackageSHA256 {
		t.Fatal("real version missing from content identity", err)
	}
	files := slices.Clone(first.Files)
	files[0].SHA256 = digest([]byte("different bytes"))
	changed, err := packageDigest(first.Manifest, first.HostContract, files)
	if err != nil || changed == first.PackageSHA256 {
		t.Fatal("same-version bytes missing from identity", err)
	}
}

func TestFrontendReadSerializedWithAcquireAndClone(t *testing.T) {
	req, client, _, files := frontendFixture(t, "zip")
	installed, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				resource, err := ReadFrontendAsset(req.Root, req.PluginID, installed.PackageSHA256, "chunk.js", req.Validate)
				if err != nil || !bytes.Equal(resource.Data, files["frontend/assets/chunk.js"]) {
					t.Errorf("concurrent read changed bytes: %v", err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			if _, err := Acquire(context.Background(), req, client); err != nil {
				t.Errorf("concurrent acquire: %v", err)
				return
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := CloneInstalled(req.Root, req.PluginID, "OwnedConcurrentClone", req.Validate); err != nil {
			t.Errorf("concurrent clone: %v", err)
		}
	}()
	wg.Wait()
	if _, err := ReadFrontendAsset(req.Root, "OwnedConcurrentClone", installed.PackageSHA256, "chunk.js", req.Validate); err != nil {
		t.Fatal(err)
	}
}
