package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
)

func TestPluginFormViewCopiesAuthenticatedImages(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false,"opaque":{"kept":true}}`))
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	formCtx := withPluginFormSession(ctx, session)
	requested := in
	requested.Manifest = json.RawMessage(`{"caller":"not-authority"}`)
	view, err := p.trustedPluginFormView(formCtx, requested)
	if err != nil {
		t.Fatal(err)
	}
	if view.mode != pluginFormInstalledCold || !view.configExists || !bytes.Equal(view.installation.Manifest, session.resolution.installation.Manifest) || bytes.Equal(view.installation.Manifest, requested.Manifest) {
		t.Fatal("view used caller metadata or lost authenticated state")
	}
	before := pluginFormValueCopy(session.durable)
	view.durable.Value[0] = '!'
	assertPluginFormSessionValue(t, view.display, before)
	view.display.Value[0] = '!'
	view.installation.Manifest[0] = '!'
	view.installation.Generation++
	view.mode, view.configExists = pluginFormPreview, false
	again, err := p.trustedPluginFormView(formCtx, in)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginFormSessionValue(t, again.durable, before)
	assertPluginFormSessionValue(t, again.display, before)
	if again.mode != pluginFormInstalledCold || !again.configExists || again.installation.Generation != in.Generation || !bytes.Equal(again.installation.Manifest, in.Manifest) {
		t.Fatal("returned view mutated the session")
	}
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPluginFormViewRejectsMissingScopeAndIdentity(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{}`))
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	_, other := pluginStoreFixture(t)
	formCtx := withPluginFormSession(ctx, session)
	for _, tc := range []struct {
		name  string
		store *PluginStore
		ctx   context.Context
	}{
		{"nil-context", p, nil}, {"no-session", p, ctx}, {"nil-session", p, withPluginFormSession(ctx, nil)}, {"wrong-value", p, context.WithValue(ctx, pluginFormSessionKey{}, "not-a-session")}, {"other-store", other, formCtx}, {"nil-store", nil, formCtx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.store.trustedPluginFormView(tc.ctx, in); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
				t.Fatal("unbound view accepted", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*PluginInstallation)
	}{
		{"instance", func(i *PluginInstallation) { i.InstanceID += ".other" }},
		{"plugin", func(i *PluginInstallation) { i.PluginID = "CategoryEditor" }},
		{"version", func(i *PluginInstallation) { i.Version = "0.0" }},
		{"schema", func(i *PluginInstallation) { i.ManifestSchemaVersion++ }},
		{"generation", func(i *PluginInstallation) { i.Generation++ }},
		{"installed", func(i *PluginInstallation) { i.Installed = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := in
			tc.change(&requested)
			if _, err := p.trustedPluginFormView(formCtx, requested); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
				t.Fatal("different identity accepted", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(formCtx)
	cancel()
	if _, err = p.trustedPluginFormView(canceled, in); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled view accepted", err)
	}
}

func TestPluginFormViewCannotClearStickyHostFailure(t *testing.T) {
	for _, cause := range []error{bad("PLUGIN_REVISION_CONFLICT", "synthetic-private-message"), errors.New("synthetic SQL failure")} {
		p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{}`))
		session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
		if err != nil {
			t.Fatal(err)
		}
		session.captureHostError(cause)
		original := *session.hostFailure
		view, err := p.trustedPluginFormView(withPluginFormSession(ctx, session), in)
		var api *APIError
		if !errors.As(err, &api) || api.Code != original.Code || api.Message != original.Message || view.durable.Value != nil || view.installation.Manifest != nil {
			t.Fatal("failed host call was washed into a usable view")
		}
		api.Code, api.Message, api.Status = "MUTATED", "mutated", 599
		_, err = p.trustedPluginFormView(withPluginFormSession(ctx, session), in)
		if !errors.As(err, &api) || *api != original || *session.hostFailure != original {
			t.Fatal("returned failure aliases the latch")
		}
		if pluginErrorCode(session.validateFinal(ctx)) != original.Code {
			t.Fatal("view cleared final failure")
		}
	}
}

func TestPluginFormViewAdvancesOnlyWithCommittedSourceWrites(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	formCtx := withPluginFormSession(ctx, session)
	before, err := p.trustedPluginFormView(formCtx, in)
	if err != nil {
		t.Fatal(err)
	}
	initial := pluginFormValueCopy(before.durable)
	next, err := p.NormalizeInstanceDefaults(formCtx, in, before.durable.Revision, map[string]json.RawMessage{"new_default": json.RawMessage(`false`)})
	if err != nil {
		t.Fatal(err)
	}
	after, err := p.trustedPluginFormView(formCtx, in)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginFormSessionValue(t, after.durable, next)
	assertPluginFormSessionValue(t, after.display, next)
	if before.durable.Revision+1 != after.durable.Revision || bytes.Equal(before.durable.Value, after.durable.Value) || !after.configExists {
		t.Fatal("view did not advance after actual commit")
	}
	_, err = p.NormalizeInstanceDefaults(formCtx, in, after.durable.Revision, map[string]json.RawMessage{"new_default": json.RawMessage(`true`)})
	if err != nil {
		t.Fatal(err)
	}
	noop, err := p.trustedPluginFormView(formCtx, in)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginFormSessionValue(t, noop.durable, after.durable)
	equal, err := pluginFormMechanicalSave(t, p, formCtx, in, after.durable.Revision, after.durable.Value, func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error {
		inside, err := p.trustedPluginFormView(formCtx, in)
		if err != nil {
			return err
		}
		assertPluginFormSessionValue(t, inside.durable, after.durable)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := p.trustedPluginFormView(formCtx, in)
	if err != nil || committed.durable.Revision != after.durable.Revision+1 {
		t.Fatal("equal source Save was not reflected after commit", err)
	}
	assertPluginFormSessionValue(t, committed.durable, equal)
	assertPluginFormSessionValue(t, before.durable, initial)
}

func TestPluginFormViewPreviewRetainsAuthenticatedPrefill(t *testing.T) {
	for _, tombstone := range []bool{false, true} {
		_, p := pluginStoreFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		manifest := builtin.SystemProcessManifest()
		raw, _ := json.Marshal(manifest)
		request := PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}
		want := PluginStoredValue{Value: json.RawMessage(`{}`)}
		if tombstone {
			installCloneSource(t, p, manifest)
			var err error
			want, err = p.SaveConfig(ctx, request.InstanceID, 0, json.RawMessage(`{"retained":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if err = p.Uninstall(ctx, request.InstanceID); err != nil {
				t.Fatal(err)
			}
		}
		session, err := p.beginPluginFormSession(ctx, request, pluginFormOriginInstall)
		if err != nil {
			t.Fatal(err)
		}
		view, err := p.trustedPluginFormView(withPluginFormSession(ctx, session), request)
		if err != nil || view.mode != pluginFormPreview || view.installation.Generation != 0 || view.installation.Installed || view.configExists != tombstone {
			t.Fatal("preview fabricated installed authority", err)
		}
		assertPluginFormSessionValue(t, view.durable, want)
		assertPluginFormSessionValue(t, view.display, want)
		view.durable.Value[0] = '!'
		view.installation.Manifest[0] = '!'
		if err = session.validateFinal(ctx); err != nil {
			t.Fatal("preview view changed retained state", err)
		}
		if err = session.admit(ctx, p.store.DB, request, want); err == nil {
			t.Fatal("preview view granted write authority")
		}
	}
}

func TestPluginFormViewIsCapturedReadNotTransactionAuthority(t *testing.T) {
	p, ctx, in := pluginFormSessionFixture(t, builtin.SystemProcessManifest(), json.RawMessage(`{"enable":false}`))
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	formCtx := withPluginFormSession(ctx, session)
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	changed, err := p.saveValueTx(ctx, tx, in.InstanceID, "config", "main", session.durable.Revision, json.RawMessage(`{"external":true}`))
	if err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(formCtx, time.Second)
	defer cancel()
	view, err := p.trustedPluginFormView(readCtx, in)
	if err != nil {
		t.Fatal("view attempted SQL re-entry on single connection", err)
	}
	assertPluginFormSessionValue(t, view.durable, session.durable)
	if view.durable.Revision == changed.Revision {
		t.Fatal("captured view silently became a new DB snapshot")
	}
	if err = session.admit(ctx, tx, in, changed); pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("captured view bypassed same-TX admission", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = session.validateFinal(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPluginFormViewKeepsDisplaySeparateFromDurable(t *testing.T) {
	manifest, _, known := builtin.Lookup("PluginUnInstall")
	if !known {
		t.Fatal("compiled uninstall fixture missing")
	}
	p, ctx, in := pluginFormSessionFixture(t, manifest, json.RawMessage(`{"plugin_ids":["SystemProcess"]}`))
	before, sealed := pluginDefaultsStoredConfig(t, p, in.InstanceID)
	p.cacheUninstallConfig(in.InstanceID, before.Revision, in.Generation, json.RawMessage(`{"plugin_ids":[]}`))
	session, err := p.beginPluginFormSession(ctx, in, pluginFormOriginInstalled)
	if err != nil {
		t.Fatal(err)
	}
	view, err := p.trustedPluginFormView(withPluginFormSession(ctx, session), in)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginFormSessionValue(t, view.durable, before)
	if string(view.display.Value) != `{"plugin_ids":[]}` || view.display.Revision != before.Revision {
		t.Fatal("view lost the display-only projection")
	}
	view.display.Value[0] = '!'
	assertPluginFormSessionValue(t, view.durable, before)
	assertPluginFormSessionValue(t, session.durable, before)
	if string(session.display.Value) != `{"plugin_ids":[]}` {
		t.Fatal("view aliases the captured display")
	}
	assertPluginFormDurableConfigUnchanged(t, p, ctx, in.InstanceID, before, sealed)
}
