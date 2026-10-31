package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

// Component-only native ports. They do not perform HTTP or emulate a daemon.
// Embed the existing fixed S03 port to keep the old QBC/TRC contract untouched.
type ownedFourControlQBPort struct {
	ownedCommandQBPort
	tags     func(context.Context, any, any) (any, error)
	category func(context.Context, any, any) (any, error)
	stop     func(context.Context, any) (any, error)
}

func (p ownedFourControlQBPort) AddTags(ctx context.Context, ids, tags any) (any, error) {
	return p.tags(ctx, ids, tags)
}
func (p ownedFourControlQBPort) SetCategory(ctx context.Context, hash, category any) (any, error) {
	return p.category(ctx, hash, category)
}
func (p ownedFourControlQBPort) Stop(ctx context.Context, ids any) (any, error) {
	return p.stop(ctx, ids)
}

type ownedFourControlTRPort struct {
	ownedCommandTRPort
	stop func(context.Context, any) (any, error)
}

func (p ownedFourControlTRPort) Stop(ctx context.Context, ids any) (any, error) {
	return p.stop(ctx, ids)
}

func TestOriginalDownloaderControlHostWire(t *testing.T) {
	a, scope, _ := ownedDownloaderCommandApp(t)
	// No upload/material invocation state is needed by these exact controls.
	scope.downloaderUploads = nil
	counts := map[string]int{}
	qb := ownedFourControlQBPort{
		tags: func(_ context.Context, ids, tags any) (any, error) {
			counts["tags"]++
			if !reflect.DeepEqual(ids, []any{json.Number("9007199254740993"), " mixed hash "}) || tags != nil {
				t.Fatal("selector precision/spacing or explicit null tags changed", ids, tags)
			}
			return false, nil
		},
		category: func(_ context.Context, hash, category any) (any, error) {
			counts["category"]++
			if hash != json.Number("9007199254740993") || category != "" {
				t.Fatal("source selector or falsey category changed", hash, category)
			}
			return false, nil
		},
		stop: func(_ context.Context, ids any) (any, error) {
			counts["qb_stop"]++
			if ids != nil {
				t.Fatal("qB null selector was changed", ids)
			}
			return false, nil
		},
	}
	tr := ownedFourControlTRPort{stop: func(_ context.Context, ids any) (any, error) {
		counts["tr_stop"]++
		if ids != nil {
			t.Fatal("TR wrapper null selector changed before native port", ids)
		}
		// This recording port is deliberately nonthrowing. Real TR require_ids
		// validation is a separate native gate, not simulated in the App test.
		return false, nil
	}}
	factories, closes := 0, 0
	factory := func(configured DownloaderConfig, guard func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		if configured.ID != "owned-qb" && configured.ID != "owned-tr" {
			t.Fatal("wrong exact configured downloader")
		}
		if err := guard(context.Background()); err != nil {
			t.Fatal(err)
		}
		return qb, tr, func() { closes++ }, nil
	}
	for _, sample := range []struct{ cap, body, want string }{
		{plugins.QBittorrentSetTagsCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":[9007199254740993," mixed hash "],"tags":null}`, `{"value":null}`},
		{plugins.QBittorrentSetCategoryCapability, `{"op":"invoke","downloader_id":"owned-qb","hash_string":9007199254740993,"category":false}`, `{"value":true}`},
		{plugins.QBittorrentStopCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null}`, `{"value":true}`},
		{plugins.TransmissionStopCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":null}`, `{"value":true}`},
		{plugins.QBittorrentSetCategoryCapability, `{"op":"invoke","downloader_id":"owned-qb","hash_string":"","category":"ignored"}`, `{"value":false}`},
	} {
		got, err := a.originalDownloaderControlResourceWithFactory(context.Background(), scope, sample.cap, json.RawMessage(sample.body), factory)
		if err != nil || string(got) != sample.want {
			t.Fatal(sample.cap, string(got), err)
		}
	}
	if factories != 5 || closes != 5 || !reflect.DeepEqual(counts, map[string]int{"tags": 1, "category": 1, "qb_stop": 1, "tr_stop": 1}) {
		t.Fatal("actual factory/selected-port/close counts", factories, closes, counts)
	}
	qb.tags = func(context.Context, any, any) (any, error) {
		counts["tags"]++
		return nil, &downloadercommands.SourceException{Class: "OwnedError", Message: "owned source failure"}
	}
	got, err := a.originalDownloaderControlResourceWithFactory(context.Background(), scope, plugins.QBittorrentSetTagsCapability, json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","ids":null,"tags":null}`), factory)
	if err != nil || string(got) != `{"value":null}` || counts["tags"] != 2 || closes != 6 {
		t.Fatal("source caught error is None, not a mutation success receipt", string(got), err, counts, closes)
	}
}

func TestOriginalDownloaderControlHostScopeAndNoReplay(t *testing.T) {
	a, scope, active := ownedDownloaderCommandApp(t)
	ctx := context.Background()
	factories, effects, closes := 0, 0, 0
	unknown := &downloadercommands.BoundaryError{Reason: "owned selected outcome unknown"}
	qb := ownedFourControlQBPort{stop: func(context.Context, any) (any, error) { effects++; return nil, unknown }}
	factory := func(DownloaderConfig, func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		return qb, nil, func() { closes++ }, nil
	}
	valid := json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","ids":["not-hex"]}`)
	for _, sample := range []struct{ cap, body string }{
		{plugins.QBittorrentStopCapability, `{"op":"invoke","downloader_id":"owned-qb"}`},
		{plugins.QBittorrentStopCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null,"ids":[]}`},
		{plugins.QBittorrentStopCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null,"endpoint":"owned-only"}`},
		{plugins.QBittorrentStopCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":null}`},
		{plugins.QBittorrentStopCapability, `{"op":"invoke","downloader_id":"","ids":null}`},
		{plugins.QBittorrentStopCapability, `{"op":"upload_begin","downloader_id":"owned-qb","ids":null}`},
		{plugins.QBittorrentSetTagsCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null}`},
		{plugins.QBittorrentSetCategoryCapability, `{"op":"invoke","downloader_id":"owned-qb","hash_string":null}`},
		{"downloader.qbittorrent.reannounce", string(valid)},
	} {
		if got, err := a.originalDownloaderControlResourceWithFactory(ctx, scope, sample.cap, json.RawMessage(sample.body), factory); err == nil || got != nil {
			t.Fatal("invalid/unsupported frame admitted", sample, string(got), err)
		}
	}
	oversize := json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","ids":"` + strings.Repeat("x", 128<<10) + `"}`)
	if _, err := a.originalDownloaderControlResourceWithFactory(ctx, scope, plugins.QBittorrentStopCapability, oversize, factory); err == nil {
		t.Fatal("ordinary128KiB budget bypass")
	}
	active.Store(false)
	if _, err := a.originalDownloaderControlResourceWithFactory(ctx, scope, plugins.QBittorrentStopCapability, valid, factory); err == nil {
		t.Fatal("stale scope admitted")
	}
	active.Store(true)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.originalDownloaderControlResourceWithFactory(cancelled, scope, plugins.QBittorrentStopCapability, valid, factory); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if factories != 0 || effects != 0 || closes != 0 {
		t.Fatal("rejected request reached factory", factories, effects, closes)
	}
	got, err := a.originalDownloaderControlResourceWithFactory(ctx, scope, plugins.QBittorrentStopCapability, valid, factory)
	if !errors.Is(err, unknown) || got != nil || factories != 1 || effects != 1 || closes != 1 {
		t.Fatal("unknown effect was replayed/coerced", string(got), err, factories, effects, closes)
	}
	// Factory changes the exact current configuration before the selected port.
	changedFactory := func(c DownloaderConfig, g func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		a.cfgMu.Lock()
		a.config.Downloaders[0].BaseURL = "https://changed-owned.invalid"
		a.cfgMu.Unlock()
		return qb, nil, func() { closes++ }, nil
	}
	got, err = a.originalDownloaderControlResourceWithFactory(ctx, scope, plugins.QBittorrentStopCapability, valid, changedFactory)
	var api *APIError
	if err == nil || !errors.As(err, &api) || api.Code != "DOWNLOADER_CHANGED" || got != nil || effects != 1 || factories != 2 || closes != 2 {
		t.Fatal("configuration changed before selected effect", string(got), err, effects, factories, closes)
	}
	a.cfgMu.Lock()
	a.config.Downloaders[0].BaseURL = "https://owned.invalid/qb"
	a.cfgMu.Unlock()
	// A source None method must not swallow an outer platform boundary.
	qb.tags = func(context.Context, any, any) (any, error) { effects++; return nil, unknown }
	got, err = a.originalDownloaderControlResourceWithFactory(ctx, scope, plugins.QBittorrentSetTagsCapability, json.RawMessage(`{"op":"invoke","downloader_id":"owned-qb","ids":null,"tags":null}`), factory)
	if !errors.Is(err, unknown) || got != nil || effects != 2 || factories != 3 || closes != 3 {
		t.Fatal("tag boundary became source None/retry", string(got), err, effects, factories, closes)
	}
}
