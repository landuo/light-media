package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autobackup"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/removelink"
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
			return err
		}
	}
	return nil
}

func (a *App) pluginWorker(ctx context.Context) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		run, err := a.pluginStore.ClaimRun(ctx)
		if err == nil {
			a.executePluginRun(ctx, run)
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

func (a *App) executePluginRun(ctx context.Context, run PluginRun) {
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
		return
	}
	installation, err := a.pluginStore.Installation(ctx, run.InstanceID)
	var manifest plugins.Manifest
	if err == nil {
		err = json.Unmarshal(installation.Manifest, &manifest)
	}
	compiled, _, available := builtin.Lookup(installation.PluginID)
	available = available && compiled.Version == installation.Version
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
		command, err = a.pluginExecutable(installation.PluginID)
	} else if err == nil {
		err = bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "当前版本没有该插件的可执行实现")
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
	workerInput := run.Input
	if err == nil && installation.PluginID == "AutoBackup" && run.Action == "api" {
		workerInput, err = autoBackupWorkerInput(run)
	}
	result := plugins.RunResult{}
	hostErrorCode := ""
	if err == nil {
		result, err = (plugins.Runner{Timeout: policy.WorkerTimeout, ExcludeHostTime: policy.ExcludeHostTime}).Run(ctx, command, manifest, installation.InstanceID, "invoke", workerInput, func(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
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
	finishCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
	if err != nil && deferred != nil {
		stored, _, receiptErr := a.pluginStore.readDeferred(finishCtx, a.Store.DB, deferred.run)
		if receiptErr != nil {
			state, errorCode = "reconcile_pending", "PLUGIN_DEFERRED_RECEIPT_FAILED"
		} else if stored.Phase == "confirmed" && deferredSameRun(stored, deferred.installation, deferred.run) && stored.Method == deferred.record.Method && bytes.Equal(stored.Ack, deferred.record.Ack) && bytes.Equal(stored.Raw, deferred.record.Raw) {
			result.Result, err, state, errorCode = stored.Result, nil, "succeeded", ""
		} else if stored.Phase == "terminal_failed" && deferredSameRun(stored, deferred.installation, deferred.run) && stored.Method == deferred.record.Method && bytes.Equal(stored.Ack, deferred.record.Ack) && bytes.Equal(stored.Raw, deferred.record.Raw) {
			result.Result, state, errorCode = stored.Result, "failed", stored.ErrorCode
			err = bad(errorCode, "插件操作失败，已保留确证结果")
		} else if stored.Phase == "started" || stored.Phase == "uncertain" {
			state = "reconcile_pending"
		}
	}
	// A failed response after a reverse host call may follow a real operation.
	// Preserve its diagnostic code while preventing a fresh startup init from
	// repeating that exact generation/configuration. Cancellation recovery keeps
	// using each plugin's existing reconciliation path.
	blockStartup := err != nil && state == "failed" && (result.HostCalls > 0 || deferred != nil)
	if finishErr := a.pluginStore.finishRun(finishCtx, run.ID, state, errorCode, result.Result, blockStartup); finishErr != nil {
		code := pluginResultFailureCode(finishErr)
		fallbackCtx, fallbackCancel := context.WithTimeout(context.Background(), 3*time.Second)
		fallbackErr := a.pluginStore.failPluginResult(fallbackCtx, run.ID, code)
		fallbackCancel()
		if fallbackErr != nil {
			a.pluginExecutionLog(installation, run, "ERROR", "finish_failed", "插件结果及失败状态均无法保存，请检查存储并核对实际操作", "PLUGIN_FINISH_FAILED")
			return
		}
		a.pluginExecutionLog(installation, run, "ERROR", "finish_failed", "保存插件执行结果失败，已阻止本次执行自动重放，请核对实际操作", code)
		return
	}
	if installation.PluginID == "MediaServerRefresh" {
		a.wakeMediaVerification(finishCtx)
	}
	if err != nil {
		a.pluginExecutionLog(installation, run, "ERROR", "execution_failed", "插件执行失败，详情见插件执行记录", errorCode)
	} else {
		a.pluginExecutionLog(installation, run, "INFO", "execution_completed", "插件执行已完成", "")
	}
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
		if err != nil || !current.Installed || !current.DesiredEnabled || current.Version != run.Version || current.Generation != run.Generation {
			return bad("PLUGIN_SNAPSHOT_STALE", "插件已卸载、重置或变更，旧执行已停止")
		}
		config, err := a.pluginStore.Config(ctx, run.InstanceID)
		if err != nil || config.Revision != run.ConfigRevision {
			return bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，旧执行已停止")
		}
		persisted, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil || persisted.State != "running" {
			return bad("PLUGIN_RUN_TERMINAL", "插件执行已停止")
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
	if method == "sites.controls.read" || method == "sites.controls.configure" {
		return a.pluginSiteControlsHost(ctx, installation, run, method, raw)
	}
	if method == "site.statistics.refresh_domain" {
		return a.pluginSiteStatisticAPIHost(ctx, installation, run, raw)
	}
	if method == "subscriptions.rules.configure" || method == "subscriptions.rules.apply" {
		return a.pluginSubscribeGroupHost(ctx, installation, run, method, raw)
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
	if method == cleaninvalidseed.ConfigureCapability || method == cleaninvalidseed.ExecuteCapability || method == cleaninvalidseed.FormCapability {
		return a.pluginCleanInvalidSeedHost(ctx, installation, run, method, raw)
	}
	if method == autobackup.ConfigureCapability || method == autobackup.ExecuteCapability || method == autobackup.APICapability || method == autobackup.FormCapability {
		return a.pluginAutoBackupHost(ctx, installation, run, method, raw)
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := a.validatePluginHostRun(ctx, installation, run, method); err != nil {
		return nil, err
	}
	switch method {
	case "site.statistics.configure", "site.statistics.refresh", "site.statistics.notify":
		return a.pluginSiteStatisticHost(ctx, installation, run, method, raw)
	case "identifiers.configure", "identifiers.refresh":
		return a.pluginIdentifiersHost(ctx, installation, run, method, raw)
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
		if _, err := parseMediaCategoryPolicy([]byte(in.Content)); err != nil {
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
		if err := a.pluginStore.patchRunConfig(ctx, run, fields); err != nil {
			return nil, err
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
		hostsNotice := installation.PluginID == "CustomHosts" && notice.Title == "自定义Hosts" && notice.Message == "写入系统hosts文件失败，请检查容器挂载及文件权限。"
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
	return nil
}
