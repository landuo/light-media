package app

import (
	"context"
	"encoding/json"

	"light-media/internal/media"
	"light-media/internal/releasemeta"
)

// MoviePilot's ordinary recognition chain uses the global RECOGNIZE_SOURCE for
// automatic directory jobs too. The TMDB matcher stays the default; any other
// configured source resolves the same parsed meta through the source chain and
// carries the provider identity into the plan.
func (a *App) directoryProviderIdentity(ctx context.Context, request ImportRequest, file importScanFile, source string) (ImportRequest, error) {
	p := file.Parsed
	if p.Status != media.Parsed || p.Title == "" {
		return request, bad("DIRECTORY_IDENTITY_REVIEW", "文件名缺少明确影视身份，请在“手动整理”中选择电影或电视剧类型，并查询或填写来源编号")
	}
	kind := "movie"
	if p.Type == "episode" {
		kind = "tv"
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return request, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return request, err
	}
	meta, err := parser.ParsePath(file.Path, nil)
	if err != nil {
		return request, err
	}
	meta.Set("type", map[string]string{"movie": "电影", "tv": "电视剧"}[kind])
	recognized, err := a.originalCatalogRecognizeByMetaMethod(ctx, "recognize_media", meta, func(current *releasemeta.MetaObject) (json.RawMessage, error) {
		fields, e := current.ToDict()
		if e != nil {
			return nil, e
		}
		kwargs, e := originalCatalogRecognitionCallKwargs(fields, map[string]any{"source": source, "mtype": meta.Get("type")})
		if e != nil {
			return nil, e
		}
		return a.originalHostMediaRecognizeNative(ctx, kwargs)
	})
	if err != nil {
		return request, err
	}
	if !originalModuleResultTruthy(recognized) {
		return request, originalRecognitionMissing()
	}
	if recognized, err = a.originalTransferObtainImages(ctx, recognized); err != nil {
		return request, err
	}
	view, err := originalCatalogMediaProjection(recognized)
	if err != nil || view.Source != source || view.SourceID == "" || view.MediaType != kind {
		return request, bad("DIRECTORY_IDENTITY_REVIEW", "来源识别结果与目录影视类型不一致，请在“手动整理”中确认")
	}
	provider := originalChatMediaProvider{Source: source, ID: view.SourceID}
	info, err := originalChatProviderSearchInformation(recognized, &provider, kind)
	if err != nil || !providerNativeMedia(info.Media) {
		return request, bad("DIRECTORY_IDENTITY_REVIEW", "来源识别结果缺少可核验的媒体编号，请在“手动整理”中确认")
	}
	request.MediaType, request.TMDBID = kind, 0
	request.providerIdentity, request.originalResolvedMedia = info.Media, info.MediaInfo
	request.originalDirectoryProvider = true
	if kind == "tv" {
		numbering := p.Original
		if err := file.validateImportNumbering(numbering); err != nil {
			return request, err
		}
		request.Files[0].Season = *numbering.Season
		request.Files[0].SeasonExplicit = true
		request.Files[0].Episodes = append([]int(nil), numbering.Episodes...)
	}
	return request, nil
}
