package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

func (a *App) assetOriginalNFO(ctx context.Context, path string, identity catalog.Media, episode *catalog.Episode) ([]byte, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return nil, err
	}
	if !originalModuleGroupsHaveMethod(groups, "metadata_nfo") {
		return assetNFO(identity, episode)
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	meta, err := parser.ParsePath(path, nil)
	if err != nil {
		return nil, err
	}
	information, err := a.sourceRecognizeTMDBCore(ctx, identity)
	if err != nil {
		return nil, err
	}
	var seasonNumber, episodeNumber any
	if episode != nil {
		seasonNumber, episodeNumber = episode.SeasonNumber, episode.EpisodeNumber
	}
	return a.originalMetadataNFO(ctx, meta, information.MediaInfo, seasonNumber, episodeNumber, func() ([]byte, error) {
		return assetNFO(identity, episode)
	}, groups)
}

// MediaChain.metadata_nfo forwards exactly these four arguments. Bytes remain
// terminal module scalars; only the later _save_file consumer tests truth and
// encodes strings. A list runs core but is still the accumulated list result.
func (a *App) originalMetadataNFO(ctx context.Context, meta *releasemeta.MetaObject, information json.RawMessage, season, episode any, native func() ([]byte, error), groups []originalPluginModuleGroup) ([]byte, error) {
	fields, err := meta.ToDict()
	if err != nil {
		return nil, err
	}
	kwargs, err := json.Marshal(map[string]any{"meta": fields, "mediainfo": information, "season": season, "episode": episode})
	if err != nil {
		return nil, err
	}
	result, err := a.invokeOriginalPluginModuleGroups(ctx, "metadata_nfo", nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return nil, err
	}
	callCore := func() ([]byte, error) {
		content, err := native()
		if err != nil && ctx.Err() == nil {
			a.reportOriginalPluginExecutionError(ctx, "system", "TheMovieDb发生了错误", map[string]any{"type": "module", "module_id": "TheMovieDbModule", "module_name": "TheMovieDb", "module_method": "metadata_nfo"}, err)
			return nil, nil
		}
		return content, err
	}
	if originalModuleResultEmpty(result) {
		return callCore()
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil && list != nil {
		if _, err = callCore(); err != nil {
			return nil, err
		}
	}
	if !originalModuleResultTruthy(result) {
		return nil, nil
	}
	if content, ok := plugins.DecodeModuleResultBytes(result); ok {
		return content, nil
	}
	var content string
	if json.Unmarshal(result, &content) != nil {
		return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "NFO模块返回的内容不支持UTF-8编码")
	}
	return []byte(content), nil
}
