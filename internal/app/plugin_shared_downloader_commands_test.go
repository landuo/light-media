package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

// These ports record the native boundary only. This file tests the actual App
// wire/store/leaf components, not a worker, HTTP client or real downloader.
type ownedCommandQBPort struct {
	add    func(context.Context, downloadercommands.QBAddCall) (any, error)
	effect func(context.Context, string, any) (any, error)
}

func (p ownedCommandQBPort) Add(ctx context.Context, in downloadercommands.QBAddCall) (any, error) {
	return p.add(ctx, in)
}
func (p ownedCommandQBPort) Start(ctx context.Context, ids any) (any, error) {
	return p.effect(ctx, "start", ids)
}
func (p ownedCommandQBPort) Recheck(ctx context.Context, ids any) (any, error) {
	return p.effect(ctx, "recheck", ids)
}
func (p ownedCommandQBPort) DeleteKeepFiles(ctx context.Context, ids any) (any, error) {
	return p.effect(ctx, "delete_keep_files", ids)
}

type ownedCommandTRPort struct {
	add    func(context.Context, downloadercommands.TRAddCall) (any, error)
	effect func(context.Context, string, any) (any, error)
}

func (p ownedCommandTRPort) Add(ctx context.Context, in downloadercommands.TRAddCall) (any, error) {
	return p.add(ctx, in)
}
func (p ownedCommandTRPort) Start(ctx context.Context, ids any) (any, error) {
	return p.effect(ctx, "start", ids)
}
func (p ownedCommandTRPort) Recheck(ctx context.Context, ids any) (any, error) {
	return p.effect(ctx, "recheck", ids)
}
func (p ownedCommandTRPort) DeleteKeepFiles(ctx context.Context, ids any) (any, error) {
	return p.effect(ctx, "delete_keep_files", ids)
}

func ownedDownloaderCommandApp(t *testing.T) (*App, originalBaseCallScope, *atomic.Bool) {
	t.Helper()
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "owned-qb", Kind: "qb", BaseURL: "https://owned.invalid/qb", AutoCategory: true, Sequential: true}},
		{Config: download.Config{ID: "owned-tr", Kind: "transmission", BaseURL: "https://owned.invalid/tr"}},
	}
	a.cfgMu.Unlock()
	active := &atomic.Bool{}
	active.Store(true)
	scope := originalBaseCallScope{downloaderUploads: newOriginalDownloaderCommandUploads(), validate: func(ctx context.Context) error {
		if !active.Load() {
			return bad("PLUGIN_CALL_CLOSED", "owned component phase ended")
		}
		return ctx.Err()
	}}
	t.Cleanup(scope.downloaderUploads.close)
	return a, scope, active
}

func ownedCommandPacket(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func ownedCommandBegin(t *testing.T, a *App, scope originalBaseCallScope, cap, downloader string, data []byte, args map[string]any, factory originalDownloaderCommandNativeFactory) string {
	t.Helper()
	raw, err := a.originalDownloaderCommandResourceWithFactory(context.Background(), scope, cap, ownedCommandPacket(t, map[string]any{"op": "upload_begin", "downloader_id": downloader, "args": args, "total": len(data), "sha256": downloaderUploadDigest(data)}), factory)
	var reply struct {
		Handle string `json:"handle"`
	}
	if err != nil || json.Unmarshal(raw, &reply) != nil || !originalDownloaderUploadHandle(reply.Handle) {
		t.Fatal("begin", string(raw), err)
	}
	return reply.Handle
}

func ownedCommandAppend(t *testing.T, a *App, scope originalBaseCallScope, cap, handle string, data []byte, factory originalDownloaderCommandNativeFactory) {
	t.Helper()
	for offset := 0; offset < len(data); {
		next := min(offset+plugins.TorrentMaterialChunkBytes, len(data))
		raw, err := a.originalDownloaderCommandResourceWithFactory(context.Background(), scope, cap, downloaderUploadPacket(t, handle, offset, data[offset:next]), factory)
		var reply struct {
			Handle string `json:"handle"`
			Offset int    `json:"offset"`
		}
		if err != nil || json.Unmarshal(raw, &reply) != nil || reply.Handle != handle || reply.Offset != next {
			t.Fatal("actual append receipt", offset, string(raw), err)
		}
		offset = next
	}
}

func TestOriginalDownloaderCommandHostWire(t *testing.T) {
	a, scope, _ := ownedDownloaderCommandApp(t)
	ctx := context.Background()
	data := bytes.Repeat([]byte("x"), plugins.TorrentMaterialChunkBytes+3)
	var qAdds, trAdds, effects, closes int
	qb := ownedCommandQBPort{add: func(_ context.Context, in downloadercommands.QBAddCall) (any, error) {
		qAdds++
		if !bytes.Equal(in.TorrentFiles.([]byte), data) || in.SavePath != "/owned/downloads" || in.AutoManagement != true || in.Sequential != true || in.FirstLastPiece != false {
			t.Fatal("host-selected options/full bytes changed", in.SavePath)
		}
		if in.Extra["owned_number"] != json.Number("9007199254740993") || in.Paused != nil {
			t.Fatal("precision or explicit null lost", in.Extra, in.Paused)
		}
		return "Ok.", nil
	}, effect: func(_ context.Context, _ string, ids any) (any, error) {
		effects++
		if ids != nil {
			t.Fatal("control explicit null changed", ids)
		}
		return false, nil // Source controls ignore this vendor return value.
	}}
	tr := ownedCommandTRPort{add: func(_ context.Context, in downloadercommands.TRAddCall) (any, error) {
		trAdds++
		if trAdds == 1 && in.Content != nil {
			t.Fatal("TR null content changed", in.Content)
		}
		if trAdds == 2 {
			b, ok := in.Content.([]byte)
			if !ok || b == nil || len(b) != 0 {
				t.Fatal("empty upload must be nonnil bytes")
			}
		}
		return map[string]any{"id": json.Number("9007199254740993"), "native": true}, nil
	}, effect: qb.effect}
	factory := func(configured DownloaderConfig, guard func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		if err := guard(ctx); err != nil {
			t.Fatal(err)
		}
		if configured.ID != "owned-qb" && configured.ID != "owned-tr" {
			t.Fatal("worker selected another endpoint", configured.ID)
		}
		return qb, tr, func() { closes++ }, nil
	}
	handle := ownedCommandBegin(t, a, scope, originalDownloaderUploadQBAdd, "owned-qb", data, map[string]any{"download_dir": "/owned/downloads", "owned_number": json.Number("9007199254740993"), "is_paused": nil}, factory)
	ownedCommandAppend(t, a, scope, originalDownloaderUploadQBAdd, handle, data, factory)
	if closes != 0 {
		t.Fatal("upload called a downloader before submit")
	}
	out, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadQBAdd, ownedCommandPacket(t, map[string]any{"op": "upload_submit", "handle": handle}), factory)
	if err != nil || string(out) != `{"success":true,"ids":[]}` || qAdds != 1 {
		t.Fatal("qB source tuple projection", string(out), err, qAdds)
	}
	for _, prefix := range []string{"downloader.qbittorrent.", "downloader.transmission."} {
		key := "owned-qb"
		if strings.Contains(prefix, "transmission") {
			key = "owned-tr"
		}
		for _, action := range []string{"start", "recheck", "delete_keep_files"} {
			out, err = a.originalDownloaderCommandResourceWithFactory(ctx, scope, prefix+action, ownedCommandPacket(t, map[string]any{"op": "invoke", "downloader_id": key, "ids": nil}), factory)
			want := `{"value":true}`
			if action == "delete_keep_files" {
				want = `{"value":false}`
			}
			if err != nil || string(out) != want {
				t.Fatal(prefix, action, string(out), err)
			}
		}
	}
	out, err = a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadTRAdd, json.RawMessage(`{"op":"invoke","downloader_id":"owned-tr","args":{"content":null}}`), factory)
	if err != nil || !bytes.Contains(out, []byte(`"id":9007199254740993`)) {
		t.Fatal("native TR JSON", string(out), err)
	}
	empty := ownedCommandBegin(t, a, scope, originalDownloaderUploadTRAdd, "owned-tr", []byte{}, map[string]any{}, factory)
	if _, err = a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadTRAdd, ownedCommandPacket(t, map[string]any{"op": "upload_submit", "handle": empty}), factory); err != nil {
		t.Fatal(err)
	}
	if qAdds != 1 || trAdds != 2 || effects != 4 || closes != 9 {
		t.Fatal("actual component calls", qAdds, trAdds, effects, closes)
	}
	// Missing keys and aliases cannot turn into source defaults. No HTTP or
	// native factory is needed for these protocol rejection observations.
	for _, raw := range []string{
		`{"op":"invoke","downloader_id":"owned-qb","args":{}}`,
		`{"op":"invoke","op":"invoke","downloader_id":"owned-qb","args":{"content":null}}`,
		`{"op":"invoke","downloader_id":"owned-qb","args":{"content":null},"endpoint":"https://owned.invalid"}`,
	} {
		if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadQBAdd, json.RawMessage(raw), factory); err == nil {
			t.Fatal("invalid framing accepted", raw)
		}
	}
	if closes != 9 {
		t.Fatal("invalid frame reached factory")
	}
	// The outer route rejects identity before selecting the larger decoder.
	if _, err := a.pluginOriginalResourceHost(ctx, scope, plugins.Identity{PluginID: "foreign"}, originalDownloaderUploadQBAdd, json.RawMessage(`invalid`)); err == nil {
		t.Fatal("foreign identity admitted")
	}
}

func TestOriginalDownloaderCommandHostCurrentBinding(t *testing.T) {
	a, scope, active := ownedDownloaderCommandApp(t)
	ctx := context.Background()
	factory := func(DownloaderConfig, func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		t.Fatal("guard/protocol rejection reached native factory")
		return nil, nil, nil, nil
	}
	cap := originalDownloaderUploadQBAdd
	data := []byte("owned")
	handle := ownedCommandBegin(t, a, scope, cap, "owned-qb", data, map[string]any{"nested": map[string]any{"rows": []any{json.Number("9007199254740993"), nil}}}, factory)
	view, err := scope.downloaderUploads.snapshot(ctx, cap, handle)
	if err != nil {
		t.Fatal(err)
	}
	view.args["nested"].(map[string]any)["rows"].([]any)[0] = "mutated"
	view.configured.BaseURL = "https://different.invalid"
	view, err = scope.downloaderUploads.snapshot(ctx, cap, handle)
	if err != nil || view.configured.BaseURL != "https://owned.invalid/qb" || view.args["nested"].(map[string]any)["rows"].([]any)[0] != json.Number("9007199254740993") {
		t.Fatal("stored metadata was mutable", err)
	}
	// beginBound also owns a deep copy, not merely the snapshot getter.
	copyHandle, err := scope.downloaderUploads.beginBound(ctx, cap, 0, downloaderUploadDigest(nil), view)
	if err != nil {
		t.Fatal(err)
	}
	view.args["nested"].(map[string]any)["rows"].([]any)[0] = "caller changed after begin"
	view.configured.BaseURL = "https://different.invalid"
	stored, err := scope.downloaderUploads.snapshot(ctx, cap, copyHandle)
	if err != nil || stored.configured.BaseURL != "https://owned.invalid/qb" || stored.args["nested"].(map[string]any)["rows"].([]any)[0] != json.Number("9007199254740993") {
		t.Fatal("begin retained caller metadata", err)
	}
	if err := scope.downloaderUploads.abort(ctx, cap, copyHandle); err != nil {
		t.Fatal(err)
	}
	packet := downloaderUploadPacket(t, handle, 0, data)
	a.cfgMu.Lock()
	a.config.Downloaders[0].Password = "owned changed value"
	a.cfgMu.Unlock()
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, cap, packet, factory); err == nil {
		t.Fatal("changed config accepted append")
	}
	a.cfgMu.Lock()
	a.config.Downloaders[0].Password = ""
	a.cfgMu.Unlock()
	ownedCommandAppend(t, a, scope, cap, handle, data, factory)
	disabled := false
	a.cfgMu.Lock()
	a.config.Downloaders[0].Enabled = &disabled
	a.cfgMu.Unlock()
	for _, op := range []string{"upload_submit", "upload_abort"} {
		if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, cap, ownedCommandPacket(t, map[string]any{"op": op, "handle": handle}), factory); err == nil {
			t.Fatal("disabled config accepted", op)
		}
	}
	a.cfgMu.Lock()
	a.config.Downloaders[0].Enabled = nil
	a.cfgMu.Unlock()
	active.Store(false)
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, cap, packet, factory); err == nil {
		t.Fatal("stale phase selected append decoder")
	}
	active.Store(true)
	foreign := scope
	foreign.downloaderUploads = newOriginalDownloaderCommandUploads()
	defer foreign.downloaderUploads.close()
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, foreign, cap, ownedCommandPacket(t, map[string]any{"op": "upload_submit", "handle": handle}), factory); err == nil {
		t.Fatal("foreign invocation borrowed token")
	}
	out, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, cap, ownedCommandPacket(t, map[string]any{"op": "upload_abort", "handle": handle}), factory)
	if err != nil || string(out) != `{"aborted":true}` {
		t.Fatal("authorized unused abort", string(out), err)
	}
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, cap, json.RawMessage(`{"op":"invoke","downloader_id":"","args":{"content":null}}`), factory); err == nil {
		t.Fatal("empty ID fell back to default")
	}
	// Closing App admission must reject submit before one-use claim.
	handle = ownedCommandBegin(t, a, scope, cap, "owned-qb", []byte{}, map[string]any{}, factory)
	a.closeOriginalBackgroundAdmission()
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, cap, ownedCommandPacket(t, map[string]any{"op": "upload_submit", "handle": handle}), factory); err == nil {
		t.Fatal("closing App accepted submit")
	}
	if _, err := scope.downloaderUploads.snapshot(ctx, cap, handle); err != nil {
		t.Fatal("non-admitted submit consumed its token", err)
	}
}

func TestOriginalDownloaderCommandHostClaimDrainsOnce(t *testing.T) {
	a, scope, active := ownedDownloaderCommandApp(t)
	ctx := context.Background()
	data := []byte("owned claimed bytes survive phase close")
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, closes atomic.Int32
	unknown := &downloadercommands.BoundaryError{Reason: "owned unknown external outcome"}
	qb := ownedCommandQBPort{add: func(_ context.Context, in downloadercommands.QBAddCall) (any, error) {
		calls.Add(1)
		close(entered)
		<-release
		if !bytes.Equal(in.TorrentFiles.([]byte), data) {
			t.Error("phase close cleared claimed bytes")
		}
		return nil, unknown
	}}
	factory := func(DownloaderConfig, func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
		return qb, nil, func() { closes.Add(1) }, nil
	}
	handle := ownedCommandBegin(t, a, scope, originalDownloaderUploadQBAdd, "owned-qb", data, map[string]any{}, factory)
	ownedCommandAppend(t, a, scope, originalDownloaderUploadQBAdd, handle, data, factory)
	packet := ownedCommandPacket(t, map[string]any{"op": "upload_submit", "handle": handle})
	result := make(chan error, 1)
	go func() {
		_, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadQBAdd, packet, factory)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("native component did not enter")
	}
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadQBAdd, packet, factory); err == nil {
		close(release)
		t.Fatal("parallel duplicate submit accepted")
	}
	active.Store(false)
	scope.downloaderUploads.close()
	select {
	case <-result:
		close(release)
		t.Fatal("phase close pretended callback drained")
	default:
	}
	close(release)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed phase returned successful outcome")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admitted callback failed to drain")
	}
	a.wg.Wait()
	if calls.Load() != 1 || closes.Load() != 1 {
		t.Fatal("repeat dispatch or leaked client", calls.Load(), closes.Load())
	}
	// An open phase also preserves the exact unknown boundary and the tombstone;
	// no retry is inferred merely because the callback returned an error.
	active.Store(true)
	scope.downloaderUploads = newOriginalDownloaderCommandUploads()
	defer scope.downloaderUploads.close()
	qb.add = func(context.Context, downloadercommands.QBAddCall) (any, error) { calls.Add(1); return nil, unknown }
	handle = ownedCommandBegin(t, a, scope, originalDownloaderUploadQBAdd, "owned-qb", []byte("x"), map[string]any{}, factory)
	ownedCommandAppend(t, a, scope, originalDownloaderUploadQBAdd, handle, []byte("x"), factory)
	packet = ownedCommandPacket(t, map[string]any{"op": "upload_submit", "handle": handle})
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadQBAdd, packet, factory); !errors.Is(err, unknown) {
		t.Fatal("unknown effect became source false", err)
	}
	if _, err := a.originalDownloaderCommandResourceWithFactory(ctx, scope, originalDownloaderUploadQBAdd, packet, factory); err == nil {
		t.Fatal("failed submit resurrected token")
	}
	if calls.Load() != 2 || closes.Load() != 2 {
		t.Fatal("unknown-effect retry occurred", calls.Load(), closes.Load())
	}
}
