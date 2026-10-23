package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestPluginFormCommonEventDispatcherDeniesBeforePendingOutbox(t *testing.T) {
	setup := func(t *testing.T) *removeLinkEffectsFixture {
		f := removeLinkEffectsNew(t, map[string]any{"notify": true, "delete_torrents": true})
		f.authorize(t, context.Background())
		cleanupOutboxCommit(t, f, "download_file_deleted")
		cleanupOutboxCommit(t, f, "notification")
		if cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 0 || len(cleanupFileEvents(t, f.a)) != 0 {
			t.Fatal("fixture already dispatched")
		}
		return f
	}
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		t.Run(pluginFormDispatchModeName(mode), func(t *testing.T) {
			f := setup(t)
			ctx, in, session := pluginCommonFormPreparedAttach(t, f.a.pluginStore, mode)
			before := pluginCommonFormRows(t, f.a.pluginStore)
			cleanupCursor, scrapeCursor := f.a.pluginCleanupOutboxAudit, f.a.pluginScrapeOutboxAudit
			files := map[string][]byte{}
			for _, path := range []string{f.history.Source, f.history.Target} {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				files[path] = raw
			}
			// Setup, durable and in-memory snapshots precede the new request.
			requestCtx, requestCancel := context.WithTimeout(context.Background(), 10*time.Second)
			t.Cleanup(requestCancel)
			requestSession, requestErr := f.a.pluginStore.beginPluginFormSession(requestCtx, in, session.origin)
			if requestErr != nil || requestSession == nil || requestSession.resolution.mode != mode {
				t.Fatal("fresh boundary request did not authenticate expected mode", requestErr)
			}
			session = requestSession
			ctx = withPluginFormSession(requestCtx, requestSession)
			result, err := f.a.pluginStore.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
				denied := f.a.dispatchPluginEvents(callCtx, time.Now())
				if pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
					t.Errorf("dispatcher did not deny before transfer: %v", denied)
				}
				return json.RawMessage(`{"swallowed":true}`), nil
			}))
			if result != nil || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
				t.Errorf("dispatcher denial was not sticky: %s %v", result, err)
			}
			if cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 0 || len(cleanupFileEvents(t, f.a)) != 0 {
				t.Error("Form dispatcher published pending source effects")
			}
			if !reflect.DeepEqual(cleanupCursor, f.a.pluginCleanupOutboxAudit) || !reflect.DeepEqual(scrapeCursor, f.a.pluginScrapeOutboxAudit) {
				t.Error("Form dispatcher advanced audit cursors")
			}
			for path, expected := range files {
				raw, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(raw, expected) {
					t.Errorf("dispatcher changed owned fixture media: %v", err)
				}
			}
			pluginCommonFormRowsUnchanged(t, f.a.pluginStore, before)
		})
	}
	t.Run("scope-free-still-transfers-exactly-once", func(t *testing.T) {
		f := setup(t)
		for range 2 {
			if err := f.a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if cleanupEffectsCount(t, f.a, "plugin_cleanup_outbox_handoffs") != 2 || len(cleanupFileEvents(t, f.a)) != 1 {
			t.Fatal("normal dispatcher lost or duplicated pending effects")
		}
		var notices int
		if err := f.a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&notices); err != nil || notices != 1 {
			t.Fatal("normal notification history transfer changed", notices, err)
		}
		for _, path := range []string{f.history.Source, f.history.Target} {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("dispatcher deleted owned fixture file", err)
			}
		}
	})
}
