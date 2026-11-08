package app

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/fileextensions"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

// The event shares the existing native parser, metadata provider and authorized
// local storage. It neither requires nor inserts an asset index record.
func (a *App) scrapeOriginalLocalMetadata(ctx context.Context, kwargs map[string]any) error {
	item := sourceMap(kwargs["fileitem"])
	path := strings.TrimSuffix(sourceText(item["path"]), "/")
	if err := a.originalMetadataAllowed(path); err != nil {
		return err
	}
	stamp, err := browserStat(path)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, originalMetadataSnapshotKey{}, stamp)
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return err
	}
	if item["type"] == "file" {
		extensions, err := a.sourceFileExtensions(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, ext := range extensions.Fields[fileextensions.MediaKey].Values {
			found = found || strings.ToLower(filepath.Ext(path)) == ext
		}
		if !found || filepath.Ext(path) == "" {
			return nil
		}
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return err
	}
	var meta *releasemeta.MetaObject
	if fields := sourceMap(kwargs["meta"]); len(fields) != 0 {
		meta = releasemeta.EmptyMetaObject()
		for key, value := range fields {
			meta.Set(key, value)
		}
	} else if meta, err = parser.ParsePath(path, nil); err != nil {
		return err
	}
	info := sourceMap(kwargs["mediainfo"])
	if len(info) == 0 {
		raw, err := a.originalCatalogRecognizeByMetaMethod(ctx, "recognize_media", meta, func(meta *releasemeta.MetaObject) (json.RawMessage, error) {
			if a.originalHostMediaRecognition.Source == "themoviedb" {
				return a.originalMetadataRecognizeNative(ctx, meta)
			}
			fields, err := meta.ToDict()
			if err != nil {
				return nil, err
			}
			input, err := originalCatalogRecognitionMetaKwargs(fields)
			if err != nil {
				return nil, err
			}
			return a.originalHostMediaRecognizeNative(ctx, input)
		})
		if err != nil {
			return err
		}
		if !originalModuleResultTruthy(raw) {
			return nil
		}
		value, err := decodeSourceJSON(raw)
		if err != nil {
			return err
		}
		info = sourceMap(value)
	}
	if info == nil {
		return originalCatalogMediaInvalid()
	}
	initFolder, recursive := true, true
	if value, present := kwargs["init_folder"]; present {
		initFolder = sourceSubscriptionTruth(value)
	}
	if value, present := kwargs["recursive"]; present {
		recursive = sourceSubscriptionTruth(value)
	}
	overwrite := sourceSubscriptionTruth(kwargs["overwrite"])
	if item["type"] == "file" {
		var season, episode any
		if info["type"] != "电影" {
			// The fixed TV file branch parses the path a second time and asks
			// recognize_media even when the event supplied a MediaInfo object.
			meta, err = parser.ParsePath(path, nil)
			if err != nil {
				return err
			}
			if number := meta.Number("begin_episode"); number == nil || number.Sign() == 0 {
				return nil
			}
			fields, err := meta.ToDict()
			if err != nil {
				return err
			}
			canonical, err := decodeSourceJSON(mustCleanSeedJSON(fields))
			if err != nil {
				return err
			}
			fields = sourceMap(canonical)
			var raw json.RawMessage
			if sourceSubscriptionTruth(info["tmdb_id"]) {
				input, inputErr := originalCatalogRecognitionCallKwargs(fields, map[string]any{"tmdbid": info["tmdb_id"], "episode_group": info["episode_group"]})
				if inputErr != nil {
					return inputErr
				}
				raw, err = a.originalCatalogRecognizeMethod(ctx, "recognize_media", input, func() (json.RawMessage, error) {
					id, _ := strconv.ParseInt(sourceNumberString(info["tmdb_id"]), 10, 64)
					result, err := a.sourceRecognizeTMDBCore(ctx, catalog.Media{ID: id, MediaType: "tv"})
					return result.MediaInfo, err
				})
			} else {
				// A provider ID from the original import is not an explicit
				// episode recognition argument. The parsed path supplies the
				// title and season to the configured recognition module.
				raw, err = a.originalHostMediaRecognize(ctx, plugins.MediaRecognizeRequest{Meta: fields, EpisodeGroup: sourceText(info["episode_group"])})
			}
			if err != nil || !originalModuleResultTruthy(raw) {
				return err
			}
			value, err := decodeSourceJSON(raw)
			if err != nil || sourceMap(value) == nil {
				return originalCatalogMediaInvalid()
			}
			info, season, episode = sourceMap(value), fields["begin_season"], fields["begin_episode"]
		}
		parent, err := a.originalMetadataParent(ctx, item, sourceMap(kwargs["parent"]))
		if err != nil {
			return err
		}
		if parent != nil {
			target := filepath.Join(sourceText(parent["path"]), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".nfo")
			if err = a.scrapeOriginalNFO(ctx, path, target, meta, info, season, episode, overwrite); err != nil {
				return err
			}
		}
		if info["type"] != "电影" || initFolder {
			a.scrapeOriginalMetadataImages(ctx, item, sourceMap(kwargs["parent"]), info, season, episode, overwrite)
		}
		return nil
	}
	var entries []map[string]any
	// Movie directories always inspect BDMV/CERTIFICATE; the fixed TV
	// directory handler lists children only for recursive traversal.
	if info["type"] == "电影" || recursive {
		entries, err = a.originalMetadataList(ctx, item)
		if err != nil {
			return err
		}
	}
	bluray := false
	for _, entry := range entries {
		bluray = bluray || entry["type"] == "dir" && (entry["name"] == "BDMV" || entry["name"] == "CERTIFICATE")
	}
	if recursive && (info["type"] != "电影" || !bluray) {
		for _, entry := range entries {
			isDir := entry["type"] == "dir"
			name := sourceText(entry["name"])
			if info["type"] == "电影" && isDir {
				continue
			}
			if info["type"] != "电影" && isDir && name != "Specials" && name != "SPs" {
				childMeta, err := parser.ParseTitle(name, nil, nil)
				if err != nil {
					return err
				}
				if childMeta.Get("begin_season") == nil {
					continue
				}
			}
			childArgs := map[string]any{"fileitem": entry, "mediainfo": info, "init_folder": isDir, "overwrite": overwrite}
			if !isDir {
				childArgs["parent"] = item
			}
			if err = a.scrapeOriginalLocalMetadata(ctx, childArgs); err != nil {
				return err
			}
		}
	}
	if !initFolder {
		return nil
	}
	var season any
	nfo := ""
	if info["type"] == "电影" {
		if bluray {
			nfo = filepath.Join(path, filepath.Base(path)+".nfo")
		}
	} else {
		seasonMeta, err := parser.ParseTitle(filepath.Base(path), nil, nil)
		if err != nil {
			return err
		}
		if filepath.Base(path) == "Specials" || filepath.Base(path) == "SPs" {
			seasonMeta.Set("begin_season", json.Number("0"))
		} else if seasonMeta.Name() != "" && seasonMeta.Get("begin_season") != nil {
			withoutCustom := []string{"#"}
			plain, err := parser.ParseTitle(filepath.Base(path), nil, &withoutCustom)
			if err != nil {
				return err
			}
			if plain.Get("begin_season") == nil {
				seasonMeta.Set("begin_season", nil)
			}
		}
		if seasonMeta.Get("begin_season") != nil {
			season, nfo = json.Number(seasonMeta.Number("begin_season").String()), filepath.Join(path, "season.nfo")
		} else if seasonMeta.Name() != "" {
			nfo = filepath.Join(path, "tvshow.nfo")
		} else {
			return nil
		}
	}
	if nfo != "" {
		if err = a.scrapeOriginalNFO(ctx, path, nfo, meta, info, season, nil, overwrite); err != nil {
			return err
		}
	}
	a.scrapeOriginalMetadataImages(ctx, item, sourceMap(kwargs["parent"]), info, season, nil, overwrite)
	return nil
}

func (a *App) originalMetadataRecognizeNative(ctx context.Context, meta *releasemeta.MetaObject) (json.RawMessage, error) {
	kind := "movie"
	if meta.Get("type") == "电视剧" {
		kind = "tv"
	}
	if id := meta.Number("tmdbid"); id != nil && id.IsInt64() && id.Sign() > 0 {
		result, err := a.sourceRecognizeTMDBCore(ctx, catalog.Media{ID: id.Int64(), MediaType: kind})
		return result.MediaInfo, err
	}
	query := catalog.ExploreMedia{Media: catalog.Media{Title: meta.Name(), MediaType: kind}, Source: "themoviedb"}
	query.Year, _ = strconv.Atoi(sourceStringValue(meta.Get("year")))
	resolved, err := a.catalog().ResolveExploreMedia(ctx, query)
	if err != nil || resolved.Media == nil {
		return nil, err
	}
	result, err := a.sourceRecognizeTMDBCore(ctx, *resolved.Media)
	return result.MediaInfo, err
}

func (a *App) scrapeOriginalNFO(ctx context.Context, mediaPath, target string, meta *releasemeta.MetaObject, info map[string]any, season, episode any, overwrite bool) error {
	exists, err := a.originalMetadataFileItemResult(ctx, "local", target)
	if err != nil {
		return err
	}
	if originalModuleResultTruthy(exists) && !overwrite {
		return nil
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	content, err := a.originalMetadataNFO(ctx, meta, raw, season, episode, func() ([]byte, error) {
		return a.originalMetadataNativeNFO(ctx, raw, season, episode)
	}, nil)
	if err != nil || len(content) == 0 {
		return err
	}
	return a.writeOriginalMetadataSidecar(ctx, mediaPath, target, content, overwrite)
}

func (a *App) originalMetadataNativeNFO(ctx context.Context, raw json.RawMessage, season, episode any) ([]byte, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return nil, err
	}
	fields := sourceMap(value)
	scraper := a.originalMetadataScrapeSource(fields)
	switch scraper {
	case "bangumi", "anilist", "douban":
		return originalProviderMetadataNFO(fields, scraper, season, episode)
	case "themoviedb":
	default:
		return nil, nil
	}
	id, _ := strconv.ParseInt(sourceNumberString(fields["tmdb_id"]), 10, 64)
	if id <= 0 {
		return originalSparseTMDBMetadataNFO(fields, season, episode, a.Settings().TMDBImageDomain)
	}
	kind := "movie"
	if fields["type"] != "电影" {
		kind = "tv"
	}
	full, err := a.originalRecognitionProjection(catalog.Media{ID: id, MediaType: kind}, raw)
	if err != nil {
		return nil, err
	}
	if kind == "movie" || season == nil {
		return assetNFO(full.Media, nil)
	}
	seasonNumber, err := strconv.Atoi(sourceStringValue(season))
	if err != nil {
		return nil, err
	}
	// The pinned system scraper uses its TMDB helper directly. tmdb_episodes
	// is a separate chain extension whose DTO deliberately omits episode IDs.
	details, err := a.nativeSeason(ctx, id, seasonNumber)
	if err != nil {
		return nil, err
	}
	if sourceSubscriptionTruth(episode) {
		number, err := strconv.Atoi(sourceStringValue(episode))
		if err != nil {
			return nil, err
		}
		for _, item := range details.Episodes {
			if item.EpisodeNumber == number {
				return assetNFO(full.Media, &item)
			}
		}
		return nil, originalRecognitionMissing()
	}
	doc := assetNFODocument{XMLName: xml.Name{Local: "season"}, Title: details.Name, Plot: details.Overview, Season: &seasonNumber, Premiered: details.AirDate}
	body, err := xml.MarshalIndent(doc, "", "  ")
	return append(append([]byte(xml.Header), body...), '\n'), err
}

func (a *App) originalMetadataScrapeSource(info map[string]any) string {
	if override := info["scrape_source"]; sourceSubscriptionTruth(override) {
		return sourceText(override)
	}
	return a.originalHostMediaRecognition.ScrapeSource
}

// MediaChain.metadata_img is a system-only override. Calling the plugin module
// of the same name here would add an extension point absent from the source.
func (a *App) scrapeOriginalMetadataImages(ctx context.Context, item, parent map[string]any, info map[string]any, season, episode any, overwrite bool) {
	path := strings.TrimSuffix(sourceText(item["path"]), "/")
	scraper := a.originalMetadataScrapeSource(info)
	images := map[string]string{}
	order := []string{}
	if scraper != "themoviedb" {
		if (scraper != "bangumi" && scraper != "anilist" && scraper != "douban") || season != nil ||
			episode != nil && (scraper != "douban" || sourceSubscriptionTruth(episode)) {
			return
		}
		for _, field := range []string{"poster", "backdrop"} {
			address := sourceText(info[field+"_path"])
			if address == "" {
				continue
			}
			ext := filepath.Ext(address)
			if scraper != "douban" {
				ext = originalProviderMetadataImageExtension(address)
			}
			name := field + ext
			images[name], order = address, append(order, name)
		}
	} else if season == nil {
		if sourceSubscriptionTruth(info["tmdb_id"]) && !(sourceSubscriptionTruth(info["poster_path"]) && sourceSubscriptionTruth(info["backdrop_path"])) {
			id, _ := strconv.ParseInt(sourceNumberString(info["tmdb_id"]), 10, 64)
			kind := "movie"
			if info["type"] != "电影" {
				kind = "tv"
			}
			if raw, err := a.catalog().SourceImages(ctx, kind, id, "zh", sourceText(info["original_language"])); err == nil {
				value, err := decodeSourceJSON(raw)
				if err == nil && sourceMap(value) != nil {
					limited, _ := json.Marshal(map[string]any{"posters": sourceMap(value)["posters"], "backdrops": sourceMap(value)["backdrops"]})
					before, _ := json.Marshal(info)
					if after, err := originalApplyTMDBImagesDomain(before, limited, a.Settings().TMDBImageDomain); err == nil {
						value, err := decodeSourceJSON(after)
						if err == nil {
							for _, key := range []string{"poster_path", "backdrop_path"} {
								info[key] = sourceMap(value)[key]
							}
						}
					}
				}
			}
		}
		keys := []string{"backdrop_path", "poster_path", "logo_path"}
		extra := []string{}
		for key := range info {
			if key != "backdrop_path" && key != "poster_path" && key != "logo_path" {
				extra = append(extra, key)
			}
		}
		sort.Strings(extra)
		keys = append(keys, extra...)
		for _, name := range keys {
			value := info[name]
			text, ok := value.(string)
			if ok && strings.HasSuffix(name, "_path") && strings.HasPrefix(text, "http") {
				filename := strings.TrimSuffix(name, "_path") + filepath.Ext(text)
				images[filename] = text
				order = append(order, filename)
			}
		}
	} else {
		id, _ := strconv.ParseInt(sourceNumberString(info["tmdb_id"]), 10, 64)
		seasonNumber, _ := strconv.Atoi(sourceStringValue(season))
		if id <= 0 {
			return
		}
		details, err := a.nativeSeason(ctx, id, seasonNumber)
		if err == nil {
			imagePath, name := details.PosterPath, fmt.Sprintf("season%02d-poster", seasonNumber)
			if seasonNumber == 0 {
				name = "season-specials-poster"
			}
			if sourceSubscriptionTruth(episode) {
				number, _ := strconv.Atoi(sourceStringValue(episode))
				imagePath, name = "", "episode-thumb"
				for _, item := range details.Episodes {
					if item.EpisodeNumber == number {
						imagePath = item.StillPath
					}
				}
			}
			if imagePath != "" {
				value, _ := sourceImagePath(imagePath, a.Settings().TMDBImageDomain)
				filename := name + filepath.Ext(imagePath)
				images[filename] = sourceText(value)
				order = append(order, filename)
			}
		}
	}
	if len(images) == 0 {
		return
	}
	base := path
	if item["type"] == "file" {
		parent, err := a.originalMetadataParent(ctx, item, parent)
		if err != nil || parent == nil {
			return
		}
		base = sourceText(parent["path"])
	}
	for _, name := range order {
		imageURL := images[name]
		metadata := ""
		for _, key := range []string{"poster", "backdrop", "fanart", "background", "logo", "disc", "cdart", "banner", "thumb", "landscape", "clearart"} {
			if strings.Contains(strings.ToLower(name), key) {
				metadata = key
				break
			}
		}
		if metadata == "" || sourceSubscriptionTruth(episode) && metadata != "thumb" || season != nil && !sourceSubscriptionTruth(episode) && (metadata == "logo" || metadata == "disc" || metadata == "cdart" || metadata == "clearart") || info["type"] != "电影" && (metadata == "disc" || metadata == "cdart") {
			continue
		}
		targets := []string{}
		switch {
		case sourceSubscriptionTruth(episode):
			targets = append(targets, filepath.Join(base, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+filepath.Ext(name)))
		case season != nil:
			targets = append(targets, filepath.Join(base, "poster"+filepath.Ext(name)))
			parent, err := a.originalMetadataParent(ctx, item, parent)
			if err == nil && parent != nil {
				targets = append([]string{filepath.Join(sourceText(parent["path"]), name)}, targets...)
			}
		default:
			targets = append(targets, filepath.Join(base, name))
			if strings.HasPrefix(name, "backdrop.") {
				targets = append(targets, filepath.Join(base, "fanart"+filepath.Ext(name)))
			} else if strings.HasPrefix(name, "fanart.") {
				targets = append(targets, filepath.Join(base, "backdrop"+filepath.Ext(name)))
			} else if strings.HasPrefix(name, "thumb.") {
				targets = append(targets, filepath.Join(base, "landscape"+filepath.Ext(name)))
			} else if strings.HasPrefix(name, "landscape.") {
				targets = append(targets, filepath.Join(base, "thumb"+filepath.Ext(name)))
			}
		}
		for _, target := range targets {
			exists, err := a.originalMetadataFileItemResult(ctx, "local", target)
			if err != nil {
				a.log("ERROR", "scrape", "metadata_image_failed", a.clean(err.Error()), "", "")
				continue
			}
			if originalModuleResultTruthy(exists) && !overwrite {
				continue
			}
			if err := a.fetchOriginalMetadataImage(ctx, path, imageURL, target, overwrite); err != nil {
				a.log("ERROR", "scrape", "metadata_image_failed", a.clean(err.Error()), "", "")
			}
		}
	}
}

func originalProviderMetadataImageExtension(address string) string {
	if parsed, err := url.Parse(address); err == nil {
		switch ext := strings.ToLower(filepath.Ext(parsed.Path)); ext {
		case ".jpg", ".jpeg", ".png", ".webp":
			return ext
		}
	}
	return ".jpg"
}

func (a *App) fetchOriginalMetadataImage(ctx context.Context, path, imageURL, target string, overwrite bool) error {
	parsed, err := url.Parse(imageURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil {
		return bad("INVALID_IMAGE_URL", "元数据图片地址无效")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return err
	}
	response, err := assetImageClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("metadata image HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > 8<<20 {
		return bad("INVALID_IMAGE", "元数据图片为空或超过8MiB")
	}
	return a.writeOriginalMetadataSidecar(ctx, path, target, data, overwrite)
}
