package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
)

// A finite resource vocabulary shared by standard artifacts. Declarations do
// not grant access without the actual current SDK object's invocation scope.
func originalStandardCapabilityAllowed(contract, method string) bool {
	if contract == pluginBaseSDKContract || contract == pluginResourcesSDKContract {
		if plugins.IsBaseMethod(method) {
			return true
		}
	}
	return contract == pluginResourcesSDKContract && (method == plugins.BrushInventoryReadCapability || method == plugins.LogWriteCapability || method == plugins.DownloaderTasksReadCapability || method == plugins.BackgroundSubmitCapability || method == plugins.ServicesRefreshCapability || method == plugins.ServiceProgressCapability || method == plugins.AuthTicketCreateCapability || method == plugins.PostMessageCapability || method == plugins.EventSendCapability || method == plugins.EventCheckCapability || method == plugins.EventManageCapability || originalInputCapability(method) || categoryEditorCapability(method) || originalSharedSubscriptionsCapability(method) || originalSharedSystemConfigCapability(method) || originalSharedHistoryCapability(method) || originalSharedManagerQueryCapability(method) || originalSharedManagerMutationCapability(method) || originalSharedManagerRuntimeCapability(method) || method == plugins.SharedPluginRemove || originalSharedSchedulerCapability(method) || originalSharedTransmissionQueryCapability(method) || originalSharedQBittorrentCompletedCapability(method) || originalSharedLocalStorageCapability(method) || originalSharedOCRCapability(method) || originalSharedTorrentMaterialCapability(method) || originalSharedLocalTorrentBackupCapability(method) || originalSharedDownloaderCommandCapability(method) || originalSharedSiteAccessCapability(method))
}

func originalResourceDecode(raw json.RawMessage, output any) error {
	if len(raw) == 0 || len(raw) > 128<<10 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return bad("INVALID_PLUGIN_VALUE", "插件资源参数必须为有界对象")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil {
		return bad("INVALID_PLUGIN_VALUE", "插件资源参数无效")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return bad("INVALID_PLUGIN_VALUE", "插件资源参数无效")
	}
	return nil
}

func (a *App) pluginOriginalResourceHost(ctx context.Context, scope originalBaseCallScope, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
	if scope.validate == nil || plugins.IsBaseMethod(method) || !originalStandardCapabilityAllowed(pluginResourcesSDKContract, method) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件资源方法未获授权")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	in := scope.installation
	if identity.PluginID != in.PluginID || identity.InstanceID != in.InstanceID || identity.Version != in.Version {
		return nil, bad("PLUGIN_IDENTITY_MISMATCH", "插件执行身份不匹配")
	}
	ctx = withOriginalPluginLogContext(ctx, scope)
	if originalSharedSiteAccessCapability(method) {
		return a.originalSharedSiteAccessResource(ctx, scope, method, raw)
	}
	if originalSharedDownloaderCommandCapability(method) {
		return a.originalSharedDownloaderCommandResource(ctx, scope, method, raw)
	}
	if originalSharedLocalTorrentBackupCapability(method) {
		return a.originalSharedLocalTorrentBackupResource(ctx, scope, raw)
	}
	if originalSharedTorrentMaterialCapability(method) {
		return a.originalSharedTorrentMaterialResource(ctx, scope, raw)
	}
	if originalSharedOCRCapability(method) {
		return a.originalSharedOCRResource(ctx, scope, method, raw)
	}
	if originalSharedLocalStorageCapability(method) {
		return a.originalSharedLocalStorageResource(ctx, scope, method, raw)
	}
	if originalSharedSystemConfigCapability(method) {
		return a.originalSharedSystemConfigResource(ctx, scope, method, raw)
	}
	if originalSharedQBittorrentCompletedCapability(method) {
		return a.originalSharedQBittorrentCompletedResource(ctx, scope, method, raw)
	}
	if originalSharedTransmissionQueryCapability(method) {
		return a.originalSharedTransmissionQueryResource(ctx, scope, method, raw)
	}
	if originalSharedManagerQueryCapability(method) {
		return a.originalSharedManagerQuery(ctx, scope, method, raw)
	}
	if originalSharedManagerMutationCapability(method) {
		return a.originalSharedManagerMutation(ctx, scope, method, raw)
	}
	if originalSharedManagerRuntimeCapability(method) {
		return a.originalSharedManagerRuntime(ctx, scope, method, raw)
	}
	if method == plugins.SharedPluginRemove {
		return a.originalSharedManagerRemove(ctx, scope, raw)
	}
	if originalSharedSchedulerCapability(method) {
		return a.originalSharedScheduler(ctx, scope, method, raw)
	}
	if originalSharedHistoryCapability(method) {
		return a.originalSharedHistoryResource(ctx, scope, method, raw)
	}
	if originalSharedSubscriptionsCapability(method) {
		return a.originalSharedSubscriptionsResource(ctx, scope, method, raw)
	}
	if categoryEditorCapability(method) {
		return a.categoryEditorResource(ctx, scope, method, raw)
	}
	if method == plugins.BackgroundSubmitCapability {
		return a.originalSubmitResource(ctx, scope, raw)
	}
	if method == plugins.AuthTicketCreateCapability {
		return a.originalAuthTicketResource(ctx, scope, raw)
	}
	if method == plugins.EventSendCapability || method == plugins.EventCheckCapability || method == plugins.EventManageCapability {
		return a.originalEventResource(ctx, scope, method, raw)
	}
	if originalInputCapability(method) {
		return a.originalInputResource(ctx, scope, method, raw)
	}
	if method == plugins.PostMessageCapability {
		return a.originalPostMessageResource(ctx, scope, raw)
	}
	if method == plugins.ServiceProgressCapability {
		return a.originalServiceProgressResource(ctx, scope, raw)
	}
	if method == plugins.ServicesRefreshCapability {
		return a.originalRefreshServicesResource(ctx, scope, raw)
	}
	if method == plugins.LogWriteCapability {
		return a.pluginOriginalLogHost(ctx, scope, raw)
	}
	if method == plugins.DownloaderTasksReadCapability {
		return a.originalDownloaderTasks(ctx, scope, raw)
	}
	var input struct{}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	inventory, err := a.originalBrushInventory(ctx)
	if err != nil {
		return nil, err
	}
	// The single-connection read transaction is closed before this DB lookup.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(inventory)
}
