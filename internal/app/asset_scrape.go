package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/fnos"
	"light-media/internal/media"
)

type assetNFODocument struct {
	XMLName       xml.Name
	Title         string           `xml:"title"`
	OriginalTitle string           `xml:"originaltitle,omitempty"`
	Year          int              `xml:"year,omitempty"`
	Plot          string           `xml:"plot,omitempty"`
	Season        *int             `xml:"season,omitempty"`
	Episode       *int             `xml:"episode,omitempty"`
	ShowTitle     string           `xml:"showtitle,omitempty"`
	Aired         string           `xml:"aired,omitempty"`
	Premiered     string           `xml:"premiered,omitempty"`
	TMDBID        string           `xml:"tmdbid"`
	UniqueIDs     []media.UniqueID `xml:"uniqueid"`
	Actors        []media.Actor    `xml:"actor,omitempty"`
	Rating        float64          `xml:"rating,omitempty"`
	Votes         int              `xml:"votes,omitempty"`
	Runtime       int              `xml:"runtime,omitempty"`
	Status        string           `xml:"status,omitempty"`
	Genres        []string         `xml:"genre,omitempty"`
}

func assetNFO(identity catalog.Media, episode *catalog.Episode) ([]byte, error) {
	metadata := media.NFOMetadata{Kind: "movie", Title: identity.Title, OriginalTitle: identity.OriginalTitle, Year: identity.Year, Plot: identity.Overview, UniqueIDs: []media.UniqueID{{Type: "tmdb", Default: true, Value: strconv.FormatInt(identity.ID, 10)}}}
	if identity.MediaType == "tv" {
		metadata.Kind = "tvshow"
	}
	cast := identity.Cast
	if episode != nil {
		metadata.Kind = "episode"
		metadata.Title = episode.Name
		metadata.OriginalTitle = ""
		metadata.Plot = episode.Overview
		metadata.Season = episode.SeasonNumber
		metadata.Episode = episode.EpisodeNumber
		metadata.ShowTitle = identity.Title
		metadata.Aired = episode.AirDate
		metadata.Year = 0
		metadata.UniqueIDs[0].Value = strconv.FormatInt(episode.ID, 10)
		if t, err := time.Parse("2006-01-02", episode.AirDate); err == nil {
			metadata.Year = t.Year()
		}
		cast = append(append([]catalog.CastMember(nil), cast...), episode.GuestStars...)
	}
	seen := map[int64]bool{}
	for _, person := range cast {
		if person.ID <= 0 || seen[person.ID] || person.Name == "" {
			continue
		}
		seen[person.ID] = true
		actor := media.Actor{Name: person.Name, Role: person.Character, Order: len(metadata.Actors)}
		if assetImagePattern.MatchString(person.ProfilePath) {
			actor.Thumb = "https://image.tmdb.org/t/p/w185" + person.ProfilePath
		}
		metadata.Actors = append(metadata.Actors, actor)
		if len(metadata.Actors) >= 300 {
			break
		}
	}
	if _, err := media.EncodeNFO(metadata); err != nil {
		return nil, bad("REVIEW_NFO", "元数据不足以生成明确季集的 NFO")
	}
	doc := assetNFODocument{XMLName: xml.Name{Local: map[string]string{"movie": "movie", "tvshow": "tvshow", "episode": "episodedetails"}[metadata.Kind]}, Title: metadata.Title, OriginalTitle: metadata.OriginalTitle, Year: metadata.Year, Plot: metadata.Plot, ShowTitle: metadata.ShowTitle, Aired: metadata.Aired, TMDBID: metadata.UniqueIDs[0].Value, UniqueIDs: metadata.UniqueIDs, Actors: metadata.Actors, Rating: identity.VoteAverage, Votes: identity.VoteCount, Runtime: identity.Runtime, Status: identity.Status, Premiered: identity.ReleaseDate}
	if episode != nil {
		doc.Season, doc.Episode = &metadata.Season, &metadata.Episode
		doc.Rating, doc.Runtime = episode.VoteAverage, episode.Runtime
		doc.Premiered = ""
		doc.Votes = 0
	}
	for _, genre := range identity.Genres {
		if genre.Name != "" {
			doc.Genres = append(doc.Genres, genre.Name)
		}
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append([]byte(xml.Header), body...), '\n'), nil
}

var assetImagePattern = regexp.MustCompile(`^/[A-Za-z0-9_-]+\.(jpg|jpeg|png|webp)$`)
var assetImageClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (a *App) runScrape(ctx context.Context, job Job, assetID string) (returnErr error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var asset Asset
	if err := a.Store.Get(ctx, "asset", assetID, &asset); err != nil {
		return err
	}
	if asset.State == "removed" || asset.State == "missing_confirmed" || asset.State == "superseded" {
		return bad("REVIEW_ASSET", "资产已移除或确认缺失，不能刮削")
	}
	root, err := a.importRoot(asset.RootID)
	if err != nil {
		return err
	}
	if !within(root.Path, asset.Path) {
		return bad("REVIEW_ROOT_CHANGED", "资产路径不在配置库内")
	}
	if _, err = snapshotTransferPath(asset.Path); err != nil {
		return bad("REVIEW_ASSET_MISSING", "视频不可访问，不能标记刮削成功")
	}
	if err = a.jobCancelled(ctx, job); err != nil {
		return err
	}
	release, err := a.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	asset.ScrapeState = "running"
	asset.Error = ""
	if err = a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			asset.ScrapeState = "failed"
			asset.Error = a.clean(returnErr.Error())
			persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = a.Store.Put(persist, "asset", asset.ID, asset)
		}
	}()
	var inherited struct {
		Event  json.RawMessage `json:"original_metadata_event"`
		Assets []string        `json:"original_metadata_assets"`
	}
	if len(job.Payload) != 0 {
		if err = json.Unmarshal(job.Payload, &inherited); err != nil {
			return err
		}
	}
	if len(inherited.Event) != 0 && originalModuleResultTruthy(inherited.Event) {
		if err = a.validateOriginalAssetMetadataEvent(asset, inherited.Event); err != nil {
			return err
		}
		var related []Asset
		seen := map[string]bool{asset.ID: true}
		for _, id := range inherited.Assets {
			if seen[id] {
				continue
			}
			seen[id] = true
			var other Asset
			if err = a.Store.Get(ctx, "asset", id, &other); err != nil {
				return err
			}
			if other.State == "removed" || other.State == "missing_confirmed" || other.State == "superseded" {
				return bad("REVIEW_ASSET", "批次资产已移除，不能刮削")
			}
			if _, err = snapshotTransferPath(other.Path); err != nil {
				return bad("REVIEW_ASSET_MISSING", "批次视频不可访问，不能标记刮削成功")
			}
			if err = a.validateOriginalAssetMetadataEvent(other, inherited.Event); err != nil {
				return err
			}
			related = append(related, other)
		}
		var directories []*originalMetadataDirectory
		for _, candidate := range append([]Asset{asset}, related...) {
			directory, e := a.beginOriginalMetadataDirectory(ctx, candidate)
			if e != nil {
				return e
			}
			if directory != nil {
				directories = append(directories, directory)
			}
		}
		inheritedContext := context.WithValue(ctx, originalMetadataWriteCheckKey{}, a.importCheck(ctx, job))
		inheritedContext = context.WithValue(inheritedContext, originalMetadataDirectoriesKey{}, directories)
		if err = a.consumeOriginalCoreMetadataScrape(inheritedContext, inherited.Event); err != nil {
			return err
		}
		for _, directory := range directories {
			if err = a.finishOriginalMetadataDirectory(ctx, directory); err != nil {
				return err
			}
		}
		if err = a.finishAssetScrape(ctx, job, &asset); err != nil {
			return err
		}
		for i := range related {
			if err = a.finishAssetScrape(ctx, job, &related[i]); err != nil {
				return err
			}
		}
		return nil
	}
	identity, err := a.media(ctx, asset.Media.MediaType, asset.Media.ID)
	if err != nil {
		return err
	}
	asset.Media = identity
	var episode *catalog.Episode
	var season catalog.Season
	if identity.MediaType == "tv" {
		if len(asset.Episodes) != 1 || asset.Season < 0 {
			return bad("REVIEW_NFO", "需要已确认的单个标准季集")
		}
		season, err = a.season(ctx, identity.ID, asset.Season)
		if err != nil {
			return err
		}
		if asset.Season == 0 && (season.ID <= 0 || season.SeriesID != identity.ID || season.SeasonNumber != 0) {
			return bad("REVIEW_NFO", "未取得当前剧集的 TMDB 第0季信息")
		}
		for _, candidate := range season.Episodes {
			if candidate.EpisodeNumber == asset.Episodes[0] && candidate.SeasonNumber == asset.Season {
				value := candidate
				episode = &value
				break
			}
		}
		if episode == nil {
			return bad("REVIEW_NFO", "未找到已确认的单集自身来源 ID")
		}
	}
	check := a.importCheck(ctx, job)
	nfo, err := a.assetOriginalNFO(ctx, asset.Path, identity, episode)
	if err != nil {
		return err
	}
	videoStem := strings.TrimSuffix(asset.Path, filepath.Ext(asset.Path))
	if len(nfo) != 0 {
		if err = a.writeAssetMetadata(ctx, job, asset, videoStem+".nfo", nfo, "nfo", asset.ID, check); err != nil {
			return err
		}
	}
	showDir := filepath.Dir(asset.Path)
	sharedOwner := asset.ID
	if episode != nil {
		if importBool(asset.Renaming, true) {
			showDir = filepath.Dir(showDir)
		}
		sharedOwner = ""
		showNFO, err := a.assetOriginalNFO(ctx, showDir, identity, nil)
		if err != nil {
			return err
		}
		if len(showNFO) != 0 {
			if err = a.writeAssetMetadata(ctx, job, asset, filepath.Join(showDir, "tvshow.nfo"), showNFO, "nfo", "", check); err != nil {
				return err
			}
		}
	}
	images := []struct{ source, target, owner string }{{identity.PosterPath, filepath.Join(showDir, "poster.jpg"), sharedOwner}, {identity.BackdropPath, filepath.Join(showDir, "fanart.jpg"), sharedOwner}}
	if episode != nil {
		if season.PosterPath != "" && importBool(asset.Renaming, true) {
			images = append(images, struct{ source, target, owner string }{season.PosterPath, filepath.Join(filepath.Dir(asset.Path), "poster.jpg"), ""})
		}
		if episode.StillPath != "" {
			images = append(images, struct{ source, target, owner string }{episode.StillPath, videoStem + "-thumb.jpg", asset.ID})
		}
	}
	for _, image := range images {
		if image.source == "" {
			continue
		}
		if err = check(); err != nil {
			return err
		}
		if err = a.fetchAssetImage(ctx, job, asset, image.source, image.target, image.owner, check); err != nil {
			return err
		}
	}
	return a.finishAssetScrape(ctx, job, &asset)
}

func (a *App) finishAssetScrape(ctx context.Context, job Job, asset *Asset) error {
	asset.ScrapeState = "scraped"
	asset.Error = ""
	fnosEnabled := a.Settings().FNOS.Enabled
	if !fnosEnabled {
		asset.FNOSState = "disabled"
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
		return err
	}
	if err = recordDomainEventTx(ctx, tx, assetDomainEvent("scrape.completed", job, *asset)); err != nil {
		return err
	}
	if fnosEnabled {
		if _, err = enqueueTx(ctx, tx, "fnos_sync", map[string]string{"id": asset.ID}, job.WorkflowID); err != nil {
			var api *APIError
			if !errors.As(err, &api) || api.Code != "JOB_EXISTS" {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.event(ctx, job, "INFO", "scrape", "metadata_published", "元数据刮削处理完成")
	return nil
}

func (a *App) writeAssetMetadata(ctx context.Context, job Job, asset Asset, target string, data []byte, kind, owner string, check func() error) error {
	if len(data) > 1<<20 {
		return bad("REVIEW_METADATA_SIZE", "元数据旁车超过大小限制")
	}
	root, err := a.importRoot(asset.RootID)
	if err != nil {
		return err
	}
	if !within(root.Path, target) {
		return bad("INVALID_TARGET", "元数据目标越界")
	}
	if existing, err := importOpen(target); err == nil {
		defer existing.Close()
		actual, err := io.ReadAll(io.LimitReader(existing, 1<<20+1))
		if err != nil {
			return err
		}
		if bytes.Equal(actual, data) {
			return a.reconcileAssetSidecar(ctx, asset, target, kind, owner)
		}
		return bad("REVIEW_METADATA_CONFLICT", "已有 NFO 内容不同，已保留原文件，需人工核对")
	} else if !os.IsNotExist(err) {
		return bad("REVIEW_METADATA_CONFLICT", "元数据目标不可安全访问")
	}
	stageDir := filepath.Join(a.Dir, "staging")
	dir, err := importDir(stageDir, true)
	if err != nil {
		return err
	}
	dir.Close()
	hash := sha256.Sum256(data)
	cache := filepath.Join(stageDir, "nfo-"+hex.EncodeToString(hash[:])+".xml")
	if err = assetCacheBytes(cache, data); err != nil {
		return err
	}
	locked, err := snapshotPath(cache)
	if err != nil {
		return err
	}
	receipt, err := a.publishImport(ctx, "metadata/"+job.ID+"/"+tokenHash(target), cache, target, locked, "copy", check)
	if err != nil {
		return err
	}
	record := managedFile{Path: target, RootID: asset.RootID, AssetID: owner, MediaKey: mediaKey(asset.Media), Kind: kind, Snapshot: receipt.StageSnapshot, SHA256: receipt.SHA256}
	return a.Store.Put(ctx, "managed_file", tokenHash(target), record)
}

func assetCacheBytes(target string, data []byte) error {
	file, err := importOpen(target)
	if err == nil {
		defer file.Close()
		prior, e := io.ReadAll(io.LimitReader(file, int64(len(data))+1))
		if e != nil {
			return e
		}
		if !bytes.Equal(prior, data) {
			return bad("REVIEW_METADATA_CACHE", "元数据临时文件发生变化")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	file, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (a *App) fetchAssetImage(ctx context.Context, job Job, asset Asset, relative, target, owner string, check func() error) error {
	if !assetImagePattern.MatchString(relative) || len(relative) > 128 {
		return bad("REVIEW_IMAGE_PATH", "TMDB 图片路径无效")
	}
	if file, err := importOpen(target); err == nil {
		defer file.Close()
		info, e := file.Stat()
		if e != nil || info.Size() <= 0 || info.Size() > 20<<20 {
			return bad("REVIEW_IMAGE_CONFLICT", "已有图片无效，需人工核对")
		}
		var owned managedFile
		if a.Store.Get(ctx, "managed_file", tokenHash(target), &owned) != nil || owned.MediaKey != mediaKey(asset.Media) || owned.RootID != asset.RootID || owned.Kind != "image" || !owned.Snapshot.matches(info) {
			if err := a.reconcileAssetSidecar(ctx, asset, target, "image", owner); err != nil {
				return err
			}
			if a.Store.Get(ctx, "managed_file", tokenHash(target), &owned) != nil || owned.MediaKey != mediaKey(asset.Media) || owned.Kind != "image" || !owned.Snapshot.matches(info) {
				return bad("REVIEW_IMAGE_CONFLICT", "已有图片不属于本程序或已被修改，未覆盖")
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return bad("REVIEW_IMAGE_CONFLICT", "图片目标不可安全访问")
	}
	stageDir := filepath.Join(a.Dir, "staging")
	dir, err := importDir(stageDir, true)
	if err != nil {
		return err
	}
	dir.Close()
	cache := filepath.Join(stageDir, "image-"+tokenHash(job.ID+target+relative)+".bin")
	if _, err = snapshotPath(cache); os.IsNotExist(err) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		u := url.URL{Scheme: "https", Host: "image.tmdb.org", Path: "/t/p/original" + relative}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return bad("INVALID_IMAGE", "无法建立图片请求")
		}
		request.Header.Set("User-Agent", "light-media/0.1")
		request.Header.Set("Accept", "image/jpeg,image/png,image/webp")
		response, err := a.metadataImageRequest(request)
		if err != nil {
			return bad("IMAGE_UNAVAILABLE", "TMDB 图片暂时不可用")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return bad("IMAGE_UNAVAILABLE", "TMDB 图片未返回成功结果")
		}
		if response.ContentLength > 20<<20 {
			return bad("REVIEW_IMAGE_SIZE", "图片超过 20 MiB 限制")
		}
		output, err := os.OpenFile(cache, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		ok := false
		defer func() {
			output.Close()
			if !ok {
				_ = os.Remove(cache)
			}
		}()
		limited := io.LimitReader(response.Body, 20<<20+1)
		_, err = importCopy(ctx, limited, output, check)
		if err != nil {
			return err
		}
		info, err := output.Stat()
		if err != nil || info.Size() <= 0 || info.Size() > 20<<20 {
			return bad("REVIEW_IMAGE_SIZE", "图片大小无效")
		}
		if err = output.Sync(); err != nil {
			return err
		}
		output.Close()
		inspect, err := importOpen(cache)
		if err != nil {
			return err
		}
		header := make([]byte, 512)
		count, _ := inspect.Read(header)
		inspect.Close()
		mime := http.DetectContentType(header[:count])
		if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
			return bad("REVIEW_IMAGE_FORMAT", "图片服务返回了非图片内容")
		}
		if mime != "image/webp" {
			verify, err := importOpen(cache)
			if err != nil {
				return err
			}
			configuration, _, err := image.DecodeConfig(verify)
			verify.Close()
			if err != nil || configuration.Width < 1 || configuration.Height < 1 || configuration.Width > 16384 || configuration.Height > 16384 {
				return bad("REVIEW_IMAGE_FORMAT", "图片尺寸或编码无效")
			}
		}
		ok = true
	} else if err != nil {
		return err
	}
	locked, err := snapshotPath(cache)
	if err != nil {
		return err
	}
	receipt, err := a.publishImport(ctx, "image/"+job.ID+"/"+tokenHash(target), cache, target, locked, "copy", check)
	if err != nil {
		return err
	}
	record := managedFile{Path: target, RootID: asset.RootID, AssetID: owner, MediaKey: mediaKey(asset.Media), Kind: "image", Snapshot: receipt.StageSnapshot, SHA256: receipt.SHA256}
	if err = a.Store.Put(ctx, "managed_file", tokenHash(target), record); err != nil {
		return err
	}
	_ = os.Remove(cache)
	return nil
}

// Reconcile a crash after sidecar publication but before its ownership record.
// Identical external NFOs may remain usable without claiming ownership of them.
func (a *App) reconcileAssetSidecar(ctx context.Context, asset Asset, target, kind, owner string) error {
	var prior managedFile
	if err := a.Store.Get(ctx, "managed_file", tokenHash(target), &prior); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	actual, err := snapshotPath(target)
	if err != nil {
		return err
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='import_receipt' AND json_extract(payload,'$.target')=? AND json_extract(payload,'$.state') IN ('staged','published') LIMIT 20`, target)
	if err != nil {
		return err
	}
	var found *importReceipt
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var receipt importReceipt
		if json.Unmarshal(raw, &receipt) == nil && receipt.Target == target && receipt.StageSnapshot == actual && within(filepath.Join(a.Dir, "staging"), receipt.Source) {
			copy := receipt
			found = &copy
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if found == nil {
		return nil
	}
	return a.Store.Put(ctx, "managed_file", tokenHash(target), managedFile{Path: target, RootID: asset.RootID, AssetID: owner, MediaKey: mediaKey(asset.Media), Kind: kind, Snapshot: actual, SHA256: found.SHA256})
}

func (a *App) runFNOSSync(ctx context.Context, job Job, assetID string) (returnErr error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var asset Asset
	if err := a.Store.Get(ctx, "asset", assetID, &asset); err != nil {
		return err
	}
	configuration := a.Settings().FNOS
	if !configuration.Enabled {
		asset.FNOSState = "disabled"
		return a.Store.Put(ctx, "asset", asset.ID, asset)
	}
	if asset.State != "present" && asset.State != "external" {
		return bad("REVIEW_ASSET", "资产不可访问，不能同步飞牛")
	}
	if asset.ScrapeState != "scraped" && asset.ScrapeState != "external" {
		return bad("REVIEW_NFO", "请先完成 NFO 刮削")
	}
	root, err := a.importRoot(asset.RootID)
	if err != nil {
		return err
	}
	if !within(root.Path, asset.Path) {
		return bad("REVIEW_ROOT_CHANGED", "资产已不在目标媒体库内")
	}
	if _, err = snapshotPath(asset.Path); err != nil {
		return bad("REVIEW_ASSET_MISSING", "视频不可访问")
	}
	actual := asset.Path
	if len(configuration.Mappings) > 0 {
		if mapped, e := mapPath(asset.Path, configuration.Mappings); e == nil {
			actual = mapped
		}
	}
	service, err := fnos.NewService(fnos.Config{Host: configuration.Host, Username: configuration.Username, Password: configuration.Password})
	if err != nil {
		return err
	}
	libraries, err := service.Libraries(ctx)
	if err != nil {
		return err
	}
	libraryGUID, actual, err := matchFNOSDirectory(asset.Path, actual, libraries)
	if err != nil {
		return err
	}
	if err = a.jobCancelled(ctx, job); err != nil {
		return err
	}
	release, err := a.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	asset.FNOSState = "scan_requested"
	asset.FNOSGUID = ""
	if err = a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			asset.FNOSState = "needs_attention"
			asset.Error = a.clean(returnErr.Error())
			persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = a.Store.Put(persist, "asset", asset.ID, asset)
		}
	}()
	scanDirectory := filepath.Dir(actual)
	if asset.Media.MediaType == "tv" && importBool(asset.Renaming, true) {
		scanDirectory = filepath.Dir(scanDirectory)
	}
	scope := libraryGUID + ":" + filepath.ToSlash(scanDirectory)
	lastScan, _ := time.Parse(time.RFC3339Nano, asset.FNOSScanAt)
	if asset.FNOSScanScope != scope || lastScan.IsZero() || time.Since(lastScan) >= 2*time.Minute {
		if err = service.Scan(ctx, libraryGUID, filepath.ToSlash(scanDirectory)); err != nil {
			return err
		}
		asset.FNOSScanAt, asset.FNOSScanScope = now(), scope
		if err = a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
			return err
		}
	}
	if err = a.jobCancelled(ctx, job); err != nil {
		return err
	}
	kind := "Movie"
	if asset.Media.MediaType == "tv" {
		kind = "Episode"
	}
	guid, err := service.Verify(ctx, libraryGUID, asset.Media.Title, kind, filepath.ToSlash(actual))
	if err != nil {
		return err
	}
	asset.FNOSState = "visible"
	asset.FNOSGUID = guid
	asset.Error = ""
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "asset", asset.ID, asset); err != nil {
		return err
	}
	if err = recordDomainEventTx(ctx, tx, assetDomainEvent("library.visible", job, asset)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.event(ctx, job, "INFO", "fnos", "visible", "已根据实际父链、季集和服务器文件路径确认飞牛可见")
	return nil
}
