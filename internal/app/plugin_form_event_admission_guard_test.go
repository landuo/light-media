package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins/builtin"
)

// This explicitly reaches the new shared INNER admission helper. Testing only
// enqueueRunTx would conceal a scope bypass introduced by the V7/V11 merge.
func TestPluginFormDispatchMediaAdmissionInnerHelperCannotBypassScope(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, withCursor := range []bool{false, true} {
			name := pluginFormDispatchModeName(mode) + "/nil-cursor"
			if withCursor {
				name = pluginFormDispatchModeName(mode) + "/captured-cursor"
			}
			t.Run(name, func(t *testing.T) {
				p, ctx, in, session := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
				var before int
				if err := p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				_, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, view pluginFormView) (json.RawMessage, error) {
					tx, err := p.store.DB.BeginTx(callCtx, nil)
					if err != nil {
						return nil, err
					}
					defer tx.Rollback()
					var cursor *mediaRefreshAdmissionCursor
					if withCursor {
						cursor = &mediaRefreshAdmissionCursor{after: 0}
					}
					_, err = p.enqueueRunWithMediaCursorTx(callCtx, tx, view.installation.InstanceID, "page", nil, cursor)
					if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
						t.Fatal("inner helper bypassed lifecycle scope", err)
					}
					if err = tx.Commit(); err != nil {
						return nil, err
					}
					return json.RawMessage(`{}`), nil // The denied attempt must remain sticky.
				}))
				if pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
					t.Fatal("ignored inner helper denial was not sticky", err)
				}
				var after int
				if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&after); err != nil || after != before {
					t.Fatal("inner helper queued work", before, after, err)
				}
				current, err := p.pluginFormAnchor(context.Background(), session.requested, session.origin)
				if err != nil || comparePluginFormAnchors(session.anchor, current) != nil {
					t.Fatal("inner helper changed captured state", err)
				}
			})
		}
	}
}

func TestPluginFormDispatchScopeFreeMediaAdmissionInnerHelperRemainsLegal(t *testing.T) {
	p, ctx, in, _ := pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), pluginFormInstalledCold, false)
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	run, err := p.enqueueRunWithMediaCursorTx(ctx, tx, in.InstanceID, "page", nil, nil)
	if err != nil {
		t.Fatal("scope-free inner helper denied", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = p.store.DB.QueryRow(`SELECT state FROM plugin_runs WHERE id=? AND instance_id=?`, run.ID, in.InstanceID).Scan(&state); err != nil || state != "pending" {
		t.Fatal("scope-free enqueue did not persist pending work", state, err)
	}
}
