package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/notice"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"testing"
	"time"
)

// Nil and invalid inputs are intentional: every closed common mutation entry
// must reject the Form capability before dereferencing, preparing, or no-oping.
// Real persistence/notice tests separately establish the effects being blocked.
var pluginClosedCommonEntryNames = []string{
	"NewPluginStore",
	"BeginMigration",
	"SetMigrationState",
	"Import",
	"failPluginResult",
	"patchRunConfig",
	"patchRunConfigTx",
	"replaceRunConfigTx",
	"enqueueDuePluginEventsFor",
	"recordEventAdmissionTx",
	"recordRunOriginTx",
	"ImportReviewedPluginExecutionHandoff",
	"handshake",
	"execute",
	"initializeNativePlugins",
	"executePluginRun",
	"enqueuePluginHTTP",
	"initNotificationChannelsTx",
	"initNotificationTemplatesTx",
	"saveNoticeRouteTx",
	"putNoticePreferenceTx",
	"publishPluginNoticeTx",
	"acceptPluginMessageTx",
	"savePluginMessageTx",
	"commitPluginMessage",
	"sendPluginMessageModules",
	"attemptPluginMessage",
	"invokePluginNoticeSender",
	"drainPluginMessages",
	"writePluginMessageIntentTx",
	"finishPluginMessageRowTx",
	"commitPluginMessageRow",
	"step",
	"finish",
	"runNoticeRoute",
	"runNoticeRouteLocked",
	"resumePluginNoticeRoutes",
	"runNoticeBusinessV2",
}

func pluginClosedCommonNilCall(ctx context.Context, name string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("entry reached pre-guard preparation: %v", recovered)
		}
	}()
	var p *PluginStore
	var a *App
	var d *pluginDeferredCall
	var n *pluginNoticeDriver
	switch name {
	case "NewPluginStore":
		_, err = NewPluginStore(ctx, nil)
	case "BeginMigration":
		_, err = p.BeginMigration(ctx, "", "", "")
	case "SetMigrationState":
		err = p.SetMigrationState(ctx, "", "", "", nil)
	case "Import":
		_, err = p.Import(ctx, PluginImport{})
	case "failPluginResult":
		err = p.failPluginResult(ctx, "", "")
	case "patchRunConfig":
		err = p.patchRunConfig(ctx, nil, nil)
	case "patchRunConfigTx":
		err = p.patchRunConfigTx(ctx, nil, nil, nil)
	case "replaceRunConfigTx":
		err = p.replaceRunConfigTx(ctx, nil, nil, nil)
	case "enqueueDuePluginEventsFor":
		err = p.enqueueDuePluginEventsFor(ctx, time.Time{}, "")
	case "recordEventAdmissionTx":
		err = p.recordEventAdmissionTx(ctx, nil, "", PluginRun{}, nil, nil)
	case "recordRunOriginTx":
		err = p.recordRunOriginTx(ctx, nil, "", PluginRun{}, nil)
	case "ImportReviewedPluginExecutionHandoff":
		err = p.ImportReviewedPluginExecutionHandoff(ctx, "", nil)
	case "handshake":
		_, err = d.handshake(ctx, plugins.Identity{}, "", nil)
	case "execute":
		_, err = d.execute(ctx, nil)
	case "initializeNativePlugins":
		err = a.initializeNativePlugins(ctx)
	case "executePluginRun":
		a.executePluginRun(ctx, PluginRun{})
		err = pluginFormSessionFromContext(ctx).validateFinal(ctx)
	case "enqueuePluginHTTP":
		_, err = a.enqueuePluginHTTP(ctx, PluginInstallation{}, plugins.HTTPRequest{}, pluginAPIScope{})
	case "initNotificationChannelsTx":
		err = p.initNotificationChannelsTx(ctx, nil)
	case "initNotificationTemplatesTx":
		err = p.initNotificationTemplatesTx(ctx, nil)
	case "saveNoticeRouteTx":
		err = p.saveNoticeRouteTx(ctx, nil, pluginNoticeRoute{})
	case "putNoticePreferenceTx":
		err = p.putNoticePreferenceTx(ctx, nil, "", "", nil)
	case "publishPluginNoticeTx":
		_, err = a.publishPluginNoticeTx(ctx, nil, pluginNoticeRoute{}, 0, notice.Object{})
	case "acceptPluginMessageTx":
		_, err = a.acceptPluginMessageTx(ctx, nil, pluginNoticeRoute{}, 0, notice.Call{})
	case "savePluginMessageTx":
		err = p.savePluginMessageTx(ctx, nil, pluginNoticeQueueEntry{})
	case "commitPluginMessage":
		err = p.commitPluginMessage(ctx, nil, "", pluginNoticeQueueEntry{})
	case "sendPluginMessageModules":
		_, err = a.sendPluginMessageModules(ctx, pluginNoticeQueueEntry{})
	case "attemptPluginMessage":
		err = a.attemptPluginMessage(ctx, "")
	case "invokePluginNoticeSender":
		_, err = invokePluginNoticeSender(ctx, nil, pluginNoticeQueueEntry{})
	case "drainPluginMessages":
		err = a.drainPluginMessages(ctx, 0)
	case "writePluginMessageIntentTx":
		_, err = p.writePluginMessageIntentTx(ctx, nil, pluginNoticeQueueEntry{})
	case "finishPluginMessageRowTx":
		_, err = p.finishPluginMessageRowTx(ctx, nil, pluginNoticeQueueRow{}, pluginNoticeQueueEntry{})
	case "commitPluginMessageRow":
		err = p.commitPluginMessageRow(ctx, nil, "", pluginNoticeQueueRow{})
	case "step":
		_, err = n.step(ctx, "", nil, nil)
	case "finish":
		err = n.finish(ctx, nil)
	case "runNoticeRoute":
		err = a.runNoticeRoute(ctx, "")
	case "runNoticeRouteLocked":
		err = a.runNoticeRouteLocked(ctx, "")
	case "resumePluginNoticeRoutes":
		err = a.resumePluginNoticeRoutes(ctx, 0)
	case "runNoticeBusinessV2":
		err = a.runNoticeBusinessV2(ctx, pluginNoticeRoute{})
	default:
		return fmt.Errorf("unregistered common entry %q", name)
	}
	return err
}
func TestPluginFormClosedCommonEntriesDenyBeforePreparation(t *testing.T) {
	for _, mode := range []pluginFormMode{pluginFormPreview, pluginFormInstalledCold, pluginFormInstalledStarted, pluginFormInstalledUnknown} {
		for _, name := range pluginClosedCommonEntryNames {
			t.Run(pluginFormDispatchModeName(mode)+"/"+name, func(t *testing.T) {
				started := time.Now()
				phase := func(label string, phaseCtx context.Context) {
					deadline, hasDeadline := phaseCtx.Deadline()
					remaining := time.Duration(0)
					if hasDeadline {
						remaining = time.Until(deadline)
					}
					t.Logf("phase=%s elapsed=%s has_deadline=%v remaining=%s ctx_err=%v", label, time.Since(started), hasDeadline, remaining, phaseCtx.Err())
				}
				phase("before-fixture", context.Background())
				p, ctx, in, session := pluginClosedCommonPreparedFixture(t, mode)
				phase("fixture-ready", ctx)
				before := pluginCommonFormRows(t, p)
				phase("snapshot-ready", ctx)
				// Fixture construction and full database evidence are preparation,
				// not the application request. Start a genuinely new request and
				// authenticate its own session; never extend/reuse the old one.
				requestCtx, requestCancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(requestCancel)
				phase("request-start", requestCtx)
				requestSession, requestErr := p.beginPluginFormSession(requestCtx, in, session.origin)
				if requestErr != nil || requestSession == nil || requestSession.resolution.mode != mode {
					t.Fatal("fresh request could not authenticate the exact fixture mode", requestErr)
				}
				session = requestSession
				ctx = withPluginFormSession(requestCtx, requestSession)
				phase("request-ready", ctx)
				result, err := p.dispatchPluginForm(ctx, in, pluginFormDispatchCallbacks(func(callCtx context.Context, _ pluginFormView) (json.RawMessage, error) {
					phase("callback-enter", callCtx)
					defer phase("callback-exit", callCtx)
					if denied := pluginClosedCommonNilCall(callCtx, name); pluginErrorCode(denied) != "PLUGIN_CAPABILITY_DENIED" {
						t.Errorf("entry did not deny before preparation: %v", denied)
					}
					return json.RawMessage(`{"swallowed":true}`), nil
				}))
				phase("dispatch-return", ctx)
				if result != nil || pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" || pluginErrorCode(session.validateFinal(ctx)) != "PLUGIN_CAPABILITY_DENIED" {
					t.Errorf("entry denial was not sticky: %s %v", result, err)
				}
				pluginCommonFormRowsUnchanged(t, p, before)
			})
		}
	}
}

// Setup is not an HTTP request. Its bounded 90s test preparation allowance
// accommodates observed race fixture costs of 18.55s and 21.52s. The caller
// creates a separate 5s authenticated request AFTER setup and snapshots.
func pluginClosedCommonPreparedFixture(t *testing.T, mode pluginFormMode) (*PluginStore, context.Context, PluginInstallation, *pluginFormSession) {
	t.Helper()
	if mode != pluginFormInstalledStarted && mode != pluginFormInstalledUnknown {
		return pluginFormDispatchFixture(t, builtin.SystemProcessManifest(), mode, false)
	}
	started := time.Now()
	setupCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	stage := func(name string) {
		t.Logf("setup_phase=%s elapsed=%s ctx_err=%v", name, time.Since(started), setupCtx.Err())
	}
	_, p := pluginStoreFixture(t)
	p.store.DB.SetMaxOpenConns(1)
	stage("store-ready")
	manifest := builtin.SystemProcessManifest()
	installCloneSource(t, p, manifest)
	stage("installed")
	if _, err := p.SaveConfig(setupCtx, manifest.PluginID, 0, json.RawMessage(`{"enable":false,"opaque":{"kept":true}}`)); err != nil {
		t.Fatal("setup config", err)
	}
	stage("configured")
	in, err := p.Installation(setupCtx, manifest.PluginID)
	if err != nil {
		t.Fatal("setup installation", err)
	}
	if mode == pluginFormInstalledStarted {
		stored, err := p.Config(setupCtx, in.InstanceID)
		if err != nil {
			t.Fatal("setup read", err)
		}
		_, queued, err := p.SaveConfigAndQueue(setupCtx, in.InstanceID, stored.Revision, stored.Value)
		if err != nil {
			t.Fatal("setup queue", err)
		}
		stage("queued")
		claimed, err := p.ClaimRun(setupCtx)
		if err != nil || claimed.ID != queued.ID {
			t.Fatal("setup claim", err)
		}
		stage("claimed")
		if err := p.FinishRun(setupCtx, claimed.ID, "succeeded", "", nil); err != nil {
			t.Fatal("setup finish", err)
		}
		stage("finished")
	} else {
		lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
	}
	in, err = p.Installation(setupCtx, in.InstanceID)
	if err != nil {
		t.Fatal("setup final installation", err)
	}
	// This preparation session is only a mode/origin assertion and is never
	// dispatched. The request later authenticates its own session and deadline.
	session, err := p.beginPluginFormSession(setupCtx, in, pluginFormOriginInstalled)
	if err != nil || session.resolution.mode != mode {
		t.Fatal("setup mode", err)
	}
	stage("authenticated")
	return p, setupCtx, in, session
}
