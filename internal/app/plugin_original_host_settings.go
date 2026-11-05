package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/fileextensions"
	"light-media/internal/media/words"
	"light-media/internal/plugins"
)

func originalHostSettingsCapability(method string) bool {
	return method == plugins.SettingsReadCapability || method == plugins.SettingsWriteCapability
}
func (a *App) originalHostSettingsResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	var in plugins.SettingsHostWriteV1
	if method == plugins.SettingsReadCapability {
		var read plugins.SettingsHostRequestV1
		if err := originalResourceDecode(raw, &read); err != nil {
			return nil, err
		}
		in.Key = read.Key
	} else {
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		if len(in.Value) == 0 || !json.Valid(in.Value) {
			return nil, bad("INVALID_PLUGIN_VALUE", "系统设置值无效")
		}
	}
	write := method == plugins.SettingsWriteCapability
	out := plugins.SettingsHostResultV1{SchemaVersion: 1, Key: in.Key, Value: json.RawMessage(`null`)}
	switch in.Key {
	case "USER_AGENT":
		if write {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "宿主HTTP身份只读")
		}
		out.Value = mustCleanSeedJSON(originalPluginServerUserAgent())
	case fileextensions.MediaKey:
		if write {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "宿主媒体扩展名设置只读")
		}
		defaults, err := fileextensions.Resolve(fileextensions.Inputs{Namespace: "native.settings", SourceID: "readonly-default", EnvironmentKnown: true, ConfigurationKnown: true})
		if err != nil {
			return nil, err
		}
		fallback := mustCleanSeedJSON(defaults.Values(fileextensions.MediaKey))
		raw := a.Settings().RMTMediaExt
		if len(raw) == 0 {
			raw = fallback
		}
		out.Value, err = fileextensions.ResolveSettingsJSONList(raw, fallback)
		if err != nil {
			return nil, err
		}
	case "BIG_MEMORY_MODE", "META_CACHE_EXPIRE", "TMDB_LOCALE", "RECOGNIZE_SOURCE":
		// These names come from core.config.settings, not SystemConfigOper's
		// database. The process boundary exposes their startup snapshot only.
		if write {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "宿主启动设置只读")
		}
		switch in.Key {
		case "BIG_MEMORY_MODE":
			out.Value = mustCleanSeedJSON(a.bigMemoryMode)
		case "META_CACHE_EXPIRE":
			out.Value = mustCleanSeedJSON(a.originalHostMediaRecognition.MetaCacheExpire)
		case "TMDB_LOCALE":
			out.Value = mustCleanSeedJSON(a.originalHostMediaRecognition.Locale)
		case "RECOGNIZE_SOURCE":
			out.Value = mustCleanSeedJSON(a.originalHostMediaRecognition.Source)
		}
	case "MEDIA_RECOGNIZE_SHARE", "MEDIA_RECOGNIZE_SHARE_API":
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		config := a.Settings()
		if in.Key == "MEDIA_RECOGNIZE_SHARE" {
			value := config.MediaRecognizeShare == nil || *config.MediaRecognizeShare
			out.Value = mustCleanSeedJSON(value)
			if write {
				var next bool
				if json.Unmarshal(in.Value, &next) != nil || string(in.Value) == "null" {
					return nil, bad("INVALID_PLUGIN_VALUE", "共享识别开关须为布尔值")
				}
				if next != value {
					if err := scope.validate(ctx); err != nil {
						return nil, err
					}
					config.MediaRecognizeShare = &next
					if err := a.saveSettings(ctx, config); err != nil {
						return nil, err
					}
					changed := true
					out.Changed = &changed
					out.Value = mustCleanSeedJSON(next)
				}
			}
		} else {
			out.Value = mustCleanSeedJSON(config.MediaRecognizeShareAPI)
			if write {
				var next string
				if json.Unmarshal(in.Value, &next) != nil || string(in.Value) == "null" {
					return nil, bad("INVALID_PLUGIN_VALUE", "共享识别地址须为字符串")
				}
				if err := validateHostMediaRecognizeShareAPI(next); err != nil {
					return nil, err
				}
				if next != config.MediaRecognizeShareAPI {
					if err := scope.validate(ctx); err != nil {
						return nil, err
					}
					config.MediaRecognizeShareAPI = next
					if err := a.saveSettings(ctx, config); err != nil {
						return nil, err
					}
					changed := true
					out.Changed = &changed
					out.Value = mustCleanSeedJSON(next)
				}
			}
		}
	case "MediaRecognizeShareCount":
		if write {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "共享识别计数仅由成功的宿主识别更新")
		}
		var count int64
		if err := a.Store.Get(ctx, "plugin_host_media_recognize_count", "main", &count); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		out.Value = mustCleanSeedJSON(count)
	case "CustomIdentifiers":
		a.recognitionMu.Lock()
		defer a.recognitionMu.Unlock()
		revision, rules, err := a.recognitionWords(ctx)
		if err != nil {
			return nil, err
		}
		old := mustCleanSeedJSON(rules)
		if revision == 0 {
			old = a.originalHostSettingClearedValue(in.Key)
		}
		out.Value = old
		if write {
			var next []string
			if json.Unmarshal(in.Value, &next) != nil {
				return nil, bad("INVALID_RECOGNITION_WORDS", "识别词须为字符串数组或null")
			}
			nextRaw := mustCleanSeedJSON(next)
			if revision == 0 || !originalBaseJSONEqual(old, nextRaw) {
				if err = scope.validate(ctx); err != nil {
					return nil, err
				}
				if revision > 0 && len(next) == 0 {
					matcher, err := words.New(nil)
					if err != nil {
						return nil, err
					}
					result, err := a.Store.DB.ExecContext(ctx, `DELETE FROM recognition_words WHERE id='CustomIdentifiers' AND revision=?`, revision)
					if err != nil {
						return nil, err
					}
					if n, _ := result.RowsAffected(); n != 1 {
						return nil, bad("RECOGNITION_REVISION_CONFLICT", "识别词已变化，本次未覆盖")
					}
					a.recognitionMatcher.Store(matcher)
					a.originalHostSettingRememberCleared(in.Key, nextRaw)
				} else {
					if err = a.saveRecognitionWordsLocked(ctx, revision, next); err != nil {
						return nil, err
					}
					a.originalHostSettingRememberCleared(in.Key, nil)
				}
				changed := true
				out.Changed = &changed
				out.Value = nextRaw
			}
		}
	case "RssSites", "IndexerSites":
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		config := a.Settings()
		ids := config.RSSSites
		if in.Key == "IndexerSites" {
			ids = config.IndexerSites
		}
		exists := ids != nil
		if !exists {
			var present bool
			err := a.Store.Get(ctx, "plugin_host_setting_presence", in.Key, &present)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			exists = present
		}
		old := mustCleanSeedJSON(ids)
		if !exists {
			old = a.originalHostSettingClearedValue(in.Key)
		}
		out.Value = old
		if write {
			var next []string
			if json.Unmarshal(in.Value, &next) != nil {
				return nil, bad("INVALID_SITES", "站点选择须为稳定ID数组或null")
			}
			known := map[string]bool{}
			for _, site := range config.Sites {
				known[site.ID] = true
			}
			for _, id := range next {
				if !known[id] {
					return nil, bad("INVALID_SITES", "系统站点选择含不存在的ID")
				}
			}
			nextRaw := mustCleanSeedJSON(next)
			if !exists || !originalBaseJSONEqual(old, nextRaw) {
				if err := scope.validate(ctx); err != nil {
					return nil, err
				}
				persist := next
				clear := exists && len(next) == 0
				if clear {
					persist = nil
				}
				if in.Key == "IndexerSites" {
					config.IndexerSites = persist
				} else {
					config.RSSSites = persist
				}
				// The marker distinguishes a newly-created null value from an
				// absent/deleted key; this is finite source-setting metadata.
				a.pluginMu.Lock()
				err := a.saveSettingsWithEffect(ctx, config, func(effectCtx context.Context, tx *sql.Tx, _ *Settings) error {
					if !clear && next == nil {
						return txPut(effectCtx, tx, "plugin_host_setting_presence", in.Key, true)
					}
					_, err := tx.ExecContext(effectCtx, `DELETE FROM records WHERE kind='plugin_host_setting_presence' AND id=?`, in.Key)
					return err
				})
				a.pluginMu.Unlock()
				if err != nil {
					return nil, err
				}
				if clear {
					a.originalHostSettingRememberCleared(in.Key, nextRaw)
				} else {
					a.originalHostSettingRememberCleared(in.Key, nil)
				}
				changed := true
				out.Changed = &changed
				out.Value = nextRaw
			}
		}
	case "UserInstalledPlugins":
		if write {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "安装清单须通过插件生命周期操作修改")
		}
		ids, err := a.originalInstalledPluginIDs(ctx)
		if err != nil {
			return nil, err
		}
		out.Value = mustCleanSeedJSON(ids)
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "该全局设置未开放为普通插件资源")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// Source SystemConfigOper keeps a falsey value in this process after deleting
// its persistent row. A restart discards this cache and Get returns null.
func (a *App) originalHostSettingClearedValue(key string) json.RawMessage {
	a.originalHostSettingsMu.Lock()
	defer a.originalHostSettingsMu.Unlock()
	if value, ok := a.originalHostSettingsValues[key]; ok {
		return append(json.RawMessage(nil), value...)
	}
	return json.RawMessage(`null`)
}
func (a *App) originalHostSettingRememberCleared(key string, value json.RawMessage) {
	a.originalHostSettingsMu.Lock()
	defer a.originalHostSettingsMu.Unlock()
	if len(value) == 0 {
		delete(a.originalHostSettingsValues, key)
		return
	}
	if a.originalHostSettingsValues == nil {
		a.originalHostSettingsValues = map[string]json.RawMessage{}
	}
	a.originalHostSettingsValues[key] = append(json.RawMessage(nil), value...)
}

// Source Settings constructs its environment once, then update_setting mutates
// the live field. Seed only this finite host field after loading native JSON;
// later real settings saves replace it normally. No environment/config is
// rewritten during startup, and the existing classifier algorithm is unchanged.
func (a *App) originalHostInitializeMediaExtensions(lookup func(string) (string, bool)) error {
	value, present := lookup(fileextensions.MediaKey)
	if !present {
		return nil
	}
	defaults, err := fileextensions.Resolve(fileextensions.Inputs{Namespace: "native.settings", SourceID: "startup-default", EnvironmentKnown: true, ConfigurationKnown: true})
	if err != nil {
		return err
	}
	normalized, err := fileextensions.ResolveSettingsJSONList(json.RawMessage(value), mustCleanSeedJSON(defaults.Values(fileextensions.MediaKey)))
	if err != nil {
		return err
	}
	a.config.RMTMediaExt = normalized
	return nil
}
