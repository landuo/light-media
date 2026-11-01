package download

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/downloadercommands"
)

func trFinalPointer[T any](v T) *T { return &v }

// Owned legacy JSON-RPC only. No real daemon, forwarding, imported vendor,
// environment change, file effect, arbitrary RPC or hidden build.
func TestSharedTransmissionFinalWireAndWarning(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var selected []map[string]any
	var warnings []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/transmission/rpc" || r.URL.RawQuery != "" {
			t.Error("fixed route mismatch")
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "owned-user" || password != "owned-password" {
			t.Error("synthetic auth mismatch")
		}
		var frame map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&frame) != nil || d.Decode(new(any)) != io.EOF || len(frame) != 2 {
			t.Error("invalid exact legacy envelope")
			w.WriteHeader(400)
			return
		}
		method, _ := frame["method"].(string)
		args, ok := frame["arguments"].(map[string]any)
		if !ok {
			t.Error("missing arguments")
			return
		}
		methods = append(methods, method)
		if method == "session-get" {
			if len(args) != 0 {
				t.Error("session args changed")
			}
			if r.Header.Get("X-Transmission-Session-Id") == "" {
				w.Header().Set("X-Transmission-Session-Id", "owned-final-session")
				w.WriteHeader(409)
				return
			}
			io.WriteString(w, `{"result":"success","arguments":{"rpc-version":15,"version":"owned"}}`)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != "owned-final-session" {
			t.Error("session not reused")
		}
		if method != "torrent-set" && method != "torrent-stop" {
			t.Error("unexpected selected method", method)
		}
		selected = append(selected, args)
		io.WriteString(w, `{"result":"success","arguments":{}}`)
	}))
	defer peer.Close()
	_, port, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-final-tr", Kind: "tr", BaseURL: peer.URL + "/transmission/rpc", Username: "owned-user", Password: "owned-password"}, "", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	native := port.(*sharedTRCommands)
	ctx := context.Background()
	log := func(_ context.Context, level, message string) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, level+":"+message)
	}
	labels := []string{"old", "new"}
	if value, err := native.Change(ctx, downloadercommands.TRChangeCall{IDs: []any{json.Number("7"), false}, Labels: &labels, Log: log}); err != nil || value != nil {
		t.Fatal(value, err)
	}
	call := downloadercommands.TRChangeCall{IDs: "recently-active", UploadLimited: trFinalPointer(false), UploadLimit: trFinalPointer(int64(0)), DownloadLimited: trFinalPointer(true), DownloadLimit: trFinalPointer(int64(-2)), SeedRatioMode: trFinalPointer(int64(1)), SeedRatioLimit: float64(0), SeedIdleMode: trFinalPointer(int64(2)), SeedIdleLimit: trFinalPointer(int64(0))}
	if _, err := native.Change(ctx, call); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Change(ctx, downloadercommands.TRChangeCall{IDs: int64(7), SeedRatioLimit: float64(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Change(ctx, downloadercommands.TRChangeCall{IDs: int64(7), SeedRatioLimit: int64(0)}); err != nil {
		t.Fatal(err)
	}
	empty := []string{}
	if _, err := native.Change(ctx, downloadercommands.TRChangeCall{IDs: int64(7), Labels: &empty, Log: log}); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Stop(ctx, int64(7)); err != nil {
		t.Fatal(err)
	} // Old consumer shares unchanged session behavior.
	mu.Lock()
	defer mu.Unlock()
	wantMethods := []string{"session-get", "session-get", "torrent-set", "torrent-set", "torrent-set", "torrent-set", "torrent-set", "torrent-stop"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatal(methods)
	}
	wantWarnings := []string{"WARNING:Using feature not supported by server. RPC version for server 15, feature introduced in 16.", "WARNING:Using feature not supported by server. RPC version for server 15, feature introduced in 16."}
	if !reflect.DeepEqual(warnings, wantWarnings) {
		t.Fatal(warnings)
	}
	want := []map[string]any{
		{"ids": []any{json.Number("7"), false}, "labels": []any{"old", "new"}},
		{"ids": "recently-active", "uploadLimited": false, "uploadLimit": json.Number("0"), "downloadLimited": true, "downloadLimit": json.Number("-2"), "seedRatioMode": json.Number("1"), "seedRatioLimit": json.Number("0.0"), "seedIdleMode": json.Number("2"), "seedIdleLimit": json.Number("0")},
		{"ids": []any{json.Number("7")}, "seedRatioLimit": json.Number("1.0")},
		{"ids": []any{json.Number("7")}, "seedRatioLimit": json.Number("0")},
		{"ids": []any{json.Number("7")}, "labels": []any{}},
		{"ids": []any{json.Number("7")}},
	}
	if !reflect.DeepEqual(selected, want) {
		t.Fatalf("actual selected args %#v want %#v", selected, want)
	}
}

func TestSharedTransmissionFinalValidationAndUnknown(t *testing.T) {
	var hits, selected atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var frame struct {
			Method string `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&frame) != nil {
			t.Error("invalid frame")
		}
		if frame.Method == "session-get" {
			io.WriteString(w, `{"result":"success","arguments":{"rpc-version":17,"version":"owned"}}`)
			return
		}
		if frame.Method != "torrent-set" {
			t.Error("unexpected RPC", frame.Method)
		}
		selected.Add(1)
		w.WriteHeader(500)
	}))
	defer peer.Close()
	var denied atomic.Bool
	guardErr := errors.New("owned authority changed")
	_, port, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-final-errors", Kind: "tr", BaseURL: peer.URL}, "", func(ctx context.Context) error {
		if denied.Load() {
			return guardErr
		}
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	native := port.(*sharedTRCommands)
	ctx := context.Background()
	_, err = native.Change(ctx, downloadercommands.TRChangeCall{IDs: nil})
	source, ok := err.(*downloadercommands.SourceException)
	if !ok || source.Class != "ValueError" || source.Message != "No arguments to set" || hits.Load() != 0 {
		t.Fatal("empty fields must fail before selector/HTTP", err, hits.Load())
	}
	_, err = native.Change(ctx, downloadercommands.TRChangeCall{IDs: nil, UploadLimit: trFinalPointer(int64(0))})
	source, ok = err.(*downloadercommands.SourceException)
	if !ok || source.Class != "ValueError" || source.Message != "request require ids" || hits.Load() != 0 {
		t.Fatal(err, hits.Load())
	}
	denied.Store(true)
	_, err = native.Change(ctx, downloadercommands.TRChangeCall{})
	if !errors.Is(err, guardErr) || !MutationNotDispatched(err) || MutationFailureClass(err) != "owner_denied" {
		t.Fatal("owner error downgraded", err)
	}
	denied.Store(false)
	c, cancel := context.WithCancel(ctx)
	cancel()
	_, err = native.Change(c, downloadercommands.TRChangeCall{})
	if !errors.Is(err, context.Canceled) || !MutationNotDispatched(err) || hits.Load() != 0 {
		t.Fatal("cancel changed to source", err)
	}
	_, err = native.Change(ctx, downloadercommands.TRChangeCall{IDs: int64(7), UploadLimit: trFinalPointer(int64(0))})
	if err == nil || MutationNotDispatched(err) || errors.As(err, &source) || selected.Load() != 1 || hits.Load() != 2 {
		t.Fatal("selected failure swallowed/replayed", err, hits.Load(), selected.Load())
	}
	// Final guard failure must replace the exact no-args source exception.
	var guards atomic.Int32
	_, other, closeOther, err := NewSharedDownloaderCommands(Config{ID: "owned-final-finalguard", Kind: "tr", BaseURL: peer.URL}, "", func(ctx context.Context) error {
		if guards.Add(1) > 1 {
			return guardErr
		}
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeOther()
	_, err = other.(*sharedTRCommands).Change(ctx, downloadercommands.TRChangeCall{})
	if !errors.Is(err, guardErr) || !MutationNotDispatched(err) || MutationFailureClass(err) != "owner_denied" || hits.Load() != 2 {
		t.Fatal("final authority lost", err)
	}
}

func TestSharedTransmissionFinalActualLeaf(t *testing.T) {
	var mu sync.Mutex
	var frames []map[string]any
	var logs []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var frame map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&frame) != nil {
			t.Error("bad RPC")
		}
		if frame["method"] == "session-get" {
			io.WriteString(w, `{"result":"success","arguments":{"rpc-version":15,"version":"owned"}}`)
			return
		}
		if frame["method"] != "torrent-set" {
			t.Error("unexpected RPC", frame["method"])
		}
		frames = append(frames, frame["arguments"].(map[string]any))
		io.WriteString(w, `{"result":"success","arguments":{}}`)
	}))
	defer peer.Close()
	_, port, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-final-leaf", Kind: "tr", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	leaf := downloadercommands.Transmission{Client: port, Log: func(_ context.Context, level, message string) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, level+":"+message)
	}}
	ctx := context.Background()
	if ok, err := leaf.SetTorrentTag(ctx, []any{json.Number("7")}, []any{"new", "new"}, []any{"old", "new"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := leaf.ChangeTorrent(ctx, int64(7), "0", int64(-2), 2.675, int64(0)); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := leaf.ChangeTorrent(ctx, int64(7), nil, nil, nil, nil); ok || err != nil {
		t.Fatal("noargs must be source-caught false", ok, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 2 || len(logs) != 2 || !strings.HasPrefix(logs[0], "WARNING:Using feature not supported by server.") || logs[1] != "ERROR:设置种子出错：No arguments to set" {
		t.Fatal(frames, logs)
	}
	labels, ok := frames[0]["labels"].([]any)
	if !ok || len(labels) != 2 {
		t.Fatal(frames[0])
	}
	members := map[any]bool{}
	for _, label := range labels {
		members[label] = true
	}
	if !members["old"] || !members["new"] {
		t.Fatal(labels)
	}
	want := map[string]any{"ids": []any{json.Number("7")}, "uploadLimited": true, "uploadLimit": json.Number("0"), "downloadLimited": true, "downloadLimit": json.Number("-2"), "seedRatioMode": json.Number("1"), "seedRatioLimit": json.Number("2.67"), "seedIdleMode": json.Number("2"), "seedIdleLimit": json.Number("0")}
	if !reflect.DeepEqual(frames[1], want) {
		t.Fatal(frames[1], want)
	}
}
