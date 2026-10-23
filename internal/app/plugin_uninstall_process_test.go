package app

import (
	"context"
	"fmt"
	"io"
	"light-media/internal/plugins"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var uninstallSDKBinary struct {
	once sync.Once
	path string
	err  error
}

func useUninstallSDKProcess(t *testing.T, a *App) {
	t.Helper()
	uninstallSDKBinary.once.Do(func() {
		uninstallSDKBinary.path, uninstallSDKBinary.err = buildPluginProcessFixture(false, "uninstall-sdk", "./internal/app/testdata/uninstall-worker")
	})
	if uninstallSDKBinary.err != nil {
		t.Fatal(uninstallSDKBinary.err)
	}
	useUninstallExecutable(t, a, uninstallSDKBinary.path)
}
func useUninstallServerProcess(t *testing.T, a *App) {
	t.Helper()
	transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	useUninstallExecutable(t, a, transferServerBinary.path)
}
func useUninstallExecutable(t *testing.T, a *App, executable string) {
	t.Helper()
	a.pluginCommand = func(id string) (plugins.Command, error) {
		if id != "PluginUnInstall" {
			return plugins.Command{}, fmt.Errorf("Uninstall fixture rejects plugin %q", id)
		}
		return plugins.Command{Path: executable, Args: []string{"--plugin-worker", id}}, nil
	}
}
func uninstallOwnedAPIRequests(t *testing.T, a *App, cookie *http.Cookie) func(string, string, string) *httptest.ResponseRecorder {
	t.Helper()
	api := httptest.NewServer(a.Handler())
	t.Cleanup(api.Close)
	address := api.Listener.Addr().String()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if network != "tcp" || target != address {
			return nil, fmt.Errorf("owned Uninstall API dial rejected")
		}
		return dialer.DialContext(ctx, network, target)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("owned Uninstall API redirect rejected") }}
	return func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req, err := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil || len(raw) > 2<<20 {
			t.Fatal("owned response unreadable or oversized", err)
		}
		out := httptest.NewRecorder()
		out.HeaderMap = response.Header.Clone()
		out.WriteHeader(response.StatusCode)
		out.Body.Write(raw)
		return out
	}
}
