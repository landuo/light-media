package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/directorysource"
	"light-media/internal/noticetemplate"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
	"light-media/internal/transferextensions"
)

// Persist source projections so preparation, explicit preview and the native
// execution branch use the same full metadata. Pure executable-plan preparation
// renders its default path without emitting the native provider's events.
type originalTransferFileContext struct {
	Item     *plugins.ModuleFileItem `json:"fileitem,omitempty"`
	Meta     json.RawMessage         `json:"meta"`
	Media    json.RawMessage         `json:"mediainfo"`
	Episodes json.RawMessage         `json:"episodes_info,omitempty"`
}

func (a *App) originalTransferHasHandlers() bool {
	for _, object := range a.originalLoadedPluginSnapshot() {
		if object.external && slices.Contains(object.manifest.Methods, "get_module") {
			return true
		}
	}
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	for _, name := range []string{"transfer.rename.build", "transfer.rename", "transfer.intercept", "transfer.overwrite.check", "storage.operation"} {
		if len(a.pluginOriginalChainHandlers[name]) > 0 {
			return true
		}
	}
	for _, name := range []string{"transfer.complete", "transfer.failed", "transfer.subtitle.complete", "transfer.subtitle.failed", "transfer.audio.complete", "transfer.audio.failed", "metadata.scrape"} {
		if len(a.pluginOriginalBroadcast.handlers[name]) > 0 {
			return true
		}
	}
	return false
}

func (a *App) originalTransferContext(ctx context.Context, identity catalog.Media, file PlanFile) (originalTransferFileContext, error) {
	return a.originalTransferContextWithMedia(ctx, identity, file, nil)
}

func (a *App) originalTransferContextWithMedia(ctx context.Context, identity catalog.Media, file PlanFile, resolved json.RawMessage, parsed ...*releasemeta.MetaObject) (originalTransferFileContext, error) {
	var out originalTransferFileContext
	var meta *releasemeta.MetaObject
	if len(parsed) > 0 {
		meta = parsed[0]
	}
	if meta == nil {
		options, err := a.globalRecognitionOptions(ctx)
		if err != nil {
			return out, err
		}
		parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
		if err != nil {
			return out, err
		}
		meta, err = parser.ParsePath(file.Source, nil)
		if err != nil {
			return out, err
		}
	}
	// Explicit manual identity/episode selection is the existing transfer task's
	// metadata, just as the source clears a single file's end season/episode.
	if identity.MediaType == "tv" {
		meta.Set("type", "电视剧")
		meta.Set("begin_season", file.Season)
		meta.Set("end_season", nil)
		if len(file.Episodes) > 0 {
			meta.Set("begin_episode", file.Episodes[0])
			meta.Set("end_episode", nil)
			if len(file.Episodes) > 1 {
				meta.Set("end_episode", file.Episodes[len(file.Episodes)-1])
			}
			meta.Set("total_episode", len(file.Episodes))
		}
	} else {
		meta.Set("type", "电影")
	}
	if file.Part != "" {
		meta.Set("part", file.Part)
	}
	state, err := meta.EncodeState()
	if err != nil {
		return out, err
	}
	fields, err := sourceNoticeMeta(state)
	if err != nil {
		return out, err
	}
	out.Meta, err = json.Marshal(fields)
	if err != nil {
		return out, err
	}
	if len(resolved) == 0 {
		media, err := a.sourceRecognizeTMDBMedia(ctx, identity)
		if err != nil {
			return out, err
		}
		resolved = media.MediaInfo
	}
	out.Media = resolved
	return out, nil
}

// These are the host's existing naming formats expressed as templates. The
// plugin platform supplies the source TemplateHelper context and callback order;
// it does not replace the application's configured naming policy.
const originalNativeMovieNaming = "{{title}}{% if year %} ({{year}}){% else %} (tmdb-{{tmdbid}}){% endif %}/{{title}}{% if year %} ({{year}}){% else %} (tmdb-{{tmdbid}}){% endif %}{% if part %}-{{part}}{% endif %}{{fileExt}}"
const originalNativeTVNaming = "{{title}}{% if year %} ({{year}}){% else %} (tmdb-{{tmdbid}}){% endif %}/Season {{season}}/{{title}} {{season_episode}}{% if part %}-{{part}}{% endif %}{{fileExt}}"

func originalNativeTransferNaming(identity catalog.Media) string {
	template := originalNativeMovieNaming
	if identity.MediaType == "tv" {
		template = originalNativeTVNaming
	}
	if providerNativeMedia(identity) {
		template = strings.ReplaceAll(template, "tmdb-{{tmdbid}}", "{{media_source}}-{{media_id}}")
	}
	return template
}

func (a *App) originalTransferNativeRename(ctx context.Context, identity catalog.Media, file PlanFile, base string, source originalTransferFileContext, episodes []catalog.Episode, observe ...bool) (string, error) {
	item, err := originalTransferSourceItem(source, file)
	if err != nil {
		return "", err
	}
	var itemFields map[string]any
	encoded, err := json.Marshal(item)
	if err != nil || json.Unmarshal(encoded, &itemFields) != nil {
		return "", bad("PLUGIN_TRANSFER_CONTEXT", "整理文件项无法读取")
	}
	meta, err := decodeSourceJSON(source.Meta)
	if err != nil {
		return "", err
	}
	media, err := decodeSourceJSON(source.Media)
	if err != nil {
		return "", err
	}
	extension := sourceText(itemFields["extension"])
	if extension != "" {
		extension = "." + extension
	}
	mediaFields := sourceMap(media)
	if mediaFields != nil {
		// The source TemplateHelper uses image getters. MediaInfo.to_dict
		// retains the paths, while the naming context needs the getter results.
		for _, name := range []string{"poster", "backdrop"} {
			if _, present := mediaFields[name]; !present {
				mediaFields[name] = strings.ReplaceAll(sourceText(mediaFields[name+"_path"]), "original", "w500")
			}
		}
	}
	input := noticetemplate.ContextInput{Meta: sourceMap(meta), Media: mediaFields, FileExtension: extension}
	for _, episode := range episodes {
		raw, err := json.Marshal(episode)
		if err != nil {
			return "", err
		}
		value, err := decodeSourceJSON(raw)
		if err != nil {
			return "", err
		}
		input.Episodes = append(input.Episodes, sourceMap(value))
	}
	variables, err := noticetemplate.BuildContext(input)
	if err != nil {
		return "", err
	}
	template := originalNativeTransferNaming(identity)
	path, err := a.originalTransferRenamePath(ctx, template, variables, &base, &file.Source, item, observe...)
	if err != nil {
		return "", err
	}
	if sourceText(itemFields["type"]) == "dir" {
		if identity.MediaType == "tv" {
			return filepath.Join(filepath.Dir(path), originalTransferDiscFolder(sourceText(sourceMap(meta)["part"]), originalTransferItemName(itemFields))), nil
		}
		root, err := directorysource.MediaRoot(&template, path)
		if err != nil {
			return "", err
		}
		if root == nil {
			return "", bad("PLUGIN_EVENT_INVALID", "蓝光目录的重命名格式无效")
		}
		return *root, nil
	}
	snapshot, err := a.sourceFileExtensions(ctx)
	if err != nil {
		return "", err
	}
	classifier, err := transferextensions.New(snapshot)
	if err != nil {
		return "", err
	}
	if classifier.Subtitle(originalTransferItemClass(itemFields)) {
		defaultSub, err := a.originalDefaultSubtitle()
		if err != nil {
			return "", err
		}
		return originalTransferSubtitleItemName(originalTransferItemName(itemFields), sourceText(itemFields["extension"]), path, defaultSub)
	}
	return path, nil
}
