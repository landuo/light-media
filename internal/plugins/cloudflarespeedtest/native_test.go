package cloudflarespeedtest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func approved(context.Context) error { return nil }
func files(t *testing.T) *OwnedFiles {
	t.Helper()
	f, err := NewOwnedFiles(filepath.Join(t.TempDir(), "new-instance"), "synthetic-test-owner")
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func request() InstallRequest {
	return InstallRequest{URL: ReleasePrefix + "/v2.3.0/cfst_linux_amd64.tar.gz", Archive: "cfst_linux_amd64.tar.gz", Version: "v2.3.0", Platform: Platform{"linux", "x86_64"}}
}
func tarball(t *testing.T, name string, kind byte, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	h := &tar.Header{Name: name, Mode: 0700, Size: int64(len(content)), Typeflag: kind}
	if kind == tar.TypeSymlink {
		h.Size = 0
		h.Linkname = "/outside"
		content = nil
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNativeStorageNeverAdoptsOldDataOrEscapes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cfst"), []byte("untrusted old binary"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewOwnedFiles(root, "fresh"); !errors.Is(err, ErrStorageScope) {
		t.Fatal(err)
	}
	f := files(t)
	if _, err := NewOwnedFiles(f.root, "different"); !errors.Is(err, ErrStorageScope) {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside", "/outside", ".cloudflare-owner", "a/b", "a\\b", ".."} {
		if err := f.write(ctx, name, []byte("bad"), 0600); !errors.Is(err, ErrStorageScope) {
			t.Fatal(name, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.root, "cfst")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Exists(ctx, FileRef{Name: "cfst"}, Platform{"linux", "x86_64"}); !errors.Is(err, ErrStorageScope) {
		t.Fatal(err)
	}
	if err := f.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatal(string(data), err)
	}
	if err := f.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNativeArchivePinAndTraversal(t *testing.T) {
	ctx := context.Background()
	req := request()
	f := files(t)
	data := tarball(t, "cfst", tar.TypeReg, []byte("new synthetic binary"))
	r := &NativeRuntime{Files: f, Target: req.Platform, Guard: approved}
	if err := f.write(ctx, req.Archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Extract(ctx, req); !errors.Is(err, ErrArtifactUnverified) {
		t.Fatal(err)
	}
	r.Pins = Pins{Archives: map[string][32]byte{req.URL: sha256.Sum256(data)}}
	if err := r.Extract(ctx, req); err != nil {
		t.Fatal(err)
	}
	got, err := f.read(ctx, "cfst", 100)
	if err != nil || string(got) != "new synthetic binary" {
		t.Fatal(string(got), err)
	}
	for _, name := range []string{"../outside", "/outside", "nested/cfst"} {
		bad := tarball(t, name, tar.TypeReg, []byte("bad"))
		if err := extractArchive(ctx, f, req, bad); !errors.Is(err, ErrStorageScope) {
			t.Fatal(name, err)
		}
	}
	bad := tarball(t, "cfst", tar.TypeSymlink, nil)
	if err := extractArchive(ctx, f, req, bad); !errors.Is(err, ErrStorageScope) {
		t.Fatal(err)
	}
	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	header := &zip.FileHeader{Name: "cfst.exe", Method: zip.Deflate}
	header.SetMode(0700)
	entry, _ := zw.CreateHeader(header)
	_, _ = entry.Write([]byte("windows fixture"))
	_ = zw.Close()
	req.Archive = "cfst_windows_amd64.zip"
	if err := extractArchive(ctx, f, req, z.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Exists(ctx, FileRef{Name: "cfst.exe", Backslash: true}, Platform{"linux", "x86_64"}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCompletedArchiveFormatFailureUsesSourcePlatformBranch(t *testing.T) {
	ctx := context.Background()
	f := files(t)
	req := request()
	data := []byte("synthetic truncated archive")
	if err := f.write(ctx, req.Archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	r := &NativeRuntime{Files: f, Target: req.Platform, Guard: approved, Pins: Pins{Archives: map[string][32]byte{req.URL: sha256.Sum256(data)}}}
	if err := r.Extract(ctx, req); err != nil {
		t.Fatal("Unix os.system status was not ignored", err)
	}
	req.Platform.OS = "windows"
	req.Archive = "cfst_windows_amd64.zip"
	req.URL = ReleasePrefix + "/v2.3.0/" + req.Archive
	if err := f.write(ctx, req.Archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	r.Pins.Archives[req.URL] = sha256.Sum256(data)
	var extraction *ExtractionError
	if err := r.Extract(ctx, req); !errors.As(err, &extraction) {
		t.Fatal("Windows ZipFile failure was swallowed", err)
	}
}

func TestNativeHTTPSourceRetryAndReleaseErrors(t *testing.T) {
	ctx := context.Background()
	var urls []string
	direct := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		return response(503, "down"), nil
	})}
	proxy := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		return response(200, `{"tag_name":"v2.3.0"}`), nil
	})}
	h := &HTTPAdapter{Direct: direct, Proxied: proxy}
	a, err := h.Latest(ctx, false)
	if err != nil || a.Available {
		t.Fatal(a, err)
	}
	a, err = h.Latest(ctx, true)
	if err != nil || !a.Available || a.Tag != "v2.3.0" || !reflect.DeepEqual(urls, []string{ReleaseAPI, ReleaseAPI}) {
		t.Fatal(a, err, urls)
	}
	for _, tt := range []struct{ body, class string }{{`not-json`, "JSONDecodeError"}, {`{}`, "KeyError"}, {`[]`, "TypeError"}, {`{"tag_name":null} true`, "JSONDecodeError"}} {
		t.Run(tt.class+tt.body, func(t *testing.T) {
			h.Direct = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(200, tt.body), nil })}
			_, err := h.Latest(ctx, false)
			if ErrorClass(err) != tt.class {
				t.Fatal(err)
			}
		})
	}
	h.Direct = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("network failed") })}
	a, err = h.Latest(ctx, false)
	if err != nil || a.Available {
		t.Fatal(a, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := h.Latest(cancelled, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeDownloadPinnedAndGuarded(t *testing.T) {
	ctx := context.Background()
	f := files(t)
	req := request()
	archive := tarball(t, "cfst", tar.TypeReg, []byte("new only"))
	var requested []string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		return response(200, string(archive)), nil
	})}
	h := &HTTPAdapter{Direct: client, Proxied: client}
	r := &NativeRuntime{Files: f, HTTP: h, Guard: approved, Target: req.Platform}
	if err := r.Download(ctx, req); !errors.Is(err, ErrArtifactUnverified) || len(requested) != 0 {
		t.Fatal(err, requested)
	}
	r.Pins = Pins{Archives: map[string][32]byte{req.URL: sha256.Sum256(archive)}}
	if err := r.Download(ctx, req); err != nil {
		t.Fatal(err)
	}
	if requested[0] != "https://ghfast.top/"+req.URL {
		t.Fatal(requested)
	}
	h.HTTPSProxy = true
	if err := r.Download(ctx, req); err != nil {
		t.Fatal(err)
	}
	if requested[1] != req.URL {
		t.Fatal(requested)
	}
	r.Pins.Archives[req.URL] = [32]byte{}
	if err := r.Download(ctx, req); !errors.Is(err, ErrArtifactUnverified) {
		t.Fatal(err)
	}
	r.Guard = func(context.Context) error { return errors.New("retired") }
	before := len(requested)
	if err := r.Download(ctx, req); err == nil || len(requested) != before {
		t.Fatal(err, requested)
	}
	bad := req
	bad.URL = ReleasePrefix + "/../../config/" + bad.Archive
	if releaseURL(bad) {
		t.Fatal("invalid URL accepted")
	}
	c, _ := h.client(false)
	redirect, _ := http.NewRequest("GET", "https://unrelated.example/file", nil)
	if err := c.CheckRedirect(redirect, []*http.Request{{}}); err == nil {
		t.Fatal("unrelated redirect accepted")
	}
}

func TestCLIHelperProcess(t *testing.T) {
	index := -1
	for i, s := range os.Args {
		if s == "child-cfst-owned-fixture" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	mode := os.Args[index+1]
	if mode == "wait" {
		_ = os.WriteFile("started", []byte("started"), 0600)
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
	if mode == "success" || mode == "exit7" {
		_ = os.WriteFile("result_hosts.txt", []byte("IP,x\n2.2.2.2,1\n"), 0600)
		if mode == "exit7" {
			os.Exit(7)
		}
		os.Exit(0)
	}
	os.Exit(2)
}
func TestNativeCLIActualOwnedFixture(t *testing.T) {
	f := files(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = f.write(ctx, "cfst", binary, 0700); err != nil {
		t.Fatal(err)
	}
	cli := &NativeCLI{Files: f, Guard: approved}
	spec := RunSpec{Binary: "cfst", Result: "result_hosts.txt", Args: []string{"-test.run=TestCLIHelperProcess", "--", "child-cfst-owned-fixture", "success"}}
	if _, err := cli.Run(ctx, spec); !errors.Is(err, ErrArtifactUnverified) {
		t.Fatal(err)
	}
	cli.Pins = Pins{Binaries: map[string][][32]byte{"cfst": {sha256.Sum256(binary)}}}
	result, err := cli.Run(ctx, spec)
	if err != nil || !result.Completed || result.ExitCode != 0 {
		t.Fatal(result, err)
	}
	csv, err := f.ReadResult(ctx)
	if err != nil || BestIP(csv, false) != "2.2.2.2" {
		t.Fatal(string(csv), err)
	}
	spec.Args[len(spec.Args)-1] = "exit7"
	result, err = cli.Run(ctx, spec)
	if err != nil || !result.Completed || result.ExitCode != 7 {
		t.Fatal(result, err)
	}
	spec.Args[len(spec.Args)-1] = "wait"
	limited, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	result, err = cli.Run(limited, spec)
	var failure *CLIError
	if !errors.As(err, &failure) || !failure.Dispatched || result.Completed {
		t.Fatal(result, err)
	}
	if err = f.Remove(ctx, "result_hosts.txt"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	cli.Guard = func(context.Context) error {
		calls++
		if calls == 2 {
			return errors.New("retired")
		}
		return nil
	}
	if _, err = cli.Run(ctx, spec); err == nil {
		t.Fatal("retired stage dispatched")
	}
	if ok, _ := f.Exists(ctx, FileRef{Name: "result_hosts.txt"}, Platform{"linux", "x86_64"}); ok {
		t.Fatal("unexpected result")
	}
}
