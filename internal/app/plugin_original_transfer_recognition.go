package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

// TransferChain.__handle_transfer uses the ordinary synchronous recognition
// chain, then obtains images on that same MediaInfo before planning a transfer.
// Keep existing native identity checks first, then use MoviePilot's TV
// matcher when the strict directory lookup cannot identify the file.
func (a *App) originalManualTransferIdentity(ctx context.Context, request ImportRequest, file ImportFileRequest, scanned importScanFile) (ImportRequest, error) {
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return request, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return request, err
	}
	path := file.Path
	if file.originalMetadataPath != "" {
		path = file.originalMetadataPath
	}
	meta := file.originalResolvedMeta.DeepCopy()
	if meta == nil {
		meta, err = parser.ParsePath(path, nil)
	}
	if err != nil {
		return request, err
	}
	if file.SeasonExplicit || file.Season != 0 {
		meta.Set("begin_season", file.Season)
	}
	if len(file.Episodes) > 0 {
		meta.Set("begin_episode", file.Episodes[0])
		meta.Set("end_episode", nil)
		if len(file.Episodes) > 1 {
			meta.Set("end_episode", file.Episodes[len(file.Episodes)-1])
		}
	}
	if file.Part != "" {
		meta.Set("part", file.Part)
	}
	initialName, initialYear := meta.Name(), meta.Text("year")
	recognized, err := a.originalCatalogRecognizeByMetaMethod(ctx, "recognize_media", meta, func(current *releasemeta.MetaObject) (json.RawMessage, error) {
		// NameRecognize may have changed the name/year before this retry.
		if current.Name() != initialName || current.Text("year") != initialYear {
			return a.originalMetadataRecognizeNative(ctx, current)
		}
		native, err := a.directoryFileIdentity(ctx, DirectoryRule{MediaType: "mixed", Mode: request.Mode}, scanned)
		if err != nil {
			if !manualTVRecognitionFallback(request, file, scanned, err) {
				return nil, err
			}
			fields, err := current.ToDict()
			if err != nil {
				return nil, err
			}
			kwargs, err := originalCatalogRecognitionCallKwargs(fields, map[string]any{"source": "themoviedb", "mtype": "电视剧"})
			if err != nil {
				return nil, err
			}
			return a.originalHostMediaRecognizeTMDB(ctx, kwargs)
		}
		full, err := a.sourceRecognizeTMDBCore(ctx, catalog.Media{ID: native.TMDBID, MediaType: native.MediaType})
		return full.MediaInfo, err
	})
	if err != nil {
		return request, err
	}
	if !originalModuleResultTruthy(recognized) {
		return request, originalRecognitionMissing()
	}
	recognized, err = a.originalTransferObtainImages(ctx, recognized)
	if err != nil {
		return request, err
	}
	identity, err := originalCatalogMediaProjection(recognized)
	if err != nil {
		return request, err
	}
	if !validMedia(identity.MediaType, identity.ID) {
		return request, bad("IMPORT_IDENTITY_REVIEW", "当前整理需要可核验的TMDB身份")
	}
	out := ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, originalResolvedMedia: recognized, Files: []ImportFileRequest{file}}
	out.Files[0].originalResolvedMeta = meta
	if identity.MediaType == "tv" {
		if n := meta.Number("begin_season"); n != nil && n.IsInt64() && n.Sign() >= 0 && n.Int64() <= 1000 {
			out.Files[0].Season = int(n.Int64())
			out.Files[0].SeasonExplicit = true
		}
		episodes, e := meta.EpisodeList()
		if e != nil {
			return request, e
		}
		out.Files[0].Episodes = nil
		for _, n := range episodes {
			if !n.IsInt64() || n.Sign() <= 0 || n.Int64() > 9999 {
				return request, bad("DIRECTORY_NUMBERING_REVIEW", "剧集编号超出受支持范围")
			}
			out.Files[0].Episodes = append(out.Files[0].Episodes, int(n.Int64()))
		}
	}
	return out, nil
}

func manualTVRecognitionFallback(request ImportRequest, file ImportFileRequest, scanned importScanFile, err error) bool {
	if request.MediaType == "movie" || scanned.InferredMedia != nil || scanned.IdentityReason != "" {
		return false
	}
	var api *APIError
	if !errors.As(err, &api) || api.Code != "DIRECTORY_IDENTITY_REVIEW" {
		return false
	}
	return request.MediaType == "tv" || scanned.Parsed.Type == "episode" ||
		file.originalResolvedMeta != nil && file.originalResolvedMeta.Text("type") == "电视剧"
}

func (a *App) originalManualRecognitionAvailable() bool {
	return a.originalTransferHasHandlers() || a.originalEventHasHandler(plugins.EventQuery{Kind: plugins.ChainEventKind, Type: "name.recognize"})
}

func (a *App) originalTransferObtainImages(ctx context.Context, recognized json.RawMessage) (json.RawMessage, error) {
	return a.originalObtainCatalogImagesMethod(ctx, "obtain_images", recognized, func(raw json.RawMessage) (json.RawMessage, error) {
		return originalCatalogTMDBImages(raw, "themoviedb", func(kind string, id any, language any) (json.RawMessage, error) {
			parsed, e := strconv.ParseInt(sourceStringValue(id), 10, 64)
			if e != nil {
				return nil, e
			}
			return a.catalog().SourceImages(ctx, kind, parsed, "zh", sourceText(language))
		}, a.Settings().TMDBImageDomain)
	})
}
