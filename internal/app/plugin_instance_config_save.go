package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"reflect"
	"slices"
	"time"
)

// A private compiled callback transforms the currently authenticated config.
// It has no store writer. Persistence and initialization use the one shared
// SaveConfigAndQueue transaction, not a separate configuration system.
type pluginConfigurationHTTPPrepare func(context.Context, pluginFormView, plugins.HTTPRequest) (json.RawMessage, json.RawMessage, error)
type pluginConfigurationWriteRegistration struct {
	manifest      plugins.Manifest
	capability    string
	keys          []string
	prepare       pluginConfigurationHTTPPrepare
	result        pluginConfigurationHTTPRead
	verifyApplied func(context.Context, *sql.Tx, PluginRun) error
}

func (a *App) pluginConfigurationHTTPWriter(m plugins.Manifest, api plugins.HTTPAPI, method string) *pluginConfigurationWriteRegistration {
	for _, r := range a.compiledPluginConfigurationWriters() {
		if m.PluginID != r.manifest.PluginID || m.Version != r.manifest.Version || m.SchemaVersion != r.manifest.SchemaVersion || !slices.Contains(r.manifest.Capabilities, r.capability) || !slices.Contains(r.keys, api.Key) {
			continue
		}
		for _, declared := range r.manifest.APIs {
			if reflect.DeepEqual(api, declared) && slices.Contains(declared.Methods, method) {
				return &r
			}
		}
	}
	return nil
}
func (a *App) savePluginInstanceConfiguration(ctx context.Context, in PluginInstallation, authority pluginAPIAuthority, input plugins.HTTPRequest, registration *pluginConfigurationWriteRegistration) (result json.RawMessage, err error) {
	var next, response json.RawMessage
	var anchor pluginFormAnchor
	_, err = a.pluginStore.readPluginInstanceView(ctx, in, func(c context.Context, v pluginFormView) (json.RawMessage, error) {
		if e := a.validatePluginReadAuthority(c, authority); e != nil {
			return nil, e
		}
		// Copy the immutable facts, never retain the session or its write scope.
		anchor = pluginFormSessionFromContext(c).anchor
		anchor.installation.Manifest = append(json.RawMessage(nil), anchor.installation.Manifest...)
		anchor.durable = pluginFormValueCopy(anchor.durable)
		var e error
		next, response, e = registration.prepare(c, v, input)
		if e != nil {
			return nil, e
		}
		return json.RawMessage(`{}`), nil
	})
	if err != nil {
		return nil, err
	}
	if len(next) == 0 {
		return response, nil
	} // Original validation response, no write.
	_, run, err := a.pluginStore.saveConfigAndQueueChecked(ctx, in.InstanceID, anchor.durable.Revision, next, func(c context.Context, tx *sql.Tx) error {
		if e := a.validatePluginHTTPAuthorityTx(c, tx, authority); e != nil {
			return e
		}
		current, e := a.pluginStore.pluginFormAnchorRead(c, tx, in, pluginFormOriginInstalled)
		if e != nil {
			return e
		}
		return comparePluginFormAnchors(anchor, current)
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			readCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			if current, readErr := a.pluginStore.Run(readCtx, run.ID); readErr == nil {
				run = current
			}
			stop()
			err = &pluginConfigurationApplyError{run: run, cause: err}
		}
	}()
	a.wakePlugins()
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	current, err := a.waitPluginConfigurationApplied(wait, in, run, authority)
	if err != nil {
		return nil, err
	}
	if registration.result == nil {
		return response, nil
	}
	owner, e := a.pluginStore.Installation(wait, run.InstanceID)
	if e != nil {
		return nil, e
	}
	return a.pluginStore.readPluginInstanceView(wait, owner, func(c context.Context, v pluginFormView) (json.RawMessage, error) {
		if v.installation.Generation != run.Generation || v.installation.Version != run.Version {
			return nil, errPluginRunAttemptStale
		}
		if e := a.validatePluginReadAuthority(c, authority); e != nil {
			return nil, e
		}
		// Native initializers may advance their own authenticated config
		// without rewriting the immutable common admission/completion.
		// Bind their existing proof to this exact fresh read snapshot.
		if v.durable.Revision != current.ConfigRevision {
			if registration.verifyApplied == nil {
				return nil, errPluginRunAttemptStale
			}
			proofTx, proofErr := a.Store.DB.BeginTx(c, &sql.TxOptions{ReadOnly: true})
			if proofErr != nil {
				return nil, proofErr
			}
			proofErr = a.pluginStore.validatePluginInstanceReadTx(c, proofTx, v.installation)
			if proofErr == nil {
				proofErr = registration.verifyApplied(c, proofTx, current)
			}
			proofTx.Rollback()
			if proofErr != nil {
				return nil, proofErr
			}
		}
		return registration.result(c, v, input)
	})
}

// Wait for an existing initialization run. No replay or new configuration write.
func (a *App) waitPluginConfigurationApplied(wait context.Context, in PluginInstallation, run PluginRun, authority pluginAPIAuthority) (PluginRun, error) {
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		current, e := a.pluginStore.Run(wait, run.ID)
		if e != nil {
			return PluginRun{}, e
		}
		if current.InstanceID != run.InstanceID || current.Generation != run.Generation || current.Action != run.Action {
			return PluginRun{}, errPluginRunAttemptEvidence
		}
		switch current.State {
		case "succeeded":
			tx, e := a.Store.DB.BeginTx(wait, &sql.TxOptions{ReadOnly: true})
			if e != nil {
				return PluginRun{}, e
			}
			head, e := a.pluginStore.runJournal.loadHead(wait, tx, run.InstanceID, run.ID)
			if e == nil {
				expected := pluginRunAdmission{InstanceID: run.InstanceID, PluginID: in.PluginID, Version: run.Version, RunID: run.ID, Action: run.Action, Generation: run.Generation, AdmissionRevision: run.ConfigRevision, InputSHA: sha256.Sum256(run.Input)}
				if head.claim.Admission != expected {
					e = errPluginRunAttemptEvidence
				}
			}
			if e == nil {
				var completion pluginRunAttemptCompletionRecord
				completion, e = a.pluginStore.runJournal.loadCompletion(head)
				if e == nil {
					var actual pluginRunAttemptResult
					actual, e = newPluginRunAttemptResult(current.State, current.ErrorCode, current.Result)
					if e == nil && (actual != completion.Result || current.ConfigRevision != completion.FinalRevision) {
						e = errPluginRunAttemptEvidence
					}
				}
			}
			if e == nil {
				e = a.validatePluginHTTPAuthorityTx(wait, tx, authority)
			}
			tx.Rollback()
			if e != nil {
				return PluginRun{}, e
			}
			return current, nil
		case "failed", "canceled", "cancelled", "reconcile_pending":
			return current, errors.New("plugin configuration initialization failed or awaits recovery")
		}
		select {
		case <-wait.Done():
			return current, wait.Err()
		case <-tick.C:
		}
	}
}

// The config has already committed. This error must not masquerade as a failed
// save or instruct the caller to replay it. The existing runs API is the source
// of eventual application status; a fresh explicit save remains a new action.
type pluginConfigurationApplyError struct {
	run   PluginRun
	cause error
}

func (e *pluginConfigurationApplyError) Error() string { return e.cause.Error() }
func (e *pluginConfigurationApplyError) Unwrap() error { return e.cause }
