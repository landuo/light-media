package browserrender

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeOwnedStartupRetainsPairedLaunchBudget(t *testing.T) {
	config := ownedBrowser(t)
	oracle := os.Getenv("BROWSER_LAUNCH_SOURCE_ORACLE")
	if oracle == "" {
		oracle = "../../testdata/plugin-platform-final-20261104/browser-ocr/playwright-launch-timeout-source.json"
	}
	raw, err := os.ReadFile(oracle)
	var source struct {
		Selection []struct {
			Pattern string   `json:"pattern"`
			Matches []string `json:"matches"`
		} `json:"selection"`
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || !strings.Contains(string(raw), "DEFAULT_PLAYWRIGHT_LAUNCH_TIMEOUT = 3 * 60 * 1e3") {
		t.Fatal("paired 180s launch default source missing", err)
	}
	wrapper := filepath.Join(t.TempDir(), "owned-delayed-browser.sh")
	quoted := "'" + strings.ReplaceAll(config.Executable, "'", "'\"'\"'") + "'"
	if err = os.WriteFile(wrapper, []byte("#!/bin/sh\nsleep 21\nexec "+quoted+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config.Executable = wrapper
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body><script>document.body.dataset.delayed='owned';</script></body></html>`)
	}))
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	began := time.Now()
	html, err := config.Render(ctx, Request{URL: peer.URL, Headless: true, Timeout: 5 * time.Second})
	if err != nil || html == nil || !strings.Contains(*html, `data-delayed="owned"`) {
		t.Fatalf("source-allowed startup after 21s was rejected: %v elapsed=%s", err, time.Since(began))
	}
	if time.Since(began) < 21*time.Second {
		t.Fatal("real delayed launch was bypassed")
	}
	entries, err := os.ReadDir(config.TempDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("delayed launch profile remained", err)
	}
}
