package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"light-media/web"
)

// Opt-in real App/SQLite/SDK/production UI with only the fixed public status
// network boundary synthetic. It does not contact the official service.
func TestPluginServerStatusOwnedUIFixture(t *testing.T) {
	root := os.Getenv("LIGHT_MEDIA_SERVER_STATUS_UI")
	if root == "" {
		t.Skip("owned interactive UI fixture")
	}
	if !filepath.IsAbs(root) || filepath.Base(root) != "owned-ui-data" {
		t.Fatal("exclusive owned UI directory required")
	}
	a, err := New(Options{DataDir: root, Web: web.Handler()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	useBuiltInPluginProcess(t, a)
	f := &pluginStatusHostFixture{clock: 1700000000}
	a.pluginServerStatus.network, a.pluginServerStatus.clock = f, f.now
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	a.Start(ctx)
	listener, err := net.Listen("tcp", "127.0.0.1:43951")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
	case err := <-done:
		if err != http.ErrServerClosed {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Minute):
		t.Error("owned browser fixture expired")
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(closeCtx)
}
