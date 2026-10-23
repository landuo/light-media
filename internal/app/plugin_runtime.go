package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autobackup"
	"light-media/internal/plugins/autosignin"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/cleaninvalidseed"
	cf "light-media/internal/plugins/cloudflarespeedtest"
	lark "light-media/internal/plugins/larkmessager"
	"light-media/internal/plugins/removelink"
	"light-media/internal/plugins/scrapefileclean"
	sr "light-media/internal/plugins/seedrescue"
	transfer "light-media/internal/plugins/torrenttransfer"
	"light-media/internal/plugins/uninstall"
)

func (a *App) pluginExecutable(pluginID string) (plugins.Command, error) {
	if a.pluginCommand != nil {
		return a.pluginCommand(pluginID)
	}
	path, err := os.Executable()
	if err != nil {
		return plugins.Command{}, bad("PLUGIN_EXECUTABLE_UNAVAILABLE", "插件执行程序不可用")
	}
	return plugins.Command{Path: path, Args: []string{"--plugin-worker", pluginID}}, nil
}

func (a *App) wakePlugins() {
	select {
	case a.pluginWake <- struct{}{}:
	default:
	}
}

func (a *App) initializeNativePlugins(ctx context.Context) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	installations, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return err
	}
	for _, installation := range installations {
		// Import staging must never reapply hosts or replay one-shot config.
		if !installation.DesiredEnabled {
			continue
		}
		if err := a.pluginStore.requirePluginUpdateActivation(ctx, a.Store.DB, installation.InstanceID); err != nil {
			if pluginErrorCodeInternal(err) == "PLUGIN_UPDATE_REVIEW_REQUIRED" {
				a.log("WARN", "plugins", "update_activation_review_pending", "插件版本已迁移，等待核对并显式保存该实例设置；未重放旧执行意图", "", "")
				continue
			}
			return err
		}
		if siteReferencePlugin(installation.PluginID) {
			if _, e := a.pluginStore.readSiteReferenceProtocol(ctx, a.Store.DB, installation); e != nil {
				if code := pluginErrorCodeInternal(e); code != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" && code != "PLUGIN_SITE_PROTOCOL_INVALID" {
					return e
				}
				a.log("ERROR", "plugins", "site_reference_protocol_pending", "插件站点引用尚未确认，已保留旧配置和待执行任务；其他插件继续启动", "", "")
				continue
			}
		}
		manifest, _, exists := builtin.Lookup(installation.PluginID)
		if !exists || manifest.Version != installation.Version {
			continue
		}
		config, err := a.pluginStore.Config(ctx, installation.InstanceID)
		if missingPluginValue(err) {
			continue
		}
		if err != nil {
			return err
		}
		if installation.PluginID == "AutoSignIn" {
			initialize, err := a.shouldInitializeAutoSignIn(ctx, installation, config)
			if err != nil {
				a.log("ERROR", "plugins", "signin_initialize_pending", "签到存在待核对状态，未重新初始化；其他插件继续启动", "", "")
				continue
			}
			if !initialize {
				continue
			}
		}
		if installation.PluginID == "ScrapeFileClean" {
			initialize, err := a.shouldInitializeScrapeFileClean(ctx, installation, config)
			if err != nil {
				a.log("ERROR", "plugins", "cleanup_initialize_pending", "文件清理存在待核对状态，未重新初始化；其他插件继续启动", "", "")
				continue
			}
			if !initialize {
				continue
			}
		}

		if installation.PluginID == "RemoveLink" {
			initialize, err := a.shouldInitializeRemoveLink(ctx, installation, config)
			if err != nil {
				a.log("ERROR", "plugins", "cleanup_initialize_pending", "文件清理存在待核对状态，未重新初始化；其他插件继续启动", "", "")
				continue
			}
			if !initialize {
				continue
			}
		}
		if installation.PluginID == "CleanInvalidSeed" {
			initialize, err := a.shouldInitializeCleanInvalidSeed(ctx, installation, config)
			if err != nil {
				a.log("ERROR", "plugins", "clean_seed_initialize_pending", "无效做种清理存在待核对状态，未重新初始化；其他插件继续启动", "", "")
				continue
			}
			if !initialize {
				continue
			}
		}
		if installation.PluginID == cf.PluginID {
			initialize, e := a.shouldInitializeCloudflare(ctx, installation, config)
			if e != nil {
				a.log("ERROR", "plugins", "cloudflare_initialize_pending", "Cloudflare存在待核对状态或证据缺失，未重新初始化", "", "")
				continue
			}
			if !initialize {
				continue
			}
		}
		if installation.PluginID == "AutoBackup" {
			initialize, e := a.shouldInitializeAutoBackup(ctx, installation, config)
			if e != nil {
				switch autoBackupErrorCode(e) {
				case "AUTO_BACKUP_EVIDENCE_MISSING", "AUTO_BACKUP_JOURNAL_INVALID", "AUTO_BACKUP_UNCERTAIN":
					a.log("ERROR", "plugins", "backup_initialize_pending", "自动备份存在待核对状态或执行证据缺失，未重新初始化；其他插件继续启动", "", "")
					continue
				default:
					return e
				}
			}
			if !initialize {
				continue
			}
		}
		if installation.PluginID == bf.PluginID {
			initialize, e := a.shouldInitializeBrushFlow(ctx, installation, config)
			if e != nil {
				a.log("ERROR", "plugins", "brush_flow_initialize_pending", "稀缺保种存在待核对状态或证据缺失，未重新初始化", "", "")
				continue
			}
			if !initialize {
				continue
			}
		}
		if installation.PluginID == sr.PluginID {
			initialize, e := a.shouldInitializeSeedRescue(ctx, installation, config)
			if e != nil {
				a.log("ERROR", "plugins", "seed_rescue_initialize_pending", "稀缺保种存在待核对状态或证据缺失，未重新初始化", "", "")
				continue
			}
			if !initialize {
				continue
			}
		}
		if installation.PluginID == "CategoryEditor" {
			var state struct {
				Enabled bool `json:"enabled"`
			}
			if json.Unmarshal(config.Value, &state) != nil || !state.Enabled {
				continue
			}
		}
		var existing int
		// A fresh startup init must not replay an init/reload whose worker ran
		// but whose result could not be persisted. A later explicit config save
		// has a new revision and can initialize normally after user review.
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs r
WHERE instance_id=? AND generation=? AND config_revision=? AND (
 state IN ('pending','running','reconcile_pending') OR (
  state='failed' AND (
   (action IN ('init','reload') AND (
    error_code IN ('PLUGIN_RESULT_SAVE_FAILED','PLUGIN_RESULT_TOO_LARGE','INVALID_PLUGIN_RESULT') OR
    EXISTS(SELECT 1 FROM plugin_run_startup_blocks b WHERE b.run_id=r.id)
   )) OR EXISTS(SELECT 1 FROM plugin_deferred_operations d WHERE d.run_id=r.id)
  )
 )
)`, installation.InstanceID, installation.Generation, config.Revision).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			continue
		}
		if _, err := a.pluginStore.EnqueueRun(ctx, installation.InstanceID, "init", nil); err != nil {
			if pluginHandoffPending(err) {
				a.log("WARN", "plugins", "source_execution_handoff_pending", "原版执行程序尚未完成交接，迁入插件保持不运行；其他插件继续启动", "", "")
				continue
			}
			return err
		}
	}
	return nil
}

func (a *App) pluginWorker(ctx context.Context) {
	// Brush source tasks own task/global locks and exact durable run leases.
	// Waiting for their native IO here would serialize unrelated source tasks.
	var brush sync.WaitGroup
	defer brush.Wait()
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		budget := &brushFlowClaimBudget{pools: a.brushFlowPools()}
		claimCtx := context.WithValue(ctx, brushFlowClaimBudgetKey{}, budget)
		run, err := a.pluginStore.ClaimRun(claimCtx)
		if err == nil {
			if a.brushFlowBackgroundRun(ctx, run) {
				brush.Add(1)
				go func() {
					defer brush.Done()
					defer func() {
						if budget.release != nil {
							budget.release()
						}
						a.wakePlugins()
					}()
					if e := a.executePluginRun(ctx, run); e != nil {
						a.log("ERROR", "plugins", "execution_unresolved", "插件执行生命周期尚未完成，请核对执行记录", "", "")
					}
				}()
				continue
			}
			if e := a.executePluginRun(ctx, run); e != nil {
				a.log("ERROR", "plugins", "execution_unresolved", "插件执行生命周期尚未完成，请核对执行记录", "", "")
			}
			if budget.release != nil {
				budget.release()
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			a.log("ERROR", "plugins", "claim_failed", "读取插件执行队列失败", "", "")
		}
		select {
		case <-ctx.Done():
			return
		case <-a.pluginWake:
		case <-timer.C:
		}
	}
}

func (a *App) executePluginRun(ctx context.Context, run PluginRun) (runtimeErr error) {
	if call, ok := a.pluginOriginalInitializingRuns.Load(run.ID); ok {
		ctx = context.WithValue(ctx, originalInitializingObjectKey{}, call.(*originalInitializationCall))
	}
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	// This is the outermost admitted-execution cleanup. A lease cannot finish
	// before the SDK, deferred business owner and process slot have all exited.
	// Business host calls can replace run with a freshly authenticated durable
	// snapshot. That revision projection must not replace the private admission
	// capability owned by this outer execution frame.
	claimedRun := run
	defer func() {
		if releaseErr := a.pluginStore.releaseSupervisedRun(claimedRun); runtimeErr == nil {
			runtimeErr = releaseErr
		}
	}()
	supervisedCtx, err := a.pluginStore.activateSupervisedRun(ctx, run)
	if err != nil {
		if errors.Is(err, errPluginRunCanceled) {
			finish, cancel := context.WithTimeout(context.WithoutCancel(supervisedCtx), 3*time.Second)
			defer cancel()
			if finishErr := a.pluginStore.finalizeSupervisedUnentered(finish, run); finishErr != nil {
				return errors.Join(err, finishErr)
			}
		}
		return err
	}
	ctx = supervisedCtx
	if handled, err := a.runOriginalAbsentEngineInit(ctx, run); handled {
		return err
	}
	if run.RequestID != "" {
		ctx = a.traceContext(ctx, "", "", run.RequestID)
	}
	lifecycleCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	ctx, cancelPrepare := context.WithTimeout(lifecycleCtx, 2*time.Minute)
	defer cancelPrepare()
	processHeld := false
	releaseProcess := func() {
		if processHeld {
			<-a.pluginProcess
			processHeld = false
		}
	}
	defer releaseProcess()
	select {
	case a.pluginProcess <- struct{}{}:
		processHeld = true
	case <-ctx.Done():
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		// No SDK process or deferred business owner was entered. Persist that
		// exact fact before releasing the lease, retaining source-specific proof.
		if err := a.pluginStore.finalizeSupervisedUnentered(finish, run); err != nil {
			return err
		}
		return ctx.Err()
	}
	installation, err := a.pluginStore.Installation(ctx, run.InstanceID)
	var manifest plugins.Manifest
	if err == nil {
		err = json.Unmarshal(installation.Manifest, &manifest)
	}
	implementation, implementationErr := a.resolvePluginInvocation(ctx, installation)
	compiled, available := implementation.Manifest, implementationErr == nil
	if err == nil && (manifest.PluginID != installation.PluginID || manifest.Version != installation.Version || manifest.SchemaVersion != installation.ManifestSchemaVersion) {
		err = bad("PLUGIN_MANIFEST_MISMATCH", "插件清单与固定安装版本不一致")
	}
	if err == nil && available {
		// Native workers are part of this executable. The compiled catalog is
		// the grant authority; an older stored same-source-version manifest must
		// neither grant new powers nor hide capabilities added with this host.
		manifest = compiled
	}
	if err == nil && run.Action == "api" {
		err = a.validatePluginHTTPRun(ctx, run)
	}
	var command plugins.Command
	if err == nil && available {
		command = implementation.Command
	} else if err == nil {
		err = bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "当前版本没有该插件的可执行实现")
	}
	if err == nil && run.Action == "service" {
		object, _, _, handled, proofErr := a.originalServiceInvocation(ctx, run)
		if proofErr != nil {
			err = proofErr
		} else if handled {
			ctx = context.WithValue(ctx, originalBoundExecutionKey{}, object)
			lifecycleCtx = context.WithValue(lifecycleCtx, originalBoundExecutionKey{}, object)
		}
	}
	var deferred *pluginDeferredCall
	if err == nil {
		deferred, err = a.preparePluginDeferred(ctx, installation, run)
	}
	policy := pluginInvocationPolicy(installation, run, deferred)
	if err == nil && policy.Lifecycle {
		// Preparation remains bounded. Only the exact admitted native operation
		// may outlive that deadline; shutdown/drain cancellation still applies.
		ctx = lifecycleCtx
	}
	workerMethod := "invoke"
	workerInput := run.Input
	if err == nil && installation.PluginID == "AutoSignIn" && run.Action == "api" {
		workerMethod = "http"
		workerInput = json.RawMessage(`{}`)
	}
	if err == nil && installation.PluginID == "LarkMessager" {
		workerMethod, workerInput, err = larkWorkerInput(run)
	}
	if err == nil && installation.PluginID == cf.PluginID && run.Action == "api" {
		workerInput, err = cloudflareWorkerInput(run)
	}
	if err == nil && installation.PluginID == "AutoBackup" && run.Action == "api" {
		workerInput, err = autoBackupWorkerInput(run)
	}
	if err == nil && installation.PluginID == bf.PluginID {
		workerInput, err = brushFlowWorkerInput(run)
	}
	if err == nil && installation.PluginID == sr.PluginID {
		workerInput, err = seedRescueWorkerInput(run)
	}
	if err == nil && implementation.External {
		var boundObject *originalPluginInstance
		workerMethod, workerInput, err = a.originalExternalRunCall(ctx, run, workerMethod, workerInput, &boundObject)
		if boundObject != nil {
			ctx = context.WithValue(ctx, originalBoundExecutionKey{}, boundObject)
		}
	}
	result := plugins.RunResult{}
	hostErrorCode := ""
	if err == nil {
		result, err = a.runPluginImplementation(ctx, plugins.Runner{Timeout: policy.WorkerTimeout, ExcludeHostTime: policy.ExcludeHostTime}, command, manifest, installation, workerMethod, workerInput, func(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
			var value json.RawMessage
			var hostErr error
			if deferred != nil {
				value, hostErr = deferred.handshake(ctx, identity, method, raw)
			} else {
				value, hostErr = a.pluginHostCall(ctx, installation, &run, identity, method, raw)
			}
			if hostErr != nil {
				var api *APIError
				if errors.As(hostErr, &api) {
					hostErrorCode = api.Code
				} else {
					hostErrorCode = "PLUGIN_HOST_FAILED"
				}
			}
			return value, hostErr
		})
	}
	// Runner has exited and cleanup completed. Actual terminal owner operations
	// must never hold the worker process slot or an outer lifecycle lock.
	releaseProcess()
	if deferred != nil {
		if err == nil {
			err = deferred.ready(result)
		}
		result.Result = nil // A private handshake is never a business result.
		if err == nil {
			result.Result, err = deferred.execute(ctx, &run)
		}
	}
	state, errorCode := "succeeded", ""
	if err != nil {
		state, errorCode = "failed", "PLUGIN_EXECUTION_FAILED"
		var api *APIError
		if errors.As(err, &api) {
			errorCode = api.Code
		}
		var protocol *plugins.Error
		if errors.As(err, &protocol) {
			errorCode = protocol.Code
		}
		if hostErrorCode != "" {
			errorCode = hostErrorCode
		}
		if ctx.Err() != nil {
			state = "reconcile_pending"
		}
	}

	// SeedRescue's exact immutable GET status/job grant has no write capability
	// reachable from its compiled handler. Canceling that real SDK read is a
	// known terminal read failure, even after a reverse call has begun.
	brushReadOnly := installation.PluginID == bf.PluginID && installation.Version == bf.Version && deferred == nil && brushFlowReadRun(run)
	seedReadOnly := installation.PluginID == sr.PluginID && installation.Version == sr.Version && deferred == nil && seedRescueReadRun(run)
	if (seedReadOnly || brushReadOnly) && err != nil && ctx.Err() != nil {
		state, errorCode = "failed", "execution_canceled"
	}
	brushNotStarted := false
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	if brushFlowSnapshotOwner(installation.PluginID, installation.Version) && ctx.Err() == nil {
		cancel()
		finishCtx, cancel = brushFlowCompletionContext(ctx)
	}
	defer cancel()
	if installation.PluginID == "PluginUnInstall" && installation.Version == "2.2" {
		committed, found, receiptErr := a.pluginStore.committedUninstallResult(finishCtx, &run)
		if receiptErr != nil {
			err, state, errorCode = receiptErr, "reconcile_pending", "PLUGIN_UNINSTALL_RECEIPT_FAILED"
		} else if found {
			result.Result, err, state, errorCode = committed, nil, "succeeded", ""
		}
	}
	if installation.PluginID == "MergeSiteSwitch" && installation.Version == "1.2" {
		committed, receiptErr := a.pluginStore.siteControlReceipt(finishCtx, run)
		if receiptErr == nil {
			result.Result, err, state, errorCode = committed, nil, "succeeded", ""
		} else if !errors.Is(receiptErr, sql.ErrNoRows) {
			err, state, errorCode = receiptErr, "reconcile_pending", "PLUGIN_SITE_RECEIPT_INVALID"
		}
	}
	if installation.PluginID == "SiteStatistic" && run.Action == "api" {
		collection, found, collectionErr := a.pluginStore.siteMessageCollectionForRun(finishCtx, a.Store.DB, run)
		if collectionErr != nil {
			err, state, errorCode = collectionErr, "reconcile_pending", "SITE_MESSAGE_COLLECTION_UNCONFIRMED"
		} else if found {
			committed, receiptErr := a.pluginStore.siteStatisticReceipt(finishCtx, run)
			if collection.Stage == "failed" || collection.Stage == "aborted" {
				// The original synchronous exception is terminal. A restart must
				// neither publish later ordinals nor turn the API into success.
				errorCode = collection.Failure
				if errorCode == "" {
					errorCode = "SITE_MESSAGE_POST_FAILED"
				}
				err, state = bad(errorCode, "站点采集或消息发布已终止，未重试外部操作"), "failed"
				result.Result = nil
			} else if receiptErr == nil && collection.Stage == "complete" {
				if proofErr := a.pluginStore.validateSiteCollectionCompletion(finishCtx, a.Store.DB, collection); proofErr != nil {
					err, state, errorCode = proofErr, "reconcile_pending", "SITE_MESSAGE_COLLECTION_UNCONFIRMED"
				} else {
					result.Result, err, state, errorCode = committed, nil, "succeeded", ""
				}
			} else {
				// Includes an interrupted HTTP/mark-read admission and a completed
				// source sequence whose final API-result write was not confirmed.
				// Recovery may use persisted facts only; never run the SDK again.
				err, state, errorCode = bad("SITE_MESSAGE_COLLECTION_UNCONFIRMED", "站点采集或通知结果尚待核对，未重复执行"), "reconcile_pending", "SITE_MESSAGE_COLLECTION_UNCONFIRMED"
				result.Result = nil
			}
		}
	}
	if installation.PluginID == "SiteStatistic" && run.Action == "event" {
		committed, receiptErr := a.pluginStore.siteStatisticReceipt(finishCtx, run)
		if receiptErr == nil {
			outcome, outcomeCode, routed, outcomeErr := a.pluginStore.siteStatisticReceiptOutcome(finishCtx, run, committed)
			if routed {
				result.Result, state, errorCode = committed, outcome, outcomeCode
				if outcomeErr != nil {
					err = outcomeErr
				} else if outcome == "succeeded" {
					err = nil
				} else {
					err = bad(outcomeCode, "统计通知保留原始路由结果，未重新发送")
				}
			} else if outcomeErr != nil {
				err, state, errorCode = outcomeErr, "reconcile_pending", "PLUGIN_SITE_STATISTIC_NOTICE_INVALID"
			}
		} else if !errors.Is(receiptErr, sql.ErrNoRows) {
			err, state, errorCode = receiptErr, "reconcile_pending", "PLUGIN_SITE_STATISTIC_NOTICE_INVALID"
		}
	}
	if err != nil && deferred != nil {
		stored, _, receiptErr := a.pluginStore.readDeferred(finishCtx, a.Store.DB, deferred.run)
		if receiptErr != nil {
			state, errorCode = "reconcile_pending", "PLUGIN_DEFERRED_RECEIPT_FAILED"
		} else if stored.Phase == "confirmed" && deferredSameRun(stored, deferred.installation, deferred.run) && stored.Method == deferred.record.Method && bytes.Equal(stored.Ack, deferred.record.Ack) && bytes.Equal(stored.Raw, deferred.record.Raw) {
			result.Result, err, state, errorCode = stored.Result, nil, "succeeded", ""
		} else if stored.Phase == "terminal_failed" && deferredSameRun(stored, deferred.installation, deferred.run) && stored.Method == deferred.record.Method && bytes.Equal(stored.Ack, deferred.record.Ack) && bytes.Equal(stored.Raw, deferred.record.Raw) {
			result.Result, state, errorCode = stored.Result, "failed", stored.ErrorCode
			err = bad(errorCode, "插件操作失败，已保留确证结果")
		} else if installation.PluginID == bf.PluginID && installation.Version == bf.Version && stored.Phase == "prepared" && deferredSameRun(stored, deferred.installation, deferred.run) && stored.Method == deferred.record.Method && bytes.Equal(stored.Ack, deferred.record.Ack) && bytes.Equal(stored.Raw, deferred.record.Raw) && a.pluginStore.brushFlowCancelBeforeSDK(finishCtx, run) == nil {
			state, errorCode = "failed", "BRUSH_FLOW_NOT_STARTED"
			result.Result = json.RawMessage(`{"version":1,"decision":"not_started","effects_started":false}`)
			err = bad(errorCode, "站点刷流在业务宿主启动前已停止，本次没有执行外部操作")
			brushNotStarted = true
		} else if stored.Phase == "started" || stored.Phase == "uncertain" {
			state = "reconcile_pending"
		}
	}
	// A failed response after a reverse host call may follow a real operation.
	// Preserve its diagnostic code while preventing a fresh startup init from
	// repeating that exact generation/configuration. Cancellation recovery keeps
	// using each plugin's existing reconciliation path.
	blockStartup := !seedReadOnly && !brushNotStarted && err != nil && state == "failed" && (result.HostCalls > 0 || deferred != nil)
	if !brushNotStarted {
		if finishErr := a.pluginStore.finishRun(finishCtx, run.ID, state, errorCode, result.Result, blockStartup); finishErr != nil {
			code := pluginResultFailureCode(finishErr)
			fallbackCtx, fallbackCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			fallbackErr := a.pluginStore.failPluginResult(fallbackCtx, run.ID, code)
			fallbackCancel()
			if fallbackErr != nil {
				a.pluginExecutionLog(installation, run, "ERROR", "finish_failed", "插件结果及失败状态均无法保存，请检查存储并核对实际操作", "PLUGIN_FINISH_FAILED")
				return errors.Join(finishErr, fallbackErr)
			}
			a.pluginExecutionLog(installation, run, "ERROR", "finish_failed", "保存插件执行结果失败，已阻止本次执行自动重放，请核对实际操作", code)
			return finishErr
		}
	}
	if installation.PluginID == "MediaServerRefresh" {
		a.wakeMediaVerification(finishCtx)
	}
	if err != nil {
		a.pluginExecutionLog(installation, run, "ERROR", "execution_failed", "插件执行失败，详情见插件执行记录", errorCode)
	} else {
		a.pluginExecutionLog(installation, run, "INFO", "execution_completed", "插件执行已完成", "")
	}
	return nil
}

func (a *App) pluginExecutionLog(installation PluginInstallation, run PluginRun, level, event, message, code string) {
	a.writeLog(map[string]any{"id": id("log_"), "time": now(), "level": level, "module": "plugins", "event": event, "message": message, "plugin_id": installation.PluginID, "plugin_version": run.Version, "plugin_instance_id": run.InstanceID, "plugin_run_id": run.ID, "request_id": run.RequestID, "error_code": code, "app_version": appVersion})
}

// Every write rechecks installation, generation and config revision while
// sharing pluginMu with configuration/reset/uninstall. Workers cannot choose
// another instance, an arbitrary path, SQL, command or URL.
func (a *App) validatePluginHostRun(ctx context.Context, installation PluginInstallation, run *PluginRun, method string) error {
	if run != nil {
		current, err := a.pluginStore.Installation(ctx, installation.InstanceID)
		originalInit := originalInitializationMatches(ctx, installation.InstanceID, installation.PluginID, run.Version, run.Generation, run.Action) && a.pluginStore.originalInitializationRunBound(*run)
		_, _, _, originalService, serviceErr := a.originalServiceInvocation(ctx, *run)
		if serviceErr != nil {
			return serviceErr
		}
		if err != nil || ((!current.Installed || !current.DesiredEnabled) && !originalInit && !originalService) || current.PluginID != installation.PluginID || current.Version != run.Version || current.Generation != run.Generation {
			return bad("PLUGIN_SNAPSHOT_STALE", "插件已卸载、重置或变更，旧执行已停止")
		}
		config, err := a.pluginStore.Config(ctx, run.InstanceID)
		if errors.Is(err, sql.ErrNoRows) && (originalInit || originalService) && run.ConfigRevision == 0 {
			// Original init_plugin(None) is a real loaded-object call. Absence
			// is not a default configuration and does not authorize other runs.
			err = nil
		}
		if err != nil {
			return err
		}
		if config.Revision != run.ConfigRevision {
			accepted := false
			if current.PluginID == "AutoSignIn" && current.Version == autosignin.Version {
				tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if e != nil {
					return e
				}
				handled, e := a.pluginStore.autoSignInScheduleAdmissionTx(ctx, tx, run.InstanceID, run.Generation, run.ConfigRevision)
				tx.Rollback()
				accepted = handled && e == nil
			}
			if !accepted {
				return bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，旧执行已停止")
			}
		}
		persisted, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil || persisted.State != "running" {
			return bad("PLUGIN_RUN_TERMINAL", "插件执行已停止")
		}
		if err := a.pluginStore.requirePluginExecutionHandoff(ctx, installation.InstanceID); err != nil {
			return err
		}
	} else if method != "category.read" && method != "media.servers" && method != "sites.controls.read" {
		return bad("PLUGIN_CAPABILITY_DENIED", "插件表单只能调用获授权的只读接口")
	}
	return nil
}

func (a *App) pluginHostCall(ctx context.Context, installation PluginInstallation, run *PluginRun, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
	if identity.PluginID != installation.PluginID || identity.InstanceID != installation.InstanceID || identity.Version != installation.Version {
		return nil, bad("PLUGIN_IDENTITY_MISMATCH", "插件执行身份不匹配")
	}
	// The catalog is the host grant list. A self-declared manifest is insufficient.
	trusted, _, exists := builtin.Lookup(installation.PluginID)
	if !exists || !slices.Contains(trusted.Capabilities, method) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "该插件未获授权调用此操作")
	}
	if method == autosignin.OptionsCapability || method == autosignin.ConfigureCapability || method == autosignin.RunCapability || method == autosignin.SnapshotCapability || method == autosignin.DeleteCapability {
		return a.pluginAutoSignInHost(ctx, installation, run, method, raw)
	}
	if method == "sites.controls.read" || method == "sites.controls.configure" {
		return a.pluginSiteControlsHost(ctx, installation, run, method, raw)
	}
	if method == "site.statistics.notify" {
		return a.pluginSiteStatisticNotifyHost(ctx, installation, run, raw)
	}
	if method == "site.statistics.refresh_domain" {
		return a.pluginSiteStatisticAPIHost(ctx, installation, run, raw)
	}
	if method == "subscriptions.rules.configure" || method == "subscriptions.rules.apply" {
		return a.pluginSubscribeGroupHost(ctx, installation, run, method, raw)
	}
	if method == lark.ConfigureCapability || method == lark.ModuleCapability || method == lark.EventCapability || method == lark.FormCapability || method == lark.PageCapability || method == lark.APICapability {
		return a.pluginLarkHost(ctx, installation, run, method, raw)
	}
	if method == scrapefileclean.ConfigureCapability || method == scrapefileclean.ApplyCapability {
		return a.pluginScrapeCleanHost(ctx, installation, run, method, raw)
	}
	if method == removelink.ConfigureCapability || method == removelink.ApplyCapability {
		return a.pluginRemoveLinkHost(ctx, installation, run, method, raw)
	}
	if method == uninstall.OptionsCapability || method == uninstall.ApplyCapability {
		if method == uninstall.ApplyCapability {
			if err := a.requireDeferredExecution(ctx, run, method); err != nil {
				return nil, err
			}
		}
		return a.pluginUninstallHost(ctx, installation, run, method, raw)
	}
	if method == "server.status.configure" {
		return a.pluginServerStatusConfigure(ctx, installation, run, raw)
	}
	if method == transfer.ConfigureCapability || method == transfer.ExecuteCapability || method == transfer.FormCapability {
		return a.pluginTransferHost(ctx, installation, run, method, raw)
	}
	if method == iyuu.ConfigureCapability || method == iyuu.ExecuteCapability || method == iyuu.FormCapability {
		return a.pluginIYUUHost(ctx, installation, run, method, raw)
	}
	if method == cleaninvalidseed.ConfigureCapability || method == cleaninvalidseed.ExecuteCapability || method == cleaninvalidseed.FormCapability {
		return a.pluginCleanInvalidSeedHost(ctx, installation, run, method, raw)
	}
	if method == cf.ConfigureCapability || method == cf.RunCapability {
		return a.pluginCloudflareHost(ctx, installation, run, method, raw)
	}
	if method == autobackup.ConfigureCapability || method == autobackup.ExecuteCapability || method == autobackup.APICapability || method == autobackup.FormCapability {
		return a.pluginAutoBackupHost(ctx, installation, run, method, raw)
	}
	if method == bf.ConfigureCapability || method == bf.TaskCapability || method == bf.APICapability || method == bf.ReadCapability {
		return a.pluginBrushFlowHost(ctx, installation, run, method, raw)
	}
	if method == sr.ConfigureCapability || method == sr.ScanCapability || method == sr.APICapability || method == sr.ReadCapability {
		return a.pluginSeedRescueHost(ctx, installation, run, method, raw)
	}
	if method == "identifiers.configure" || method == "identifiers.refresh" {
		return a.pluginIdentifiersHost(ctx, installation, run, method, raw)
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := a.validatePluginHostRun(ctx, installation, run, method); err != nil {
		return nil, err
	}
	switch method {
	case "site.statistics.configure", "site.statistics.refresh":
		return a.pluginSiteStatisticHost(ctx, installation, run, method, raw)
	case "media.servers", "events.configure", "media.refresh":
		return a.pluginMediaHost(ctx, installation, run, method, raw)
	case "hosts.read":
		return a.readContainerHosts()
	case "hosts.replace":
		return a.replaceContainerHosts(ctx, raw)
	case "category.read":
		return json.Marshal(map[string]string{"content": defaultMediaCategoryPolicy})
	case "category.write":
		var in struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return nil, bad("INVALID_CATEGORY_POLICY", "二级分类策略无效")
		}
		if _, _, err := categoryPluginReload([]byte(in.Content)); err != nil {
			return nil, err
		}
		if err := a.writePluginCategory(in.Content); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"applied":true}`), nil
	case "config.patch":
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件配置更新格式不符合授权范围")
		}
		if installation.PluginID == "CategoryEditor" {
			if len(fields) != 1 || string(fields["enabled"]) != "false" {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "二级分类插件只能关闭自身的一次性开关")
			}
		} else if installation.PluginID == "CustomHosts" {
			var enabled bool
			var hosts string
			var invalid []string
			if len(fields) != 3 || json.Unmarshal(fields["enabled"], &enabled) != nil || json.Unmarshal(fields["hosts"], &hosts) != nil || json.Unmarshal(fields["err_hosts"], &invalid) != nil || len(hosts) > builtin.MaxCustomHostsBytes {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "Hosts插件只能规范自身配置和错误记录")
			}
			stored, err := a.pluginStore.Config(ctx, run.InstanceID)
			var source builtin.CustomHostsConfiguration
			if err != nil || json.Unmarshal(stored.Value, &source) != nil || (!source.Enabled && enabled) {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件不能自行启用未获用户启用的配置")
			}
		} else {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "此插件尚无配置写回授权")
		}
		if installation.PluginID == "CustomHosts" {
			if err := a.replaceCustomHostsRunConfig(ctx, run, fields); err != nil {
				return nil, err
			}
		} else {
			if err := a.pluginStore.patchRunConfig(ctx, run, fields); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`{"saved":true}`), nil
	case "notification.post":
		var notice struct {
			Title    string   `json:"title"`
			Message  string   `json:"message"`
			Messages []string `json:"messages"`
		}
		if json.Unmarshal(raw, &notice) != nil {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件通知格式无效")
		}
		categoryNotice := installation.PluginID == "CategoryEditor" && notice.Title == "二级分类策略" && notice.Message == "二级分类策略已更新，请注意同步调整目录设置，插件将恢复关闭状态！"
		hostsNotice := installation.PluginID == "CustomHosts" && notice.Title == "自定义Hosts" && (notice.Message == "写入系统hosts文件失败，请检查容器挂载及文件权限。" || notice.Message == "更新系统hosts文件失败：请检查权限" || notice.Message == "恢复系统hosts文件失败：请检查权限")
		if installation.PluginID == "CustomHosts" && notice.Title == "自定义Hosts" && notice.Message == "" && len(notice.Messages) > 0 && len(notice.Messages) <= 4096 {
			for _, message := range notice.Messages {
				if message != "[HOST] 格式转换错误：" && message != "[HOST] 格式转换错误：list index out of range" && message != "[HOST] 格式转换错误：Address and Name(s) must be specified." {
					return nil, bad("PLUGIN_CAPABILITY_DENIED", "Hosts错误通知内容无效")
				}
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			for index, message := range notice.Messages {
				n := Notification{ID: fmt.Sprintf("plugin_notice_%s_invalid_%d", run.ID, index), Title: notice.Title, Message: message, CreatedAt: now()}
				if err := txPut(ctx, tx, "notification", n.ID, n); err != nil {
					return nil, err
				}
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"posted":true}`), nil
		}
		if !categoryNotice && !hostsNotice {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件通知内容不符合已授权动作")
		}
		n := Notification{ID: "plugin_notice_" + run.ID, Title: notice.Title, Message: notice.Message, CreatedAt: now()}
		if err := a.Store.Put(ctx, "notification", n.ID, n); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"posted":true}`), nil
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件操作尚未实现")
	}
}

func (a *App) writePluginCategory(content string) error {
	// Preserve the previous process maps before replacing their backing text.
	if _, err := a.currentMediaCategoryPolicy(); err != nil {
		// A valid new policy may repair an unsupported old file. No old maps are
		// invented on this path; the replacement is parsed again after the write.
		if _, kind, check := categoryPluginReload([]byte(content)); check != nil || kind != categoryReplaceMaps {
			return err
		}
	}
	path := filepath.Join(a.Dir, "category.yaml")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return bad("CATEGORY_FILE_UNSAFE", "分类策略目标不是普通配置文件")
	}
	file, err := os.CreateTemp(a.Dir, ".category-*.tmp")
	if err != nil {
		return bad("CATEGORY_WRITE_FAILED", "无法创建二级分类策略文件，请检查配置目录权限")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.WriteString(content); err == nil {
		err = file.Sync()
	}
	if err == nil {
		err = file.Close()
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return bad("CATEGORY_WRITE_FAILED", "写入二级分类策略失败，原配置保留")
	}
	_, err = a.currentMediaCategoryPolicy()
	return err
}
