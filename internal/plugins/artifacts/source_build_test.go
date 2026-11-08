package artifacts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestSourceBuildCompilesOnlySelectedPackageAndStagesResources(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("requires Go toolchain")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("1", 40)
	prefix := "Repo-" + commit + "/"
	files := map[string][]byte{
		prefix + "LICENSE":                 []byte("fixture license"),
		prefix + "module/go.mod":           []byte("module example.invalid/sourcefixture\n\ngo 1.22\n"),
		prefix + "module/cmd/one/main.go":  []byte("package main\nfunc init() { panic(\"the build must never execute this worker\") }; func main() {}\n"),
		prefix + "module/cmd/other/bad.go": []byte("invalid package must not be built"),
		prefix + "frontend/remoteEntry.js": []byte("export const init = () => {};"),
		prefix + "frontend/parts/chunk.js": []byte("export default {};"),
	}
	archive := frontendArchive(t, "zip", files)
	client := &http.Client{Transport: repositoryReplayTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://codeload.github.com/Owner/Repo/zip/"+commit || req.Header.Get("Authorization") != "" {
			t.Fatalf("source acquisition requested unexpected URL %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive))}, nil
	})}
	source := SourceBuild{Commit: commit, ModuleDir: "module", Package: "./cmd/one",
		FrontendDir: "frontend", LicenseFiles: []string{"LICENSE"}, HostContract: "owned/v1"}
	manifest := plugins.Manifest{
		SchemaVersion: 1, PluginID: "Owned", Name: "Owned source build", Version: "1.0",
		Runtime: "go-executable", Entry: "bin/worker", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"stop"}, Capabilities: []string{},
		Frontend: &plugins.Frontend{
			Contract: plugins.FrontendFederationContract, APIContract: plugins.FrontendAPIContract,
			Root: "frontend/assets", Entry: "remoteEntry.js", Exposes: []string{"Config"}, Shared: []string{"vue"},
		},
	}
	workerHashes := map[string]bool{}
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		t.Run(platform, func(t *testing.T) {
			stage, err := os.MkdirTemp(root, ".prepare-")
			if err != nil {
				t.Fatal(err)
			}
			req := Request{RepositoryURL: "https://github.com/Owner/Repo", Platform: platform}
			result, err := prepareSourceBuild(context.Background(), req, Release{Manifest: manifest}, source, stage, client)
			if err != nil {
				t.Fatal(err)
			}
			if result.Platform != platform || result.HostContract != source.HostContract || !validDigest(result.ExecutableSHA256) ||
				workerHashes[result.ExecutableSHA256] || len(result.Files) != 4 {
				t.Fatalf("invalid compiled package inventory: %+v", result)
			}
			workerHashes[result.ExecutableSHA256] = true
			if err = verifyPackageTree(stage, result.Files, false); err != nil {
				t.Fatal(err)
			}
			for src, dst := range map[string]string{
				"LICENSE": "LICENSE", "frontend/remoteEntry.js": "frontend/assets/remoteEntry.js",
				"frontend/parts/chunk.js": "frontend/assets/parts/chunk.js",
			} {
				actual, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(dst)))
				if err != nil || !bytes.Equal(actual, files[prefix+src]) {
					t.Fatalf("incorrect resource %s: %v", dst, err)
				}
			}
		})
	}
	// A source index cannot redirect the compiler to read a host module.
	files[prefix+"module/go.mod"] = []byte("module example.invalid/sourcefixture\n\ngo 1.22\nreplace example.invalid/foreign => /host/config\n")
	archive = frontendArchive(t, "zip", files)
	stage, err := os.MkdirTemp(root, ".prepare-")
	if err != nil {
		t.Fatal(err)
	}
	req := Request{RepositoryURL: "https://github.com/Owner/Repo", Platform: "linux-amd64"}
	_, err = prepareSourceBuild(context.Background(), req, Release{Manifest: manifest}, source, stage, client)
	if err == nil || !strings.Contains(err.Error(), "replace directives") {
		t.Fatalf("outside module replacement was not rejected: %v", err)
	}
	if entries, err := os.ReadDir(stage); err != nil || len(entries) != 0 {
		t.Fatalf("unsafe source reached package staging: %v %v", entries, err)
	}
}

func TestSourceBuildRejectsUnpinnedOrEscapingInputs(t *testing.T) {
	source := SourceBuild{Commit: strings.Repeat("a", 40), Package: "./cmd/worker", LicenseFiles: []string{"LICENSE"}, HostContract: "owned/v1"}
	for _, tc := range []struct {
		name, repository string
		change           func(*SourceBuild)
	}{
		{"wrong host", "https://example.invalid/index.json", nil},
		{"moving ref", "https://github.com/owner/repo", func(s *SourceBuild) { s.Commit = "main" }},
		{"path traversal", "https://github.com/owner/repo", func(s *SourceBuild) { s.ModuleDir = "../.." }},
		{"all packages", "https://github.com/owner/repo", func(s *SourceBuild) { s.Package = "./..." }},
		{"compiler option", "https://github.com/owner/repo", func(s *SourceBuild) { s.Package = "-toolexec=sh" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := source
			if tc.change != nil {
				tc.change(&candidate)
			}
			if err := validateSourceBuild(candidate, tc.repository, "linux-amd64"); err == nil {
				t.Fatal("unsafe source declaration accepted")
			}
		})
	}
	if _, err := unpackSourceBuild(frontendArchive(t, "zip", map[string][]byte{
		"Repo-" + source.Commit + "/../../escape": []byte("invalid"),
	}), t.TempDir(), "Repo", source.Commit); err == nil {
		t.Fatal("source archive traversal accepted")
	}
}
