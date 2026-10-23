package app

import (
	"context"
	"database/sql"
	"errors"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// Original direct init still needs the native owner's actual configuration
// transition. This publishes the real pending run atomically, without creating
// a config write, calling Init early, or bypassing the native beforeRun barrier.
func (a *App) enqueueOriginalCleanSeedInit(ctx context.Context, instance string) (PluginRun, bool, error) {
	call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if !ok || call == nil || call.object == nil || call.object.app != a || call.object.installation.PluginID != "CleanInvalidSeed" || call.object.installation.InstanceID != instance {
		return PluginRun{}, false, nil
	}
	expected := call.object.installation
	if !originalInitializationMatches(ctx, instance, expected.PluginID, expected.Version, expected.Generation, "init") {
		return PluginRun{}, true, seedhost.ErrUnsafe
	}
	in, err := a.pluginStore.Installation(ctx, instance)
	if err != nil {
		return PluginRun{}, true, err
	}
	if in.PluginID != expected.PluginID || in.Version != expected.Version || in.Generation != expected.Generation || !in.Installed || !in.DesiredEnabled {
		return PluginRun{}, true, seedhost.ErrUnsafe
	}
	// A genuinely new original object with absent configuration has no native
	// transition to publish. Ordinary enqueue retains null and revision zero;
	// the supervised execution hook rechecks absence before real Init(None).
	if call.nativeBefore == nil {
		if _, e := a.pluginStore.Config(ctx, instance); errors.Is(e, sql.ErrNoRows) {
			return PluginRun{}, false, nil
		} else if e != nil {
			return PluginRun{}, true, e
		}
	}
	prepared, err := a.prepareCleanInvalidSeedTransition(ctx, in, "init")
	if err != nil {
		return PluginRun{}, true, err
	}
	defer prepared.release()
	var run PluginRun
	committed := false
	mutationErr := func() error {
		a.pluginLifecycleMu.Lock()
		defer a.pluginLifecycleMu.Unlock()
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		run, err = a.pluginStore.enqueueRunTx(ctx, tx, instance, "init", nil)
		if err != nil {
			return err
		}
		to := seedhost.Binding{InstanceID: instance, Generation: run.Generation, Revision: run.ConfigRevision}
		if err = prepared.publishTx(ctx, tx, to, run.ID); err != nil {
			return err
		}
		// Exactly the ordinary EnqueueRun ordering: bind before visibility so a
		// server executor can only execute this very original object invocation.
		a.pluginOriginalInitializingRuns.Store(run.ID, call)
		a.pluginStore.originalInitializingRuns.Store(run.ID, call)
		err = tx.Commit()
		if err != nil {
			a.pluginOriginalInitializingRuns.Delete(run.ID)
			a.pluginStore.originalInitializingRuns.Delete(run.ID)
			return err
		}
		committed = true
		return nil
	}()
	resolveErr := resolveCleanInvalidSeedPrepared(prepared, committed)
	if resolveErr != nil {
		return run, true, errors.Join(mutationErr, resolveErr)
	}
	return run, true, mutationErr
}
