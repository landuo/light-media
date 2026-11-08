package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
	"strings"
)

// A finite resource vocabulary shared by standard artifacts. Declarations do
// not grant access without the actual current SDK object's invocation scope.
func originalStandardCapabilityAllowed(contract, method string) bool {
	if contract == pluginBaseSDKContract || contract == pluginResourcesSDKContract {
		if plugins.IsBaseMethod(method) {
			return true
		}
	}
	if contract == pluginResourcesSDKContract && (method == originalLarkEndpointCapability || method == plugins.NetworkGetCapability || method == plugins.NetworkProxyReadCapability || method == plugins.SitesRequestCapability || method == plugins.SitesCredentialsCapability || method == plugins.SiteRenderCapability || method == plugins.MessageReceiveCapability || originalHostOCRCapability(method) || originalHostDownloaderCapability(method) || originalHostStorageCapability(method) || originalHostSubscriptionsCapability(method) || originalHostMediaCapability(method) || originalHostManagerCapability(method) || originalHostHistoryCapability(method) || originalHostSettingsCapability(method) || originalHostSitesCapability(method) || originalSourceSiteRequestCapability(method)) {
		return true
	}
	return contract == pluginResourcesSDKContract && (method == originalMergeSiteSwitchCapability || method == plugins.BrushInventoryReadCapability || originalBrushSourceCapability(method) || originalCleanSeedCapability(method) || originalSeedRescueCapability(method) || originalCloudflareCapability(method) || method == originalCustomHostsCapability || method == plugins.LogWriteCapability || method == plugins.DownloaderTasksReadCapability || method == plugins.BackgroundSubmitCapability || method == plugins.CleanupSubmitCapability || method == plugins.ServicesRefreshCapability || method == plugins.ServiceProgressCapability || method == plugins.AuthTicketCreateCapability || method == plugins.PostMessageCapability || method == plugins.EventSendCapability || method == plugins.EventCheckCapability || method == plugins.EventManageCapability || method == plugins.LocalBackupCapability || originalInputCapability(method) || categoryEditorCapability(method))
}

func originalResourceDecode(raw json.RawMessage, output any) error {
	return originalResourceDecodeBounded(raw, output, 128<<10)
}

func originalResourceDecodeBounded(raw json.RawMessage, output any, limit int) error {
	if len(raw) == 0 || len(raw) > limit || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
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
	if method == originalMergeSiteSwitchCapability {
		return a.originalMergeSiteSwitchResource(ctx, scope, raw)
	}
	if originalBrushSourceCapability(method) {
		return a.originalBrushSourceResource(ctx, scope, method, raw)
	}
	if originalCleanSeedCapability(method) {
		return a.originalCleanSeedResource(ctx, scope, method, raw)
	}
	if originalSeedRescueCapability(method) {
		return a.originalSeedRescueResource(ctx, scope, strings.TrimPrefix(method, "seed_rescue."), raw)
	}
	if originalCloudflareCapability(method) {
		return a.originalCloudflareResource(ctx, scope, strings.TrimPrefix(method, "cloudflare_speed."), raw)
	}
	if method == originalCustomHostsCapability {
		return a.originalCustomHostsApply(ctx, scope, raw)
	}
	if method == plugins.LocalBackupCapability {
		return a.originalLocalBackupResource(ctx, scope, raw)
	}
	if method == originalLarkEndpointCapability {
		return a.originalLarkEndpointResource(ctx, scope, raw)
	}
	if method == plugins.NetworkProxyReadCapability {
		return a.originalHostProxyResource(ctx, scope, raw)
	}
	if method == plugins.NetworkGetCapability {
		return a.originalHostNetworkResource(ctx, scope, raw)
	}
	if method == plugins.SitesRequestCapability {
		return a.originalHostSiteHTTPResource(ctx, scope, raw)
	}
	if method == plugins.SitesCredentialsCapability {
		return a.originalHostSiteCredentialsResource(ctx, scope, raw)
	}
	if method == plugins.SiteRenderCapability {
		return a.originalHostBrowserResource(ctx, scope, raw)
	}
	if method == plugins.MessageReceiveCapability {
		return a.originalHostMessageInboundResource(ctx, scope, raw)
	}
	if originalHostOCRCapability(method) {
		return a.originalHostOCRResource(ctx, scope, method, raw)
	}
	if originalHostDownloaderCapability(method) {
		return a.originalHostDownloaderResource(ctx, scope, method, raw)
	}
	if originalHostStorageCapability(method) {
		return a.originalHostStorageResource(ctx, scope, method, raw)
	}
	if originalHostSubscriptionsCapability(method) {
		return a.originalHostSubscriptionsResource(ctx, scope, method, raw)
	}
	if originalHostMediaCapability(method) {
		return a.originalHostMediaResource(ctx, scope, method, raw)
	}
	if originalHostManagerCapability(method) {
		return a.originalHostManagerResource(ctx, scope, method, raw)
	}
	if originalHostHistoryCapability(method) {
		return a.originalHostHistoryResource(ctx, scope, method, raw)
	}
	if originalHostSettingsCapability(method) {
		return a.originalHostSettingsResource(ctx, scope, method, raw)
	}
	if originalSourceSiteRequestCapability(method) {
		return a.originalHostSourceSiteRequestResource(ctx, scope, method, raw)
	}
	if originalHostSitesCapability(method) {
		return a.originalHostSitesResource(ctx, scope, method, raw)
	}
	if categoryEditorCapability(method) {
		return a.categoryEditorResource(ctx, scope, method, raw)
	}
	if method == plugins.BackgroundSubmitCapability {
		return a.originalSubmitResource(ctx, scope, raw)
	}
	if method == plugins.CleanupSubmitCapability {
		return a.originalCleanupSubmitResource(ctx, scope, raw)
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
