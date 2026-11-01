package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

// Component-only typed ports. No worker/native HTTP or real wall-clock sleep.
type ownedQBTagScopePort struct {
	ownedCommandQBPort
	run func(context.Context, func(context.Context, downloadercommands.QBTagPrimitives) (any, error)) (any, error)
}

func (p ownedQBTagScopePort) WithTagScope(ctx context.Context, run func(context.Context, downloadercommands.QBTagPrimitives) (any, error)) (any, error) {
	return p.run(ctx, run)
}

type ownedQBTagPrimitives struct {
	info   func(context.Context, any) (*downloadercommands.QBTagRows, error)
	remove func(context.Context, any, any) (any, error)
	delete func(context.Context, any) (any, error)
	sleep  func(context.Context, time.Duration) error
}

func (p ownedQBTagPrimitives) Info(ctx context.Context, status any) (*downloadercommands.QBTagRows, error) {
	return p.info(ctx, status)
}
func (p ownedQBTagPrimitives) RemoveTags(ctx context.Context, ids, tags any) (any, error) {
	return p.remove(ctx, ids, tags)
}
func (p ownedQBTagPrimitives) DeleteTags(ctx context.Context, tags any) (any, error) {
	return p.delete(ctx, tags)
}
func (p ownedQBTagPrimitives) Sleep(ctx context.Context, duration time.Duration) error {
	return p.sleep(ctx, duration)
}

func TestOriginalQBTagHostWireAndSourceEnvelope(t *testing.T) {
	a, scope, _ := ownedDownloaderCommandApp(t)
	scope.downloaderUploads = nil
	ctx := context.Background()
	factories, closes, scopes, removes, deletes, reads, sleeps := 0, 0, 0, 0, 0, 0, 0
	var rows []any
	var expectedID any = []any{json.Number("9007199254740993"), " mixed hash "}
	port := ownedQBTagPrimitives{
		info: func(_ context.Context, status any) (*downloadercommands.QBTagRows, error) {
			reads++
			if status != nil {
				t.Fatal("explicit null status changed", status)
			}
			return &downloadercommands.QBTagRows{Values: rows}, nil
		},
		remove: func(_ context.Context, ids, tags any) (any, error) {
			removes++
			if !reflect.DeepEqual(ids, expectedID) || tags != nil {
				t.Fatal("selector precision/spacing/falsey value or explicit null tag changed", ids, tags)
			}
			return false, nil
		},
		delete: func(_ context.Context, tags any) (any, error) {
			deletes++
			if tags != nil {
				t.Fatal("explicit null global tag changed", tags)
			}
			return nil, nil
		},
		sleep: func(ctx context.Context, duration time.Duration) error {
			sleeps++
			if duration != 3*time.Second {
				t.Fatal("original requested delay changed", duration)
			}
			return ctx.Err()
		},
	}
	qb := ownedQBTagScopePort{run: func(ctx context.Context, run func(context.Context, downloadercommands.QBTagPrimitives) (any, error)) (any, error) {
		scopes++
		return run(ctx, port)
	}}
	factory := func(configured DownloaderConfig, guard func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		if configured.ID != "owned-qb" || configured.BaseURL != "https://owned.invalid/qb" {
			t.Fatal("factory did not receive exact current host configuration")
		}
		if err := guard(ctx); err != nil {
			t.Fatal(err)
		}
		return qb, nil, func() { closes++ }, nil
	}
	deleteRaw := json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","ids":[9007199254740993," mixed hash "],"tag":null}`)
	got, err := a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentDeleteTagsCapability, deleteRaw, factory)
	if err != nil || string(got) != `{"value":true}` || factories != 1 || scopes != 1 || removes != 1 || deletes != 1 || closes != 1 {
		t.Fatal("complete nonthrowing two-write leaf return", string(got), err, factories, scopes, removes, deletes, closes)
	}
	lookup := json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","tags":null,"status":null}`)
	for _, sample := range []struct {
		value any
		want  string
	}{
		{nil, `{"value":null}`},
		{false, `{"value":false}`},
		{"", `{"value":""}`},
		{json.Number("9007199254740993"), `{"value":9007199254740993}`},
	} {
		rows = nil
		expectedID = sample.value
		if sample.value != nil {
			rows = []any{map[string]any{"hash": sample.value}}
		}
		beforeReads, beforeSleep, beforeRemove, beforeDelete := reads, sleeps, removes, deletes
		got, err = a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentGetIDByTagCapability, lookup, factory)
		if err != nil || string(got) != sample.want {
			t.Fatal("raw source lookup value changed", string(got), err)
		}
		wantRead, wantCleanup := 1, 1
		if sample.value == nil {
			wantRead, wantCleanup = 9, 0
		}
		if reads-beforeReads != wantRead || sleeps-beforeSleep != wantRead || removes-beforeRemove != wantCleanup || deletes-beforeDelete != wantCleanup {
			t.Fatal("actual leaf steps differ", reads-beforeReads, sleeps-beforeSleep, removes-beforeRemove, deletes-beforeDelete)
		}
	}
	// Falsey tags skip filtering. Original private first-row .get is outside
	// its catch, so this ordinary JSON row produces the observed source class.
	rows = []any{json.Number("7")}
	got, err = a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentGetIDByTagCapability, lookup, factory)
	if err != nil || string(got) != `{"kind":"source_error","source_error":"AttributeError"}` {
		t.Fatal("escaping source error was swallowed, leaked or converted", string(got), err)
	}
	if factories != 6 || closes != 6 || scopes != 6 {
		t.Fatal("one factory/scope/close required for every complete public call", factories, scopes, closes)
	}
	// One actual production-factory seam, not the recording scope above:
	// original first-row AttributeError must survive the native aggregate proof
	// and become only the permitted App class envelope. Fixed owned peer only;
	// use the real native 3-second delay, with no environment/proxy rewrite.
	var peerCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerCalls.Add(1)
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 129))
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/torrents/info" || r.URL.RawQuery != "" ||
			r.Header.Get("Authorization") != "Bearer owned-qb-tag-key" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
			r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" || readErr != nil || len(body) != 0 {
			t.Error("unexpected request to fixed owned qB info peer")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[7]`)
	}))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders[0].BaseURL = peer.URL
	a.config.Downloaders[0].APIKey = "owned-qb-tag-key"
	a.cfgMu.Unlock()
	nativeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	started := time.Now()
	got, err = a.originalSharedQBTagResource(nativeCtx, scope, plugins.QBittorrentGetIDByTagCapability, lookup)
	elapsed := time.Since(started)
	if err != nil || string(got) != `{"kind":"source_error","source_error":"AttributeError"}` || peerCalls.Load() != 1 || elapsed < 3*time.Second {
		t.Fatal("actual native proof/source-envelope seam or real original delay differs", string(got), err, peerCalls.Load(), elapsed)
	}
}

func TestOriginalQBTagHostScopeAndUnknown(t *testing.T) {
	a, scope, active := ownedDownloaderCommandApp(t)
	ctx := context.Background()
	factories, effects, closes := 0, 0, 0
	unknown := &downloadercommands.BoundaryError{Reason: "owned selected outcome unknown"}
	port := ownedQBTagPrimitives{
		remove: func(context.Context, any, any) (any, error) { effects++; return nil, unknown },
		delete: func(context.Context, any) (any, error) {
			t.Fatal("must not issue write two after unknown effect")
			return nil, nil
		},
	}
	qb := ownedQBTagScopePort{run: func(ctx context.Context, run func(context.Context, downloadercommands.QBTagPrimitives) (any, error)) (any, error) {
		return run(ctx, port)
	}}
	factory := func(DownloaderConfig, func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		return qb, nil, func() { closes++ }, nil
	}
	deleteRaw := json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","ids":null,"tag":null}`)
	lookup := json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","tags":null,"status":null}`)
	for _, sample := range []struct{ cap, body string }{
		{plugins.QBittorrentDeleteTagsCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null}`},
		{plugins.QBittorrentDeleteTagsCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null,"tag":null,"tag":[]}`},
		{plugins.QBittorrentDeleteTagsCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null,"tag":null,"url":"owned.invalid"}`},
		{plugins.QBittorrentDeleteTagsCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":null,"tag":null}`},
		{plugins.QBittorrentDeleteTagsCapability, `{"op":"upload_begin","downloader_id":"owned-qb","ids":null,"tag":null}`},
		{plugins.QBittorrentGetIDByTagCapability, `{"op":"invoke","downloader_id":"owned-qb","tags":null}`},
		{plugins.QBittorrentGetIDByTagCapability, `{"op":"invoke","downloader_id":null,"tags":null,"status":null}`},
		{"downloader.qbittorrent.delete_files", string(deleteRaw)},
	} {
		if out, err := a.originalQBTagResourceWithFactory(ctx, scope, sample.cap, json.RawMessage(sample.body), factory); err == nil || out != nil {
			t.Fatal("invalid/unsupported frame admitted", sample.cap, string(out), err)
		}
	}
	oversize := json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","tags":"` + strings.Repeat("x", 128<<10) + `","status":null}`)
	if _, err := a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentGetIDByTagCapability, oversize, factory); err == nil {
		t.Fatal("ordinary128KiB input bound bypassed")
	}
	active.Store(false)
	if _, err := a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentDeleteTagsCapability, deleteRaw, factory); err == nil {
		t.Fatal("stale scope reached native")
	}
	active.Store(true)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.originalQBTagResourceWithFactory(cancelled, scope, plugins.QBittorrentDeleteTagsCapability, deleteRaw, factory); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if factories != 0 || effects != 0 || closes != 0 {
		t.Fatal("rejected frame created a client", factories, effects, closes)
	}
	got, err := a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentDeleteTagsCapability, deleteRaw, factory)
	if got != nil || !errors.Is(err, unknown) || factories != 1 || effects != 1 || closes != 1 {
		t.Fatal("unknown effect replayed or converted to source False", string(got), err, factories, effects, closes)
	}
	// A direct original AttributeError is eligible only on lookup. Wrapped
	// source errors, other source classes and platform errors stay host errors.
	for _, sample := range []struct {
		err error
	}{
		{fmt.Errorf("owned outer boundary: %w", &downloadercommands.SourceException{Class: "AttributeError", Message: "must not leak"})},
		{&downloadercommands.SourceException{Class: "ValueError", Message: "must not leak"}},
		{unknown},
		{context.Canceled},
	} {
		qb.run = func(context.Context, func(context.Context, downloadercommands.QBTagPrimitives) (any, error)) (any, error) {
			return nil, sample.err
		}
		got, err = a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentGetIDByTagCapability, lookup, factory)
		if got != nil || !errors.Is(err, sample.err) {
			t.Fatal("non-eligible error became source envelope", string(got), err)
		}
	}
	beforeEffects, beforeFactories, beforeClose := effects, factories, closes
	changedFactory := func(c DownloaderConfig, guard func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		a.cfgMu.Lock()
		a.config.Downloaders[0].BaseURL = "https://changed-owned.invalid"
		a.cfgMu.Unlock()
		return qb, nil, func() { closes++ }, nil
	}
	got, err = a.originalQBTagResourceWithFactory(ctx, scope, plugins.QBittorrentGetIDByTagCapability, lookup, changedFactory)
	var api *APIError
	if got != nil || !errors.As(err, &api) || api.Code != "DOWNLOADER_CHANGED" || effects != beforeEffects || factories != beforeFactories+1 || closes != beforeClose+1 {
		t.Fatal("current configuration guard not applied before leaf", string(got), err)
	}
}
