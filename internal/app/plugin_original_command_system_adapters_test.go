package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"light-media/internal/systemcommands"
)

func command40AdapterStore(t *testing.T, dir string) *originalSystemCommandAdapters {
	t.Helper()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &originalSystemCommandAdapters{app: &App{Store: store}}
}

func TestSystemCommandMarkerRealStoreColdAndPrivateKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	h := command40AdapterStore(t, dir)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = h.app.Store.Close()
		}
	})
	if _, found, err := h.Get(ctx, originalSystemRestartMarkerKey); err != nil || found {
		t.Fatalf("absent: %v %v", found, err)
	}
	if err := h.app.Store.Put(ctx, "cache", originalSystemRestartMarkerKey, map[string]any{"unrelated": true}); err != nil {
		t.Fatal(err)
	}
	marker := map[string]any{"channel": "Web", "userid": json.Number("9223372036854775807")}
	encoded, err := h.Encode(ctx, marker)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Set(ctx, originalSystemRestartMarkerKey, encoded); err != nil {
		t.Fatal(err)
	}
	marker["userid"] = "changed after encode"
	if err = h.app.Store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	h = command40AdapterStore(t, dir)
	closed = false
	opaque, found, err := h.Get(ctx, originalSystemRestartMarkerKey)
	if err != nil || !found {
		t.Fatalf("cold: %v %v", found, err)
	}
	decoded, err := h.Decode(ctx, opaque)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"channel": "Web", "userid": json.Number("9223372036854775807")}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("lost exact userid: %#v", decoded)
	}
	for _, foreign := range []string{"", "cache", "__system_restart__/../other"} {
		if err = h.Set(ctx, foreign, encoded); err == nil {
			t.Fatal("foreign set admitted")
		}
		if _, _, err = h.Get(ctx, foreign); err == nil {
			t.Fatal("foreign get admitted")
		}
		if err = h.Delete(ctx, foreign); err == nil {
			t.Fatal("foreign delete admitted")
		}
	}
	if err = h.Delete(ctx, originalSystemRestartMarkerKey); err != nil {
		t.Fatal(err)
	}
	if _, found, err = h.Get(ctx, originalSystemRestartMarkerKey); err != nil || found {
		t.Fatalf("delete: %v %v", found, err)
	}
	var unrelated map[string]any
	if err = h.app.Store.Get(ctx, "cache", originalSystemRestartMarkerKey, &unrelated); err != nil || unrelated["unrelated"] != true {
		t.Fatal("unrelated row changed", err, unrelated)
	}
}

func TestSystemCommandMarkerCodecAndLifetimeBoundaries(t *testing.T) {
	ctx := context.Background()
	h := command40AdapterStore(t, t.TempDir())
	t.Cleanup(func() { _ = h.app.Store.Close() })
	for _, bad := range []any{
		map[string]any{"channel": "Web", "userid": "owned", "source": "not persisted"},
		map[string]any{"channel": "Web", "userid": 1.5},
		map[string]any{"channel": "Web", "userid": json.Number("9223372036854775808")},
		map[string]any{"channel": "Web", "userid": []any{"owned"}},
	} {
		if _, err := h.Encode(ctx, bad); err == nil {
			t.Fatal("unsupported producer accepted")
		}
	}
	legacy := `{"channel":"Web","userid":7}`
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Set(ctx, originalSystemRestartMarkerKey, originalSystemMarkerBytes(raw)); err != nil {
		t.Fatal(err)
	}
	opaque, found, err := h.Get(ctx, originalSystemRestartMarkerKey)
	if err != nil || !found {
		t.Fatal(err)
	}
	if value, err := h.Decode(ctx, opaque); err != nil || value != legacy {
		t.Fatal("legacy JSON string rewritten", value, err)
	}
	// Simulated corrupt native row remains an opaque successful Get; decoding
	// is the separate ordinary caught error, not a cache-get failure.
	if _, err = h.app.Store.DB.ExecContext(ctx, `UPDATE records SET payload=? WHERE kind=? AND id=?`, []byte("{"), originalSystemRestartMarkerKind, originalSystemRestartMarkerKey); err != nil {
		t.Fatal(err)
	}
	opaque, found, err = h.Get(ctx, originalSystemRestartMarkerKey)
	if err != nil || !found {
		t.Fatal("corrupt payload prematurely decoded", err)
	}
	_, err = h.Decode(ctx, opaque)
	var source *systemcommands.SourceError
	if !errors.As(err, &source) || source.Class != "ValueError" {
		t.Fatal("native codec classification", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = h.Get(cancelled, originalSystemRestartMarkerKey); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = h.Delete(cancelled, originalSystemRestartMarkerKey); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = h.UnsupportedRestart(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	h.app.closeOriginalBackgroundAdmission()
	if err = h.Delete(ctx, originalSystemRestartMarkerKey); err == nil {
		t.Fatal("closed host admitted delete")
	}
	if err = h.Backup(ctx); err == nil {
		t.Fatal("closed host admitted backup")
	}
	h.app.wg.Wait()
}

func TestSystemCommandNoticeAdapterAndNoRestartAuthority(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t) // No Start: no schedulers, plugin process, HTTP or listener.
	setupTestAdmin(t, a)
	h := &originalSystemCommandAdapters{app: a}
	source := "owned-source"
	initial := systemcommands.Notice{Channel: &systemcommands.Channel{Name: "Web", Value: "Web"}, UserID: "owned", Source: &source, IncludeSource: true, Title: "owned system adapter"}
	fields, err := originalSystemNoticeFields(initial)
	if err != nil || fields["source"] != source || fields["save_history"] != false {
		t.Fatal(fields, err)
	}
	finish := initial
	finish.Source = nil
	finish.IncludeSource = false
	fields, err = originalSystemNoticeFields(finish)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := fields["source"]; present {
		t.Fatal("finish acquired source")
	}
	invalid := initial
	invalid.SaveHistory = true
	if _, err = originalSystemNoticeFields(invalid); err == nil {
		t.Fatal("outgoing history enabled")
	}
	// Real native route with no sender: it records a caught provider failure,
	// not successful delivery and not a thrown router exception.
	if err = h.Notify(ctx, initial); err != nil {
		t.Fatal("caught sender result promoted to exception", err)
	}
	var count int
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE kind='notification'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("outgoing history saved", count, err)
	}
	if value, err := h.UnsupportedRestart(ctx); err != nil || value != false {
		t.Fatal(value, err)
	}
	select {
	case <-a.RestartRequested:
		t.Fatal("adapter emitted restart intent without controller")
	default:
	}
	// Existing backup gate/delegate with an empty owned package root only;
	// full artifact-copy behavior retains its earlier feature21 evidence.
	if err = h.Backup(ctx); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
}

func TestSystemCommandMarkerStorageErrorIsNotCancellation(t *testing.T) {
	ctx := context.Background()
	h := command40AdapterStore(t, t.TempDir())
	encoded, err := h.Encode(ctx, map[string]any{"channel": "Web", "userid": "owned"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.app.Store.Close(); err != nil {
		t.Fatal(err)
	}
	err = h.Set(ctx, originalSystemRestartMarkerKey, encoded)
	var source *systemcommands.SourceError
	if !errors.As(err, &source) || source.Class != "OSError" {
		t.Fatal("save storage failure not ordinary", err)
	}
	if _, found, err := h.Get(ctx, originalSystemRestartMarkerKey); err == nil || found || errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed IO became absent", found, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = h.Set(cancelled, originalSystemRestartMarkerKey, encoded)
	if !errors.Is(err, context.Canceled) || errors.As(err, &source) {
		t.Fatal("cancellation became source failure", err)
	}
	h.app.wg.Wait()
}
