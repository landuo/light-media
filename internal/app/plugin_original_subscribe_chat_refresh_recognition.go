package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
	"light-media/internal/rsscontext"
)

type originalChatLatestMedia map[string]any

func (m originalChatLatestMedia) Get(key string) any { return m[key] }

func originalChatLatestMediaFields(raw json.RawMessage) originalChatLatestMedia {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return nil
	}
	return originalChatLatestMedia(sourceMap(value))
}

func originalChatLatestDownloadMedia(raw json.RawMessage, target catalog.Media) (json.RawMessage, string) {
	media := originalChatLatestMediaFields(raw)
	if media.Get("type") != map[string]string{"movie": "电影", "tv": "电视剧"}[target.MediaType] {
		return nil, "title"
	}
	if providerNativeMedia(target) {
		identity := rsscontext.ResolveIdentity(media)
		if identity.Source != target.ProviderSource || identity.ID != target.ProviderID {
			return nil, "title"
		}
		return raw, target.ProviderSource + "id"
	}
	if sourceStringValue(media.Get("tmdb_id")) != strconv.FormatInt(target.ID, 10) {
		return nil, "title"
	}
	return raw, "tmdbid"
}

func originalChatLatestParser(options releasemeta.Options) (*releasemeta.MetaInfoParser, error) {
	return releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
}

func (a *App) originalChatLatestRecognizeSite(ctx context.Context, site *originalChatLatestSite, mode string, uncached bool, parser *releasemeta.MetaInfoParser, invalidRSS map[string]struct{}) error {
	var old originalChatLatestSite
	if !uncached || len(site.Rows) == 0 {
		if err := a.originalChatLatestRead(ctx, a.Store.DB, originalChatLatestCacheKind, mode+":"+site.SiteID, &old); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old.Key != site.Key || old.SiteID != site.SiteID {
			old.Rows = nil
		}
		old.Rows = originalChatLatestPruneInvalidRSS(old.Rows, invalidRSS)
	}
	prior := map[string]bool{}
	site.rechecked = map[string]originalChatLatestRow{}
	for _, row := range old.Rows {
		signature := originalChatLatestSignature(row.Resource)
		prior[signature] = true
		if len(row.Media) == 0 && row.Failures < 3 {
			a.originalChatLatestRecognize(ctx, &row, parser)
			site.rechecked[signature] = row
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	for i := range site.Rows {
		row := &site.Rows[i]
		if prior[originalChatLatestSignature(row.Resource)] || len(row.Media) != 0 {
			continue
		}
		// Source first recognizes a new torrent when refreshing the cache,
		// then retries the same unrecognized context in match preprocessing.
		a.originalChatLatestRecognize(ctx, row, parser)
		if len(row.Media) == 0 && ctx.Err() == nil {
			a.originalChatLatestRecognize(ctx, row, parser)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) originalChatLatestRecognize(ctx context.Context, row *originalChatLatestRow, parser *releasemeta.MetaInfoParser) {
	subtitle := row.Resource.SourceSubtitle
	if subtitle == nil && row.Resource.Subtitle != "" {
		subtitle = &row.Resource.Subtitle
	}
	meta, err := parser.ParseTitle(row.Resource.Title, subtitle, nil)
	if err == nil {
		if fields := originalChatLatestMediaFields(row.Source); fields.Get("category") == "电视剧" {
			meta.Set("type", "电视剧")
		}
		row.MetaName = meta.Text("org_string")
		recognized, err := a.originalCatalogRecognizeByMetaMethod(ctx, "recognize_media", meta, func(current *releasemeta.MetaObject) (json.RawMessage, error) {
			if a.originalHostMediaRecognition.Source == "" || a.originalHostMediaRecognition.Source == "themoviedb" {
				return a.originalMetadataRecognizeNative(ctx, current)
			}
			fields, err := current.ToDict()
			if err != nil {
				return nil, err
			}
			kwargs, err := originalCatalogRecognitionMetaKwargs(fields)
			if err != nil {
				return nil, err
			}
			return a.originalHostMediaRecognizeNative(ctx, kwargs)
		})
		if err == nil && rsscontext.ResolveIdentity(originalChatLatestMediaFields(recognized)).ID != "" {
			if clean, err := originalClearMediaSearchInformation(recognized); err == nil {
				row.Media, row.Failures = clean, 0
				return
			}
		}
	}
	row.Failures++
}
