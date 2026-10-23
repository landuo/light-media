package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// This dispatch happens before the generic HTTP lifecycle lock. Native owner
// pause/drain and resolve must never wait while that lock is held.
func (a *App) scrapeCleanLifecycleRequest(ctx context.Context, path, method string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !pluginStorageID.MatchString(parts[0]) {
		return false
	}
	if !(parts[1] == "config" && method == "PUT" || (parts[1] == "reload" || parts[1] == "reset" || parts[1] == "uninstall") && method == "POST") {
		return false
	}
	in, err := a.pluginStore.Installation(ctx, parts[0])
	return err == nil && in.PluginID == "ScrapeFileClean" && in.Version == "1.0.7"
}
func (a *App) pluginScrapeCleanLifecycleHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	ctx := r.Context()
	parts := strings.Split(path, "/")
	operation := parts[1]
	installation, err := a.pluginStore.Installation(ctx, parts[0])
	if err != nil {
		return nil, err
	}
	if !installation.Installed {
		return nil, &APIError{"NOT_FOUND", "插件已卸载", 404}
	}
	var resetProof *pluginResetAdmission
	if operation == "reset" {
		resetProof, err = a.pluginStore.resetAdmission(ctx, installation.InstanceID)
		if err != nil {
			return nil, err
		}
	}
	var revision int64
	var raw json.RawMessage
	if operation == "config" {
		var body struct {
			Revision int64           `json:"revision"`
			Config   json.RawMessage `json:"config"`
		}
		if err = decodePluginBody(w, r, &body); err != nil {
			return nil, err
		}
		value, err := a.validateNativePluginConfig(installation.PluginID, body.Config)
		if err != nil {
			return nil, err
		}
		raw, err = pluginJSON(value, true)
		if err != nil {
			return nil, err
		}
		revision = body.Revision
	}
	if operation == "reload" && !installation.DesiredEnabled {
		return nil, bad("PLUGIN_MIGRATION_STAGED", "迁移插件仍在暂存，请先核对并保存配置")
	}
	kind := operation
	if kind == "config" {
		kind = "init"
	}
	if resetProof != nil {
		resetProof.setOwnerPending(true)
	}
	prepared, err := a.prepareScrapeFileCleanTransition(ctx, installation, kind)
	if err != nil {
		return nil, err
	}
	defer prepared.release()
	if operation == "config" || operation == "reload" {
		proposed := raw
		if operation == "reload" {
			stored, readErr := a.pluginStore.Config(ctx, installation.InstanceID)
			if readErr != nil {
				_ = resolveScrapeFileCleanPrepared(prepared, false)
				return nil, readErr
			}
			proposed = stored.Value
		}
		if preflightErr := prepared.validateConfig(ctx, proposed, kind); preflightErr != nil {
			if restoreErr := resolveScrapeFileCleanPrepared(prepared, false); restoreErr != nil {
				return nil, restoreErr
			}
			return nil, preflightErr
		}
	}
	if operation == "reset" || operation == "uninstall" {
		if err = prepared.drain(ctx); err != nil {
			if resolveErr := resolveScrapeFileCleanPrepared(prepared, false); resolveErr == nil && resetProof != nil {
				resetProof.setOwnerPending(false)
			}
			return nil, err
		}
	}
	committed := false
	result, mutationErr := func() (any, error) {
		a.pluginLifecycleMu.Lock()
		defer a.pluginLifecycleMu.Unlock()
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		if resetProof != nil {
			defer resetProof.rollback(tx)
			if err = a.pluginStore.requireResetAdmissionTx(ctx, tx, installation.InstanceID); err != nil {
				return nil, err
			}
		} else {
			defer tx.Rollback()
		}
		var currentGeneration int64
		var installed bool
		if err = tx.QueryRowContext(ctx, `SELECT generation,installed FROM plugin_installations WHERE instance_id=? AND plugin_id='ScrapeFileClean' AND version='1.0.7'`, installation.InstanceID).Scan(&currentGeneration, &installed); err != nil {
			return nil, err
		}
		if !installed || currentGeneration != installation.Generation {
			return nil, bad("PLUGIN_SNAPSHOT_STALE", "插件安装状态已变化")
		}
		to := scrapehost.Binding{InstanceID: installation.InstanceID, Generation: installation.Generation}
		var result any
		runID := ""
		switch operation {
		case "config":
			writeCtx := context.WithValue(ctx, scrapeConfigWriteKey{}, scrapeConfigWrite{Prepared: prepared, RawSHA: deferredInputSHA(raw)})
			config, err := a.pluginStore.saveValueTx(writeCtx, tx, installation.InstanceID, "config", "main", revision, raw)
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=1,updated_at=? WHERE instance_id=?`, now(), installation.InstanceID); err != nil {
				return nil, err
			}
			if err = deactivateScrapeFileCleanTx(ctx, tx, installation.InstanceID); err != nil {
				return nil, err
			}
			run, err := a.pluginStore.enqueueRunTx(ctx, tx, installation.InstanceID, "init", nil)
			if err != nil {
				return nil, err
			}
			to.Revision, runID = config.Revision, run.ID
			result = map[string]any{"revision": config.Revision, "run": run}
		case "reload":
			run, err := a.pluginStore.enqueueRunTx(ctx, tx, installation.InstanceID, "reload", nil)
			if err != nil {
				return nil, err
			}
			to.Revision, runID = run.ConfigRevision, run.ID
			result = map[string]any{"run": run}
		case "reset", "uninstall":
			query := `UPDATE plugin_installations SET desired_enabled=0,generation=generation+1,updated_at=? WHERE instance_id=?`
			if operation == "uninstall" {
				query = `UPDATE plugin_installations SET installed=0,desired_enabled=0,generation=generation+1,updated_at=? WHERE instance_id=?`
			}
			if _, err = tx.ExecContext(ctx, query, now(), installation.InstanceID); err != nil {
				return nil, err
			}
			if err = a.pluginStore.recordPluginGenerationLifecycleTx(ctx, tx, installation.InstanceID); err != nil {
				return nil, err
			}
			if operation == "reset" {
				if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=?`, installation.InstanceID); err != nil {
					return nil, err
				}
			} else {
				if err = a.pluginStore.removeOrganizationMemberTx(ctx, tx, installation.InstanceID); err != nil {
					return nil, err
				}
			}
			if err = deactivateScrapeFileCleanTx(ctx, tx, installation.InstanceID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code='PLUGIN_SNAPSHOT_STALE',updated_at=? WHERE instance_id=? AND generation=? AND state IN ('pending','running','reconcile_pending')`, now(), installation.InstanceID, installation.Generation); err != nil {
				return nil, err
			}
			to.Generation++
			result = map[string]bool{"reset": true}
			if operation == "uninstall" {
				result = map[string]bool{"uninstalled": true, "data_retained": pluginUninstallDataRetained(installation)}
			}
		}
		if err = prepared.publishTx(ctx, tx, to, runID); err != nil {
			return nil, err
		}
		if operation == "reset" || operation == "uninstall" {
			if err = a.pluginStore.scrapeCleanGenesisTx(ctx, tx, installation.InstanceID, operation); err != nil {
				return nil, err
			}
		}
		if resetProof != nil {
			err = resetProof.commit(tx)
		} else {
			err = tx.Commit()
		}
		if err != nil {
			return nil, err
		}
		committed = true
		return result, nil
	}()
	resolveErr := resolveScrapeFileCleanPrepared(prepared, committed)
	if resolveErr == nil && resetProof != nil {
		resetProof.setOwnerPending(false)
	}
	if mutationErr != nil && resolveErr == nil {
		return nil, mutationErr
	}
	if resolveErr != nil {
		return nil, resolveErr
	}
	if operation == "config" || operation == "reload" {
		a.wakePlugins()
	}
	return result, nil
}
func deactivateScrapeFileCleanTx(ctx context.Context, tx *sql.Tx, instance string) error {
	for _, query := range []string{`UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, `UPDATE plugin_schedules SET active=0 WHERE instance_id=?`, `UPDATE plugin_event_batches SET state='canceled' WHERE instance_id=? AND run_id IS NULL`} {
		if _, err := tx.ExecContext(ctx, query, instance); err != nil {
			return err
		}
	}
	return nil
}

// Request cancellation cannot guess rollback or abandon an already committed
// marker. Only bounded trusted lifecycle resolution uses this fresh context.
func resolveScrapeFileCleanPrepared(p *pluginScrapeCleanPrepared, committed bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.resolve(ctx, committed)
}
