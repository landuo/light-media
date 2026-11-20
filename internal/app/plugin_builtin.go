package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"light-media/internal/plugins"

	"github.com/landuo/light-media-plugins/plugins/autobackup"
	"github.com/landuo/light-media-plugins/plugins/autosignin"
	brushflow "github.com/landuo/light-media-plugins/plugins/brushflowlanduo"
	"github.com/landuo/light-media-plugins/plugins/categoryeditor"
	"github.com/landuo/light-media-plugins/plugins/cleaninvalidseed"
	"github.com/landuo/light-media-plugins/plugins/cloudflarespeedtest"
	"github.com/landuo/light-media-plugins/plugins/customhosts"
	"github.com/landuo/light-media-plugins/plugins/iyuuautoseed"
	"github.com/landuo/light-media-plugins/plugins/larkmessager"
	"github.com/landuo/light-media-plugins/plugins/mediaserverrefresh"
	merge "github.com/landuo/light-media-plugins/plugins/mergemessagenotify"
	mergesmtp "github.com/landuo/light-media-plugins/plugins/mergemessagenotify/smtp"
	"github.com/landuo/light-media-plugins/plugins/mergesiteswitch"
	"github.com/landuo/light-media-plugins/plugins/mpserverstatus"
	"github.com/landuo/light-media-plugins/plugins/pluginuninstall"
	"github.com/landuo/light-media-plugins/plugins/remoteidentifiers"
	"github.com/landuo/light-media-plugins/plugins/removelink"
	"github.com/landuo/light-media-plugins/plugins/scrapefileclean"
	"github.com/landuo/light-media-plugins/plugins/seedrescue"
	"github.com/landuo/light-media-plugins/plugins/sitestatistic"
	"github.com/landuo/light-media-plugins/plugins/subscribegroup"
	"github.com/landuo/light-media-plugins/plugins/systemprocess"
	"github.com/landuo/light-media-plugins/plugins/torrenttransfer"
)

// sdkBridge adapts the host's named HostCaller to the unnamed function shape
// the plugin SDK's named HostCaller accepts. Assignability between one named
// and one unnamed function type with identical underlying types is the whole
// bridge: the host never imports the plugin SDK.
func sdkBridge(host plugins.HostCaller) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return host
}

// builtinPlugin is one compiled-in plugin class. The constructor closure
// builds a fresh instance per session; every SDK type stays unnamed here: the
// handler bridge passes function literals, which are assignable to the SDK's
// named HostCaller without importing the plugin SDK's internal package.
type builtinPlugin struct {
	pluginID   string
	concurrent bool
	manifest   func() any
	newHandler func() plugins.InProcessHandler
}

// sendSMTP reproduces the standalone merge-message-notify entry's SMTP
// binding; the builtin must not silently drop a declared channel.
func builtinSendSMTP(ctx context.Context, config map[string]any, request merge.SMTPRequest) error {
	binding, err := mergesmtp.BindingFromTrustedConfig(config)
	if err != nil {
		return err
	}
	adapter, err := mergesmtp.New(binding, mergesmtp.Options{})
	if err != nil {
		return err
	}
	return adapter.Send(ctx, request)
}

// builtinClasses is the compiled-in catalog. Version and identity come from
// each class's own manifest declaration; the startup adoption pass aligns the
// persisted installation rows to this table, which is the whole release
// train: a plugin update ships as a light-media build.
var builtinClasses = []builtinPlugin{
	{pluginID: "AutoBackup", concurrent: true, manifest: func() any { return autobackup.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := autobackup.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "AutoSignIn", concurrent: true, manifest: func() any { return autosignin.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := autosignin.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "BrushFlowLanduo", concurrent: true, manifest: func() any { return brushflow.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := brushflow.NewStandard()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	// CategoryEditor ships the standalone serial worker; keep the same
	// serialization contract in-process.
	{pluginID: "CategoryEditor", concurrent: false, manifest: func() any { return categoryeditor.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := categoryeditor.New()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "CleanInvalidSeed", concurrent: true, manifest: func() any { return cleaninvalidseed.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := cleaninvalidseed.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "CloudflareSpeedTest", concurrent: true, manifest: func() any { return cloudflarespeedtest.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := cloudflarespeedtest.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "CustomHosts", concurrent: true, manifest: func() any { return customhosts.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := customhosts.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "IYUUAutoSeed", concurrent: true, manifest: func() any { return iyuuautoseed.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := iyuuautoseed.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "LarkMessager", concurrent: true, manifest: func() any { return larkmessager.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := larkmessager.NewWorker()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "MediaServerRefresh", concurrent: true, manifest: func() any { return mediaserverrefresh.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := mediaserverrefresh.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "MergeMessageNotify", concurrent: true, manifest: func() any { return merge.Manifest() }, newHandler: func() plugins.InProcessHandler {
		worker := merge.NewWorker(builtinSendSMTP)
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return worker.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "MergeSiteSwitch", concurrent: true, manifest: func() any { return mergesiteswitch.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := mergesiteswitch.New()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "MPServerStatus", concurrent: true, manifest: func() any { return mpserverstatus.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := mpserverstatus.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "PluginUnInstall", concurrent: true, manifest: func() any { return pluginuninstall.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := pluginuninstall.New()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "RemoteIdentifiers", concurrent: true, manifest: func() any { return remoteidentifiers.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := remoteidentifiers.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "RemoveLink", concurrent: true, manifest: func() any { return removelink.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := removelink.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "ScrapeFileClean", concurrent: true, manifest: func() any { return scrapefileclean.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := scrapefileclean.New()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "SeedRescue", concurrent: true, manifest: func() any { return seedrescue.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := seedrescue.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "SiteStatistic", concurrent: true, manifest: func() any { return sitestatistic.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := sitestatistic.New()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "SubscribeGroup", concurrent: true, manifest: func() any { return subscribegroup.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := subscribegroup.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "SystemProcess", concurrent: true, manifest: func() any { return systemprocess.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := systemprocess.NewPlugin()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
	{pluginID: "TorrentTransfer", concurrent: true, manifest: func() any { return torrenttransfer.Manifest() }, newHandler: func() plugins.InProcessHandler {
		plugin := torrenttransfer.New()
		return func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			return plugin.Handle(ctx, method, raw, sdkBridge(host))
		}
	}},
}

// pluginAcquisitionClosed reports that this deployment ships its plugins in
// the binary, so per-plugin acquisition and removal are not offered. The
// rollback switch reopens that surface.
func (a *App) pluginAcquisitionClosed() bool {
	return !a.pluginBuiltinDisabled && !pluginBuiltinDisabled(os.LookupEnv)
}

// pluginBuiltinDisabled reports the operator rollback switch. "off" restores
// the external worker path for every class that still has an installed
// package on disk, which is exactly the pre-builtin behaviour.
func pluginBuiltinDisabled(lookup func(string) (string, bool)) bool {
	raw, ok := lookup("LIGHT_MEDIA_PLUGIN_BUILTIN")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "0", "false", "disabled":
		return true
	}
	return false
}

// builtinCatalog is the resolved, validated catalog keyed by PluginID. It is
// built once at startup: a manifest that cannot convert to the host schema
// fails fast instead of failing a live session.
type builtinCatalogEntry struct {
	manifest   plugins.Manifest
	concurrent bool
	newHandler func() plugins.InProcessHandler
}

var builtinCatalogOnce sync.Once
var builtinCatalog map[string]builtinCatalogEntry
var builtinCatalogErr error

// convertBuiltinManifest round-trips a plugin SDK manifest through JSON into
// the host schema. The two declarations are wire-compatible by design; this
// is the same conversion the market index performed, now at link time.
func convertBuiltinManifest(sdkManifest any) (plugins.Manifest, error) {
	raw, err := json.Marshal(sdkManifest)
	if err != nil {
		return plugins.Manifest{}, err
	}
	var out plugins.Manifest
	if err := json.Unmarshal(raw, &out); err != nil {
		return plugins.Manifest{}, err
	}
	return out, nil
}

func builtinCatalogLoad() (map[string]builtinCatalogEntry, error) {
	builtinCatalogOnce.Do(func() {
		catalog := make(map[string]builtinCatalogEntry, len(builtinClasses))
		for _, class := range builtinClasses {
			manifest, err := convertBuiltinManifest(class.manifest())
			if err != nil {
				builtinCatalogErr = fmt.Errorf("builtin %s manifest: %w", class.pluginID, err)
				return
			}
			if manifest.PluginID != class.pluginID {
				builtinCatalogErr = fmt.Errorf("builtin %s declares id %s", class.pluginID, manifest.PluginID)
				return
			}
			if err := validatePluginArtifactContract(manifest, pluginResourcesSDKContract); err != nil {
				builtinCatalogErr = fmt.Errorf("builtin %s contract: %w", class.pluginID, err)
				return
			}
			catalog[class.pluginID] = builtinCatalogEntry{manifest: manifest, concurrent: class.concurrent, newHandler: class.newHandler}
		}
		builtinCatalog = catalog
	})
	return builtinCatalog, builtinCatalogErr
}
