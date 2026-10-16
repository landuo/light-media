package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

type pluginCard struct {
	plugins.Manifest
	InstanceID string `json:"instance_id"`
	Installed  bool   `json:"installed"`
	State      bool   `json:"state"`
}

func (a *App) pluginCards(ctx context.Context, market bool) ([]pluginCard, error) {
	installed, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return nil, err
	}
	items := []pluginCard{}
	seen := map[string]bool{}
	for _, installation := range installed {
		manifest, _, exists := builtin.Lookup(installation.PluginID)
		if !exists || manifest.Version != installation.Version {
			// Keep unavailable installed records visible; never claim runnable.
			manifest = plugins.Manifest{PluginID: installation.PluginID, Name: installation.PluginID, Version: installation.Version}
			exists = false
		}
		seen[installation.PluginID] = true
		if !market {
			state := false
			config, err := a.pluginStore.Config(ctx, installation.InstanceID)
			if err != nil && !missingPluginValue(err) {
				return nil, err
			}
			if err == nil && exists && installation.DesiredEnabled {
				var flags struct {
					Enabled bool `json:"enabled"`
					Enable  bool `json:"enable"`
				}
				_ = json.Unmarshal(config.Value, &flags)
				state = flags.Enabled
				if installation.PluginID == "SystemProcess" {
					state = flags.Enable
				}
			}
			items = append(items, pluginCard{Manifest: manifest, InstanceID: installation.InstanceID, Installed: true, State: state})
		}
	}
	if market {
		for _, manifest := range builtin.Catalog() {
			if !seen[manifest.PluginID] {
				items = append(items, pluginCard{Manifest: manifest, InstanceID: manifest.PluginID})
			}
		}
	}
	return items, nil
}

func decodePluginBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
	}
	return nil
}

func (a *App) pluginForm(ctx context.Context, installation PluginInstallation) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case a.pluginProcess <- struct{}{}:
		defer func() { <-a.pluginProcess }()
	case <-ctx.Done():
		return nil, bad("PLUGIN_BUSY", "插件正在执行，请稍后重试")
	}
	manifest, _, exists := builtin.Lookup(installation.PluginID)
	if !exists || manifest.Version != installation.Version {
		return nil, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "此插件的Go实现尚未完成，不能配置或运行")
	}
	command, err := a.pluginExecutable(installation.PluginID)
	if err != nil {
		return nil, err
	}
	result, err := (plugins.Runner{Timeout: 10 * time.Second}).Run(ctx, command, manifest, installation.InstanceID, "config_form", nil, func(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
		return a.pluginHostCall(ctx, installation, nil, identity, method, raw)
	})
	if err != nil {
		return nil, bad("PLUGIN_FORM_FAILED", "加载插件配置表单失败")
	}
	var form struct {
		Conf  json.RawMessage            `json:"conf"`
		Model map[string]json.RawMessage `json:"model"`
	}
	if json.Unmarshal(result.Result, &form) != nil || form.Model == nil || !json.Valid(form.Conf) {
		return nil, bad("PLUGIN_FORM_FAILED", "插件配置表单返回格式无效")
	}
	stored, err := a.pluginStore.Config(ctx, installation.InstanceID)
	if err != nil && !missingPluginValue(err) {
		return nil, err
	}
	if err == nil {
		// The current catalog has no credential fields. Credentialed schemas must
		// define secret projections before joining this catalog.
		var config map[string]json.RawMessage
		if json.Unmarshal(stored.Value, &config) != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "插件配置无效")
		}
		for key := range form.Model {
			if value, exists := config[key]; exists {
				form.Model[key] = value
			}
		}
	}
	return map[string]any{"conf": form.Conf, "model": form.Model, "revision": stored.Revision}, nil
}

func (a *App) pluginAPI(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	if r.Method != "GET" {
		a.pluginLifecycleMu.Lock()
		defer a.pluginLifecycleMu.Unlock()
	}
	ctx := r.Context()
	if path == "" && r.Method == "GET" {
		return a.pluginCards(ctx, false)
	}
	if path == "market" && r.Method == "GET" {
		return a.pluginCards(ctx, true)
	}
	if path == "dashboard/meta" && r.Method == "GET" {
		return a.pluginDashboardMeta(ctx)
	}
	if path == "install" && r.Method == "POST" {
		var in struct {
			PluginID string `json:"plugin_id"`
		}
		if err := decodePluginBody(w, r, &in); err != nil {
			return nil, err
		}
		manifest, _, exists := builtin.Lookup(in.PluginID)
		if !exists {
			return nil, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "此插件尚无可安装的Go实现")
		}
		raw, _ := json.Marshal(manifest)
		installation := PluginInstallation{PluginID: in.PluginID, InstanceID: in.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}
		form, err := a.pluginForm(ctx, installation)
		if err != nil {
			return nil, err
		}
		a.pluginMu.Lock()
		run, err := a.pluginStore.InstallAndQueue(ctx, installation, form["model"])
		a.pluginMu.Unlock()
		if err == nil {
			a.wakePlugins()
		}
		return map[string]any{"run": run}, err
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !pluginStorageID.MatchString(parts[0]) {
		return nil, &APIError{"NOT_FOUND", "插件操作不存在", 404}
	}
	installation, err := a.pluginStore.Installation(ctx, parts[0])
	if err != nil {
		return nil, err
	}
	if !installation.Installed {
		return nil, &APIError{"NOT_FOUND", "插件已卸载", 404}
	}
	switch {
	case parts[1] == "page" && r.Method == "GET":
		return a.pluginView(ctx, installation, "page", "")
	case parts[1] == "dashboard" && r.Method == "GET":
		if !installation.DesiredEnabled {
			return nil, bad("PLUGIN_MIGRATION_STAGED", "迁移插件仍在暂存")
		}
		key := r.URL.Query().Get("key")
		if key == "" {
			key = "default"
		}
		return a.pluginView(ctx, installation, "dashboard", key)
	case parts[1] == "form" && r.Method == "GET":
		return a.pluginForm(ctx, installation)
	case parts[1] == "runs" && r.Method == "GET":
		return a.pluginStore.Runs(ctx, installation.InstanceID, 50)
	case parts[1] == "config" && r.Method == "PUT":
		if installation.PluginID != "CategoryEditor" && installation.PluginID != "CustomHosts" && installation.PluginID != "SystemProcess" && installation.PluginID != "MediaServerRefresh" {
			return nil, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "此插件的配置和业务尚未实现")
		}
		var in struct {
			Revision int64           `json:"revision"`
			Config   json.RawMessage `json:"config"`
		}
		if err := decodePluginBody(w, r, &in); err != nil {
			return nil, err
		}
		configValue, err := a.validateNativePluginConfig(installation.PluginID, in.Config)
		if err != nil {
			return nil, err
		}
		stored, err := a.pluginStore.Config(ctx, installation.InstanceID)
		if err != nil && !missingPluginValue(err) {
			return nil, err
		}
		if stored.Revision != in.Revision {
			return nil, bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请重新打开配置")
		}
		if err := a.drainMediaRefresh(ctx, installation); err != nil {
			return nil, err
		}
		a.pluginMu.Lock()
		config, run, err := a.pluginStore.SaveConfigAndQueue(ctx, installation.InstanceID, in.Revision, configValue)
		a.pluginMu.Unlock()
		if err == nil {
			a.wakePlugins()
		}
		return map[string]any{"revision": config.Revision, "run": run}, err
	case parts[1] == "reload" && r.Method == "POST":
		if !installation.DesiredEnabled {
			return nil, bad("PLUGIN_MIGRATION_STAGED", "迁移插件仍在暂存，请先核对并保存配置")
		}
		if err := a.drainMediaRefresh(ctx, installation); err != nil {
			return nil, err
		}
		a.pluginMu.Lock()
		run, err := a.pluginStore.EnqueueRun(ctx, installation.InstanceID, "reload", nil)
		a.pluginMu.Unlock()
		if err == nil {
			a.wakePlugins()
		}
		return map[string]any{"run": run}, err
	case parts[1] == "reset" && r.Method == "POST":
		if err := a.drainMediaRefresh(ctx, installation); err != nil {
			return nil, err
		}
		a.pluginMu.Lock()
		err := a.pluginStore.Reset(ctx, installation.InstanceID)
		a.pluginMu.Unlock()
		if err != nil {
			return nil, err
		}
		return map[string]bool{"reset": true}, nil
	case parts[1] == "uninstall" && r.Method == "POST":
		if err := a.drainMediaRefresh(ctx, installation); err != nil {
			return nil, err
		}
		a.pluginMu.Lock()
		err := a.pluginStore.Uninstall(ctx, installation.InstanceID)
		a.pluginMu.Unlock()
		return map[string]bool{"uninstalled": err == nil, "data_retained": true}, err
	default:
		return nil, &APIError{"NOT_FOUND", "插件操作不存在或方法不支持", 404}
	}
}

func (a *App) validateNativePluginConfig(pluginID string, raw json.RawMessage) (any, error) {
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return nil, bad("INVALID_PLUGIN_JSON", "插件配置必须是对象")
	}
	decode := func(value any) error {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
			return bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
		}
		return nil
	}
	switch pluginID {
	case "CategoryEditor":
		var config struct {
			Enabled bool   `json:"enabled"`
			Content string `json:"content"`
		}
		if err := decode(&config); err != nil {
			return nil, err
		}
		if config.Enabled {
			if _, err := parseMediaCategoryPolicy([]byte(config.Content)); err != nil {
				return nil, err
			}
		}
		return config, nil
	case "CustomHosts":
		var config builtin.CustomHostsConfiguration
		if err := decode(&config); err != nil {
			return nil, err
		}
		if err := config.Validate(); err != nil {
			return nil, bad("INVALID_HOSTS_VALUE", "Hosts配置必须为文本或文本行数组，且不超过128 KiB")
		}
		return config, nil
	case "SystemProcess":
		var config builtin.SystemProcessConfiguration
		if err := decode(&config); err != nil {
			return nil, err
		}
		if _, _, err := config.Display(); err != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "仪表板尺寸必须选择列表中的值，刷新间隔须为0到86400秒或留空")
		}
		return config, nil
	case "MediaServerRefresh":
		var config builtin.MediaServerRefreshConfiguration
		if err := decode(&config); err != nil {
			return nil, err
		}
		if _, err := config.Duration(); err != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "媒体库刷新延迟或媒体服务器引用无效")
		}
		for _, server := range config.MediaServers {
			if server != "fnos" {
				return nil, bad("PLUGIN_MEDIA_SERVER_UNMAPPED", "媒体服务器引用尚未映射")
			}
		}
		return config, nil
	default:
		return nil, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "此插件尚无配置校验实现")
	}
}
