package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	signin "light-media/internal/plugins/autosignin"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

// runOriginalAbsentEngineInit is only the real new-object Init(None) case.
// It runs after supervisor activation and finishes that actual admitted run. It
// does not claim a durable journal completion, create config, or authorize hosts.
func (a *App) runOriginalAbsentEngineInit(ctx context.Context, run PluginRun) (handled bool, err error) {
	call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if !ok || call == nil || call.object == nil || call.object.app != a || call.nativeBefore != nil || run.Action != "init" || run.ConfigRevision != 0 {
		return false, nil
	}
	in := call.object.installation
	if in.PluginID != "AutoSignIn" && in.PluginID != "CleanInvalidSeed" {
		return false, nil
	}
	handled = true
	// A failed check is terminal, never a fallback into a different execution path.
	defer func() {
		state, code := "succeeded", ""
		if err != nil {
			state, code = "failed", "PLUGIN_ORIGINAL_INIT_FAILED"
		}
		err = errors.Join(err, a.pluginStore.FinishRun(ctx, run.ID, state, code, nil))
	}()
	if !originalInitializationMatches(ctx, run.InstanceID, in.PluginID, run.Version, run.Generation, run.Action) || !a.pluginStore.originalInitializationRunBound(run) {
		return true, fmt.Errorf("original absent init identity mismatch")
	}
	bound, ok := a.pluginOriginalInitializingRuns.Load(run.ID)
	if !ok || bound != call {
		return true, fmt.Errorf("original absent init object mismatch")
	}
	current, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		return true, err
	}
	if current.State != "running" || current.InstanceID != in.InstanceID || current.Version != in.Version || current.Generation != in.Generation || current.Action != "init" || current.ConfigRevision != 0 || !bytes.Equal(current.Input, run.Input) {
		return true, fmt.Errorf("original absent init run mismatch")
	}
	var input struct {
		Action string          `json:"action"`
		Config json.RawMessage `json:"config"`
	}
	if json.Unmarshal(current.Input, &input) != nil || input.Action != "init" || !bytes.Equal(bytes.TrimSpace(input.Config), []byte("null")) {
		return true, fmt.Errorf("original absent init snapshot is not None")
	}
	latest, err := a.pluginStore.Installation(ctx, in.InstanceID)
	if err != nil {
		return true, err
	}
	if latest.PluginID != in.PluginID || latest.Version != in.Version || latest.Generation != in.Generation {
		return true, fmt.Errorf("original absent init installation changed")
	}
	if _, err := a.pluginStore.Config(ctx, in.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return true, err
		}
		return true, fmt.Errorf("original absent init config now exists")
	}
	var actual any
	switch in.PluginID {
	case "AutoSignIn":
		zone, e := autoBackupHostTimezone()
		if e != nil {
			return true, e
		}
		engine, e := signin.NewAtZone(signin.Inventory{}, time.Now, zone)
		if e != nil {
			return true, e
		}
		actual = engine
		captureOriginalNativeState(ctx, engine)
		// Defensive assertion: future source changes cannot silently add effects to
		// this deliberately hostless path.
		engine.Effects = func(signin.Effect) error { return fmt.Errorf("unexpected effect in new Init(None)") }
		err = engine.Init(nil)
		engine.Effects = nil
		captureOriginalAppliedConfiguration(ctx, engine.Config)
	case "CleanInvalidSeed":
		machine := seed.New(nil)
		actual = machine
		captureOriginalNativeState(ctx, machine)
		err = machine.Init(ctx, nil)
		captureOriginalAppliedConfiguration(ctx, machine.Config)
	}
	call.object.configMu.Lock()
	call.object.originalAbsentEngine = actual
	call.object.configMu.Unlock()
	return true, err
}

func clearOriginalAbsentEngine(ctx context.Context) {
	if call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall); ok && call != nil && call.object != nil {
		call.object.configMu.Lock()
		call.object.originalAbsentEngine = nil
		call.object.configMu.Unlock()
	}
}
