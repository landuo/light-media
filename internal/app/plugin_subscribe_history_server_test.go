package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Ordinary production worker plus real loopback HTTP. Under -race only the App
// host is instrumented; this does not replace the retained race-child gate.
func TestSubscribeGroupLargeHistoryActualServerHTTP(t *testing.T) {
	transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	useOrdinary := func(a *App) {
		a.pluginCommand = func(id string) (plugins.Command, error) {
			if id != "SubscribeGroup" {
				return plugins.Command{}, fmt.Errorf("unexpected worker")
			}
			return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
		}
	}

	a := newTestApp(t)
	ctx := context.Background()
	useOrdinary(a)
	cookie := setupTestAdmin(t, a)
	server := httptest.NewServer(a.Handler())
	defer func() { server.Close() }()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req, e := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		began := time.Now()
		response, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("owned actual HTTP %s: status=%d bytes=%d elapsed=%s", path, response.StatusCode, len(raw), time.Since(began))
		out := httptest.NewRecorder()
		out.Code = response.StatusCode
		out.Body = bytes.NewBuffer(raw)
		return out
	}

	if response := request("POST", "/api/plugins/install", `{"plugin_id":"SubscribeGroup"}`); response.Code != 200 {
		t.Fatal("fixture install failed", response.Code)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	subscribeSDKExecute(t, a, run)
	var revision int64
	for _, test := range []struct{ name, entry string }{
		{"small_input_large_page", `{}`},
		{"large_input_large_page", `{"name":"synthetic-full-history","content":"` + strings.Repeat("x", 320) + `","type":"synthetic","time":"2026-10-16"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage("[" + strings.Repeat(test.entry+",", 4499) + test.entry + "]")
			if (test.name == "small_input_large_page") != (len(raw) < 1<<20) {
				t.Fatal("fixture does not distinguish input versus expanded output limits")
			}
			stored, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history", revision, raw)
			if err != nil {
				t.Fatal(err)
			}
			revision = stored.Revision
			response := request("GET", "/api/plugins/SubscribeGroup/page", "")
			if response.Code != 200 || response.Body.Len() <= 1<<20 {
				t.Fatal("full expanded history did not cross the real worker/HTTP boundary", response.Code, response.Body.Len(), response.Body.String())
			}
			var envelope any
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal("large history response was incomplete JSON", err)
			}
			rows, cells := 0, 0
			var visit func(any)
			visit = func(value any) {
				switch item := value.(type) {
				case map[string]any:
					if item["component"] == "tr" {
						rows++
					}
					if item["component"] == "td" {
						cells++
					}
					for _, child := range item {
						visit(child)
					}
				case []any:
					for _, child := range item {
						visit(child)
					}
				}
			}
			visit(envelope)
			if rows != 4500 || cells != 18000 {
				t.Fatal("history rows were truncated or duplicated", rows, cells)
			}
			kept, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
			if err != nil || kept.Revision != stored.Revision || !bytes.Equal(kept.Value, stored.Value) {
				t.Fatal("read-only large page changed private history", err)
			}
		})
	}
	// The actual application must reopen and load the complete private history,
	// not just succeed while the previous worker/configuration remains cached.
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	useOrdinary(reopened)
	server.Close()
	server = httptest.NewServer(reopened.Handler())
	response := request("GET", "/api/plugins/SubscribeGroup/page", "")
	if response.Code != 200 || strings.Count(response.Body.String(), `"component":"tr"`) != 4500 {
		t.Fatal("large complete page failed after actual application reopen", response.Code, response.Body.Len(), response.Body.String())
	}
}
