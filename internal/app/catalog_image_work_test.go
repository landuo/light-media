package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestImageSlotsIsolateProviderAndCancelQueue(t *testing.T) {
	a := &App{}
	defer a.closeImageWork()
	for i := 0; i < 2; i++ {
		release, err := a.acquireImageSlot(context.Background(), "slow")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := a.acquireImageSlot(ctx, "slow"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked queue: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	release, err := a.acquireImageSlot(ctx2, "fast")
	if err != nil {
		t.Fatalf("slow provider blocked fast one: %v", err)
	}
	release()
}
func TestImageDuplicateMissesShareDownloadAndWaiterCancels(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Dir: dir}
	defer a.closeImageWork()
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	old := assetImageClient
	defer func() { assetImageClient = old }()
	entered := make(chan struct{})
	finish := make(chan struct{})
	var calls atomic.Int32
	assetImageClient = &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-finish:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(bytes.NewReader(data.Bytes()))}, nil
	})}
	run := func(ctx context.Context) error {
		return a.catalogImage(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/catalog/image?path=%2Fduplicate.jpg", nil).WithContext(ctx))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 9)
	wg.Add(1)
	go func() { defer wg.Done(); errs <- run(context.Background()) }()
	select {
	case <-entered:
	case err := <-errs:
		t.Fatalf("request failed before transport: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach transport")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter cancellation: %v", err)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- run(context.Background()) }()
	}
	close(finish)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate downloads: %d", calls.Load())
	}
}
func TestMetadataAPITransportReusesPoolAndReplacesConfig(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	a := &App{}
	defer a.closeMetadataAPITransportLocked()
	first, err := a.metadataAPITransportForConfig()
	if err != nil {
		t.Fatal(err)
	}
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	for i := 0; i < 3; i++ {
		transport, err := a.metadataAPITransportForConfig()
		if err != nil {
			t.Fatal(err)
		}
		response, err := (&http.Client{Transport: transport}).Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	if connections.Load() != 1 {
		t.Fatalf("pool created %d TCP connections", connections.Load())
	}
	second, err := a.metadataAPITransportForConfig()
	if err != nil || first != second {
		t.Fatal("pool not reused", err)
	}
	a.config.ProxyHost = "http://127.0.0.1:12345"
	third, err := a.metadataAPITransportForConfig()
	if err != nil || third == first {
		t.Fatal("route change not applied", err)
	}
}
func TestBrowserProtectionSnapshotIsImmutableAndMutationRefreshes(t *testing.T) {
	a := &App{Dir: "/app-data"}
	a.config.AllowedRoots = []string{"/media"}
	a.config.Roots = []Root{{Path: "/library"}}
	a.config.Downloaders = []DownloaderConfig{{DownloadPath: "/downloads", Mappings: []PathMapping{{From: "/downloads", To: "/mounted"}}}}
	snapshot := a.browserProtectionSnapshot()
	for _, p := range []string{"/app-data/x", "/app-data", "/media", "/library", "/library/movie", "/downloads/x", "/mounted/x", "/other"} {
		if got, want := snapshot.reason(p), legacyProtectionForTest(a, p); got != want {
			t.Fatalf("%s: %q != %q", p, got, want)
		}
	}
	a.config.Downloaders[0].DownloadPath = "/new-downloads"
	if snapshot.reason("/downloads/x") == "" || a.browserStaticProtection("/downloads/x") != "" {
		t.Fatal("snapshot or fresh mutation view incorrect")
	}
	if a.browserStaticProtection("/new-downloads/x") == "" {
		t.Fatal("new protection missing")
	}
}

// Independent pre-optimization implementation guards ordering and overlap semantics.
func legacyProtectionForTest(a *App, path string) string {
	if within(a.Dir, path) || within(path, a.Dir) {
		return "应用数据目录受保护"
	}
	for _, root := range a.Settings().Roots {
		if mapped, overlap := sharedDirectoryPath(root.Path, path); overlap && within(mapped, root.Path) {
			return "媒体库根目录不能改名或删除"
		}
	}
	for _, root := range a.browserRoots() {
		if path == root.Path {
			return "允许根目录不能改名或删除"
		}
	}
	for _, d := range a.Settings().Downloaders {
		paths := []string{d.DownloadPath}
		for _, m := range d.Mappings {
			if within(m.From, d.DownloadPath) {
				rel, _ := filepath.Rel(m.From, d.DownloadPath)
				paths = append(paths, filepath.Join(m.To, rel))
			}
		}
		for _, p := range paths {
			if importPath(p) && (within(p, path) || within(path, p)) {
				return "下载器目录受保护，防止破坏下载与做种"
			}
		}
	}
	return ""
}
func TestImageBackgroundPruneFlushesLimitsOnClose(t *testing.T) {
	a := &App{}
	dir := t.TempDir()
	for i := 0; i < 1030; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.img", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		a.scheduleCatalogImagePrune(dir)
	}
	a.closeImageWork()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1024 {
		t.Fatalf("cache entry limit: %d", len(entries))
	}
	a.scheduleCatalogImagePrune(dir) // Closed manager must not start work.
}
func BenchmarkBrowserProtectionSnapshot(b *testing.B) {
	a := &App{Dir: "/app-data"}
	a.config.AllowedRoots = []string{"/media"}
	a.config.Downloaders = []DownloaderConfig{{DownloadPath: "/downloads"}}
	b.Run("legacy-per-entry", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			legacyProtectionForTest(a, "/media/movie.mkv")
		}
	})
	b.Run("snapshot-per-entry", func(b *testing.B) {
		p := a.browserProtectionSnapshot()
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			p.reason("/media/movie.mkv")
		}
	})
}
