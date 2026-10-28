package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestOriginalAPI35StaticFixedSourceOracle(t *testing.T) {
	testOriginalAPI35StaticOracle(t, "static-source-oracle.json", 8)
}
func TestOriginalAPI35StaticFilenameFixedSourceOracle(t *testing.T) {
	testOriginalAPI35StaticOracle(t, "static-filename-source-oracle.json", 6)
}
func testOriginalAPI35StaticOracle(t *testing.T, name string, count int) {
	var oracle struct {
		Files map[string]struct {
			Base64      string
			Pattern     []byte
			Repetitions int
		}
		Cases []struct {
			Name, Method, Path string
			Session            bool
			Status             int
			ContentType        *string `json:"content_type"`
			Disposition        *string `json:"content_disposition"`
			Allow              *string
			ContentRange       *string `json:"content_range"`
			Bytes              int
			HeaderBytes        string  `json:"header_bytes_base64"`
			Detail             *string `json:"detail"`
			SHA256             *string
		}
	}
	raw, err := os.ReadFile("../../testdata/standard-api/" + name)
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != count {
		t.Fatal("fixed static oracle unavailable", err)
	}
	a, cookie := originalAuthFrontendFixture(t)
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), "OwnedAuth", validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := os.ReadFile(installed.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{installed.Manifest.Entry: worker}
	for name, input := range oracle.Files {
		if input.Repetitions > 0 {
			files[name] = bytes.Repeat(input.Pattern, input.Repetitions)
		} else {
			files[name], err = base64.StdEncoding.DecodeString(input.Base64)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var compressed bytes.Buffer
	archive := zip.NewWriter(&compressed)
	inventory := []artifacts.PackageFile{}
	for _, name := range names {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(files[name]); err != nil {
			t.Fatal(err)
		}
		inventory = append(inventory, artifacts.PackageFile{Path: name, Size: int64(len(files[name])), SHA256: originalFrontendHash(files[name])})
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	payload := compressed.Bytes()
	index := artifacts.Index{SchemaVersion: 1, PluginID: "OwnedAuth", CurrentVersion: installed.Manifest.Version, Releases: []artifacts.Release{{Version: installed.Manifest.Version, Manifest: installed.Manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", Size: int64(len(payload)), SHA256: originalFrontendHash(payload), ExecutableSHA256: originalFrontendHash(worker), HostContract: installed.HostContract, Files: inventory}}}}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			_ = json.NewEncoder(w).Encode(index)
		} else if r.URL.Path == "/package.zip" {
			w.Write(payload)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/package.zip"
	body, _ := json.Marshal(map[string]any{"plugin_id": "OwnedAuth", "repo_url": repository.URL + "/index", "force": true})
	result := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	a.wg.Wait()
	result = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAuth", `{"enabled":true,"providers":[{"enabled":false}]}`, cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			pieces := strings.SplitN(sample.Path, "/", 2)
			path := "/api/v1/plugin/file/OwnedAuth/" + pieces[1]
			target := &url.URL{Path: path}
			r := httptest.NewRequest(sample.Method, target.EscapedPath(), nil)
			if sample.Session {
				r.AddCookie(cookie)
			}
			if sample.Name == "binary-range-is-full-stream" {
				r.Header.Set("Range", "bytes=0-1")
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != sample.Status {
				t.Fatal("fixed static status differs", w.Code, sample.Status)
			}
			for key, expected := range map[string]*string{"Content-Disposition": sample.Disposition, "Allow": sample.Allow, "Content-Range": sample.ContentRange} {
				want := ""
				if expected != nil {
					want = *expected
				}
				if key == "Content-Disposition" && sample.HeaderBytes != "" {
					expected, _ := base64.StdEncoding.DecodeString(sample.HeaderBytes)
					if !bytes.Equal([]byte(w.Header().Get(key)), expected) {
						t.Fatal("source Latin1 header wire bytes differ")
					}
					continue
				}
				if w.Header().Get(key) != want {
					t.Fatal("fixed static header differs", key, w.Header().Get(key), want)
				}
			}
			if sample.Detail != nil {
				var failure struct{ Detail string }
				if json.Unmarshal(w.Body.Bytes(), &failure) != nil || failure.Detail != *sample.Detail {
					t.Fatal("source filename header exception response differs")
				}
			}
			if sample.Status == 200 {
				if sample.ContentType == nil || w.Header().Get("Content-Type") != *sample.ContentType {
					t.Fatal("fixed static MIME differs", w.Header())
				}
				sum := sha256.Sum256(w.Body.Bytes())
				if w.Body.Len() != sample.Bytes || sample.SHA256 == nil || hex.EncodeToString(sum[:]) != *sample.SHA256 {
					t.Fatal("fixed static streamed bytes differ", w.Body.Len(), sample.Bytes)
				}
			}
		})
	}
}
