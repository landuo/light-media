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
	"sync"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

// Explicit component port; production remains the fixed native factory.
type ownedTRFinalPort struct {
	ownedCommandTRPort
	change func(context.Context, downloadercommands.TRChangeCall) (any, error)
}

func (p ownedTRFinalPort) Change(ctx context.Context, in downloadercommands.TRChangeCall) (any, error) {
	return p.change(ctx, in)
}

func ownedTRFinalLabels(t *testing.T, got, want []string) {
	t.Helper()
	// Only the independently accepted string-label set comparison. Do not sort
	// raw oracle/wire arrays or apply this normalization to other fields.
	counts := map[string]int{}
	for _, value := range got {
		counts[value]++
	}
	if len(got) != len(want) || len(counts) != len(want) {
		t.Fatal("labels lost unique membership/cardinality", got, want)
	}
	for _, value := range want {
		if counts[value] != 1 {
			t.Fatal("labels membership differs", got, want)
		}
	}
}

func TestOriginalTRFinalHostWireAndSourceEnvelope(t *testing.T) {
	a, scope, _ := ownedDownloaderCommandApp(t)
	scope.downloaderUploads = nil
	scope.installation = PluginInstallation{PluginID: "OwnedTRFinal", InstanceID: "OwnedTRFinal", Version: "1.0"}
	scope.logCall = originalPluginLogCall{id: "owned-tr-final-component", phase: "component"}
	ctx := withOriginalPluginLogContext(context.Background(), scope)
	factories, closes, calls := 0, 0, 0
	tr := ownedTRFinalPort{change: func(_ context.Context, in downloadercommands.TRChangeCall) (any, error) {
		calls++
		if in.Log == nil {
			t.Fatal("existing trusted leaf log sink not passed to native DTO")
		}
		switch calls {
		case 1:
			if !reflect.DeepEqual(in.IDs, []any{json.Number("9007199254740993"), " owned "}) || in.Labels == nil {
				t.Fatal("wire UseNumber/selector or labels changed", in.IDs, in.Labels)
			}
			ownedTRFinalLabels(t, *in.Labels, []string{"owned-old", "owned-new"})
		case 2:
			if in.IDs != json.Number("9007199254740993") || in.Labels != nil ||
				in.UploadLimited == nil || !*in.UploadLimited || in.UploadLimit == nil || *in.UploadLimit != 0 ||
				in.DownloadLimited == nil || !*in.DownloadLimited || in.DownloadLimit == nil || *in.DownloadLimit != -4 ||
				in.SeedRatioMode == nil || *in.SeedRatioMode != 1 || in.SeedRatioLimit != float64(0) ||
				in.SeedIdleMode == nil || *in.SeedIdleMode != 1 || in.SeedIdleLimit == nil || *in.SeedIdleLimit != 0 {
				t.Fatal("source string-zero truth or numeric type/preparation differs", in)
			}
		case 3:
			if in.Labels != nil || in.UploadLimited != nil || in.UploadLimit != nil || in.DownloadLimited != nil || in.DownloadLimit != nil || in.SeedRatioMode != nil || in.SeedRatioLimit != nil || in.SeedIdleMode != nil || in.SeedIdleLimit != nil {
				t.Fatal("explicit null options were default-filled", in)
			}
		default:
			t.Fatal("unexpected component dependency call", calls)
		}
		return false, nil // Original wrapper ignores nonthrowing port results.
	}}
	factory := func(c DownloaderConfig, guard func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		if c.ID != "owned-tr" || c.Kind != "transmission" || c.BaseURL != "https://owned.invalid/tr" {
			t.Fatal("factory lost current exact host-selected TR configuration")
		}
		if err := guard(ctx); err != nil {
			t.Fatal(err)
		}
		return nil, tr, func() { closes++ }, nil
	}
	tags := json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","ids":[9007199254740993," owned "],"tags":["owned-new","owned-old","owned-new"],"org_tags":["owned-old"]}`)
	limits := json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","hash_string":9007199254740993,"upload_limit":"0","download_limit":"-4","ratio_limit":"0","seeding_time_limit":"0"}`)
	nulls := json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","hash_string":"owned-a","upload_limit":null,"download_limit":null,"ratio_limit":null,"seeding_time_limit":null}`)
	for _, sample := range []struct {
		cap string
		raw json.RawMessage
	}{{plugins.TransmissionSetTagsCapability, tags}, {plugins.TransmissionChangeCapability, limits}, {plugins.TransmissionChangeCapability, nulls}} {
		got, err := a.originalTRFinalResourceWithFactory(ctx, scope, sample.cap, sample.raw, factory)
		if err != nil || string(got) != `{"value":true}` {
			t.Fatal("complete source nonthrowing return differs", string(got), err)
		}
	}
	for _, field := range []string{"upload_limit", "download_limit", "ratio_limit", "seeding_time_limit"} {
		var packet map[string]any
		if json.Unmarshal(nulls, &packet) != nil {
			t.Fatal("fixed null packet")
		}
		packet[field] = "owned-invalid"
		got, err := a.originalTRFinalResourceWithFactory(ctx, scope, plugins.TransmissionChangeCapability, ownedCommandPacket(t, packet), factory)
		if err != nil || string(got) != `{"kind":"source_error","source_error":"ValueError"}` || calls != 3 {
			t.Fatal("outside-try conversion must not become False/platform error/native call", field, string(got), err, calls)
		}
	}
	for _, sample := range []struct{ cap, raw string }{
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":"owned-a","tags":"owned-invalid-concat","org_tags":null}`},
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":"owned-a","tags":[["owned-unhashable"]],"org_tags":null}`},
		{plugins.TransmissionChangeCapability, `{"op":"invoke","downloader_id":"owned-tr","hash_string":"","upload_limit":"owned-invalid","download_limit":null,"ratio_limit":null,"seeding_time_limit":null}`},
	} {
		got, err := a.originalTRFinalResourceWithFactory(ctx, scope, sample.cap, json.RawMessage(sample.raw), factory)
		if err != nil || string(got) != `{"value":false}` || calls != 3 {
			t.Fatal("source caught/early-return boundary changed", string(got), err, calls)
		}
	}
	tr.change = func(context.Context, downloadercommands.TRChangeCall) (any, error) {
		calls++
		return nil, &downloadercommands.SourceException{Class: "ValueError", Message: "No arguments to set"}
	}
	got, err := a.originalTRFinalResourceWithFactory(ctx, scope, plugins.TransmissionChangeCapability, nulls, factory)
	if err != nil || string(got) != `{"value":false}` || calls != 4 || factories != closes {
		t.Fatal("inside-call source ValueError must be caught, not escaping envelope", string(got), err, calls, factories, closes)
	}

	// Real fixed factory + isolated owned HTTP: conversion must make zero
	// requests; low RPC version labels still dispatch; limit camelCase survives.
	var mu sync.Mutex
	var methods []string
	var selected []map[string]any
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/transmission/rpc" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected owned TR request")
			w.WriteHeader(400)
			return
		}
		var packet struct {
			Method    string         `json:"method"`
			Arguments map[string]any `json:"arguments"`
		}
		d := json.NewDecoder(io.LimitReader(r.Body, 4097))
		d.UseNumber()
		d.DisallowUnknownFields()
		if d.Decode(&packet) != nil || d.Decode(new(any)) != io.EOF {
			t.Error("unexpected fixed TR frame")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		methods = append(methods, packet.Method)
		if packet.Method == "torrent-set" {
			selected = append(selected, packet.Arguments)
		}
		mu.Unlock()
		switch packet.Method {
		case "session-get":
			if len(packet.Arguments) != 0 {
				t.Error("session initialization arguments changed")
			}
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"rpc-version":15}}`)
		case "torrent-set":
			_, _ = io.WriteString(w, `{"result":"success","arguments":{}}`)
		default:
			t.Error("unexpected native TR method", packet.Method)
			w.WriteHeader(400)
		}
	}))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders[1].BaseURL = peer.URL + "/transmission/rpc"
	a.config.LogLevel = "WARN"
	a.cfgMu.Unlock()
	nativeInvalid := json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","hash_string":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","upload_limit":"owned-invalid","download_limit":null,"ratio_limit":null,"seeding_time_limit":null}`)
	got, err = a.originalSharedTRFinalResource(ctx, scope, plugins.TransmissionChangeCapability, nativeInvalid)
	mu.Lock()
	zero := len(methods) == 0
	mu.Unlock()
	if err != nil || string(got) != `{"kind":"source_error","source_error":"ValueError"}` || !zero {
		t.Fatal("actual native factory conversion was not prewrite", string(got), err, zero)
	}
	for _, sample := range []struct{ cap, raw string }{
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":7,"tags":["owned-new","owned-old","owned-new"],"org_tags":["owned-old"]}`},
		{plugins.TransmissionChangeCapability, `{"op":"invoke","downloader_id":"owned-tr","hash_string":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","upload_limit":"0","download_limit":-4,"ratio_limit":2.675,"seeding_time_limit":0}`},
	} {
		got, err = a.originalSharedTRFinalResource(ctx, scope, sample.cap, json.RawMessage(sample.raw))
		if err != nil || string(got) != `{"value":true}` {
			t.Fatal("actual native tag/limits dispatch failed", string(got), err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(methods, []string{"session-get", "torrent-set", "session-get", "torrent-set"}) || len(selected) != 2 {
		t.Fatal("extra initialization/read, replay, or low-version labels rejection", methods)
	}
	if len(selected[0]) != 2 || !reflect.DeepEqual(selected[0]["ids"], []any{json.Number("7")}) {
		t.Fatal("native tag selector/fixed shape differs", selected[0])
	}
	rawLabels, ok := selected[0]["labels"].([]any)
	if !ok {
		t.Fatal("native labels not an array", selected[0])
	}
	labels := make([]string, len(rawLabels))
	for i, value := range rawLabels {
		labels[i], ok = value.(string)
		if !ok {
			t.Fatal("nonstring native label", value)
		}
	}
	ownedTRFinalLabels(t, labels, []string{"owned-old", "owned-new"})
	want := map[string]any{"ids": []any{strings.Repeat("a", 40)}, "uploadLimited": true, "uploadLimit": json.Number("0"), "downloadLimited": true, "downloadLimit": json.Number("-4"), "seedRatioMode": json.Number("1"), "seedRatioLimit": json.Number("2.67"), "seedIdleMode": json.Number("2"), "seedIdleLimit": json.Number("0")}
	if !reflect.DeepEqual(selected[1], want) {
		t.Fatal("actual source camelCase/value projection differs", selected[1], want)
	}
	// The low-version message uses the existing trusted host log writer/query,
	// not a new worker log capability or an assertion that HTTP200 is success.
	flushTestLogs(t, a)
	page, err := a.queryLogs(ctx, map[string]string{"plugin_instance_id": "OwnedTRFinal", "from": "0001-01-01T00:00:00Z", "limit": "100"})
	if err != nil {
		t.Fatal(err)
	}
	warnings := 0
	for _, entry := range page.Entries {
		if entry["message"] == "Using feature not supported by server. RPC version for server 15, feature introduced in 16." {
			warnings++
			if entry["level"] != "WARN" || entry["event"] != "downloader_command" || entry["plugin_id"] != "OwnedTRFinal" || entry["plugin_instance_id"] != "OwnedTRFinal" || entry["plugin_call_id"] != "owned-tr-final-component" || entry["operation"] != "component" {
				t.Fatal("native warning lost trusted invocation attribution", entry)
			}
		}
	}
	if warnings != 1 {
		t.Fatal("low-version warning missing/repeated despite successful selected request", warnings)
	}
}

func TestOriginalTRFinalHostScopeAndUnknown(t *testing.T) {
	a, scope, active := ownedDownloaderCommandApp(t)
	ctx := context.Background()
	factories, effects, closes := 0, 0, 0
	unknown := &downloadercommands.BoundaryError{Reason: "owned selected outcome unknown"}
	tr := ownedTRFinalPort{change: func(context.Context, downloadercommands.TRChangeCall) (any, error) {
		effects++
		return nil, unknown
	}}
	factory := func(DownloaderConfig, func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		return nil, tr, func() { closes++ }, nil
	}
	valid := json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","ids":"owned-a","tags":["owned-label"],"org_tags":null}`)
	change := json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","hash_string":"owned-a","upload_limit":1,"download_limit":null,"ratio_limit":null,"seeding_time_limit":null}`)
	for _, sample := range []struct{ cap, raw string }{
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":null,"tags":null}`},
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":null,"tags":null,"org_tags":null,"org_tags":[]}`},
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":"owned-tr","ids":null,"tags":null,"org_tags":null,"url":"owned.invalid"}`},
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":"owned-qb","ids":null,"tags":null,"org_tags":null}`},
		{plugins.TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":null,"ids":null,"tags":null,"org_tags":null}`},
		{plugins.TransmissionSetTagsCapability, `{"op":"upload_begin","downloader_id":"owned-tr","ids":null,"tags":null,"org_tags":null}`},
		{plugins.TransmissionChangeCapability, `{"op":"invoke","downloader_id":"owned-tr","hash_string":null,"upload_limit":null,"download_limit":null,"ratio_limit":null}`},
		{"downloader.transmission.reannounce", string(valid)},
	} {
		if out, err := a.originalTRFinalResourceWithFactory(ctx, scope, sample.cap, json.RawMessage(sample.raw), factory); err == nil || out != nil {
			t.Fatal("invalid/unsupported fixed frame admitted", sample.cap, string(out), err)
		}
	}
	oversize := json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","ids":null,"tags":["` + strings.Repeat("x", 128<<10) + `"],"org_tags":null}`)
	if _, err := a.originalTRFinalResourceWithFactory(ctx, scope, plugins.TransmissionSetTagsCapability, oversize, factory); err == nil {
		t.Fatal("ordinary128KiB bound bypass")
	}
	active.Store(false)
	if _, err := a.originalTRFinalResourceWithFactory(ctx, scope, plugins.TransmissionSetTagsCapability, valid, factory); err == nil {
		t.Fatal("stale scope admitted")
	}
	active.Store(true)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.originalTRFinalResourceWithFactory(cancelled, scope, plugins.TransmissionSetTagsCapability, valid, factory); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if factories != 0 || effects != 0 || closes != 0 {
		t.Fatal("rejected request reached factory", factories, effects, closes)
	}
	got, err := a.originalTRFinalResourceWithFactory(ctx, scope, plugins.TransmissionSetTagsCapability, valid, factory)
	if got != nil || !errors.Is(err, unknown) || factories != 1 || effects != 1 || closes != 1 {
		t.Fatal("unknown result was replayed or source-caught", string(got), err, factories, effects, closes)
	}
	for _, failure := range []error{fmt.Errorf("owned wrapper: %w", &downloadercommands.SourceException{Class: "ValueError", Message: "must not leak"}), unknown, context.Canceled} {
		tr.change = func(context.Context, downloadercommands.TRChangeCall) (any, error) { effects++; return nil, failure }
		got, err = a.originalTRFinalResourceWithFactory(ctx, scope, plugins.TransmissionChangeCapability, change, factory)
		if got != nil || !errors.Is(err, failure) {
			t.Fatal("wrapped/platform/cancel error became source result", string(got), err)
		}
	}
	beforeEffects, beforeFactories, beforeCloses := effects, factories, closes
	changedFactory := func(DownloaderConfig, func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		factories++
		a.cfgMu.Lock()
		a.config.Downloaders[1].BaseURL = "https://changed-owned.invalid/tr"
		a.cfgMu.Unlock()
		return nil, tr, func() { closes++ }, nil
	}
	got, err = a.originalTRFinalResourceWithFactory(ctx, scope, plugins.TransmissionChangeCapability, change, changedFactory)
	var api *APIError
	if got != nil || !errors.As(err, &api) || api.Code != "DOWNLOADER_CHANGED" || effects != beforeEffects || factories != beforeFactories+1 || closes != beforeCloses+1 {
		t.Fatal("exact current config guard/close failed", string(got), err)
	}
}
