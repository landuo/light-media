package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/media"
)

type assetLocalNFO struct {
	XMLName       xml.Name
	Title         string           `xml:"title"`
	OriginalTitle string           `xml:"originaltitle"`
	Year          int              `xml:"year"`
	Plot          string           `xml:"plot"`
	Season        *int             `xml:"season"`
	Episode       *int             `xml:"episode"`
	ShowTitle     string           `xml:"showtitle"`
	Aired         string           `xml:"aired"`
	TMDBID        string           `xml:"tmdbid"`
	UniqueIDs     []media.UniqueID `xml:"uniqueid"`
	Actors        []media.Actor    `xml:"actor"`
}

func readAssetNFO(name string) (assetLocalNFO, int64, error) {
	var result assetLocalNFO
	file, err := importOpen(name)
	if err != nil {
		return result, 0, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return result, 0, bad("REVIEW_NFO", "NFO 超过限制或读取失败")
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	if err = decoder.Decode(&result); err != nil {
		return result, 0, bad("REVIEW_NFO", "NFO 格式无效")
	}
	structural := xml.NewDecoder(bytes.NewReader(raw))
	depth, counts := 0, map[string]int{}
	for {
		token, tokenErr := structural.Token()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			return result, 0, bad("REVIEW_NFO", "NFO 格式无效")
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 && (value.Name.Local == "tmdbid" || value.Name.Local == "title" || value.Name.Local == "season" || value.Name.Local == "episode") {
				counts[value.Name.Local]++
				if counts[value.Name.Local] > 1 {
					return result, 0, bad("REVIEW_NFO", "NFO 身份字段重复，不能选择性推断")
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, 0, bad("REVIEW_NFO", "NFO 尾部无效")
		}
		if text, ok := token.(xml.CharData); ok && strings.TrimSpace(string(text)) == "" {
			continue
		}
		return result, 0, bad("REVIEW_NFO", "NFO 包含多个文档")
	}
	if result.Title == "" || len(result.Title) > 1024 || len(result.Actors) > 2000 || (result.Year != 0 && (result.Year < 1800 || result.Year > 2199)) {
		return result, 0, bad("REVIEW_NFO", "NFO 缺少可靠标题或字段超限")
	}
	identifier := ""
	for _, unique := range result.UniqueIDs {
		if unique.Type != "tmdb" {
			continue
		}
		if identifier != "" {
			return result, 0, bad("REVIEW_NFO", "NFO TMDB ID 重复")
		}
		identifier = unique.Value
	}
	if result.TMDBID != "" {
		if identifier != "" && identifier != result.TMDBID {
			return result, 0, bad("REVIEW_NFO", "NFO TMDB ID 不一致")
		}
		identifier = result.TMDBID
	}
	number, err := strconv.ParseInt(identifier, 10, 32)
	if err != nil || number <= 0 || strconv.FormatInt(number, 10) != identifier {
		return result, 0, bad("REVIEW_NFO", "NFO 没有标准数字 TMDB ID")
	}
	return result, number, nil
}

func localMedia(nfo assetLocalNFO, id int64, kind string) catalog.Media {
	result := catalog.Media{ID: id, MediaType: kind, Title: nfo.Title, OriginalTitle: nfo.OriginalTitle, Year: nfo.Year, Overview: nfo.Plot, Cast: []catalog.CastMember{}, Genres: []catalog.Genre{}, Recommendations: []catalog.Media{}, Seasons: []catalog.SeasonSummary{}}
	for _, actor := range nfo.Actors {
		result.Cast = append(result.Cast, catalog.CastMember{Name: actor.Name, Character: actor.Role, Order: actor.Order})
	}
	return result
}

func (a *App) scanLibrary(ctx context.Context, job Job, rootID string) (returnErr error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	root, err := a.importRoot(rootID)
	if err != nil {
		return err
	}
	scan := map[string]any{"root_id": root.ID, "started_at": now(), "status": "running", "visited": 0, "videos": 0, "imported": 0, "unrecognized": 0}
	defer func() {
		scan["finished_at"] = now()
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = a.Store.Put(persist, "library_scan", root.ID, scan)
	}()
	dir, err := importDir(root.Path, false)
	if err != nil {
		scan["status"] = "unavailable"
		return bad("ROOT_UNAVAILABLE", "媒体库根目录不可访问，未更新既有资产缺失状态")
	}
	dir.Close()
	if err = a.jobCancelled(ctx, job); err != nil {
		scan["status"] = "incomplete"
		return err
	}
	paths, visited, incomplete := []string{}, 0, false
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		if depth > 16 {
			incomplete = true
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		opened, err := importDir(directory, false)
		if err != nil {
			return err
		}
		defer opened.Close()
		for {
			entries, readErr := opened.ReadDir(100)
			for _, entry := range entries {
				visited++
				if visited > 50000 || len(paths) >= 10000 {
					incomplete = true
					return nil
				}
				if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				name := filepath.Join(directory, entry.Name())
				if entry.IsDir() {
					if err = walk(name, depth+1); err != nil {
						return err
					}
				} else if isVideo(name) {
					paths = append(paths, name)
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			if incomplete {
				return nil
			}
		}
	}
	if err = walk(root.Path, 0); err != nil {
		scan["status"] = "incomplete"
		return bad("REVIEW_LIBRARY_SCAN", "目录读取未完成，未推断任何既有资产缺失")
	}
	scan["visited"], scan["videos"] = visited, len(paths)
	imported, unrecognized := 0, 0
	for _, name := range paths {
		if err = a.jobCancelled(ctx, job); err != nil {
			scan["status"] = "incomplete"
			return err
		}
		var existingID string
		err = a.Store.DB.QueryRowContext(ctx, `SELECT id FROM records WHERE kind='asset' AND json_extract(payload,'$.path')=? LIMIT 1`, name).Scan(&existingID)
		if err == nil {
			if e := a.resolveLibraryCandidate(ctx, root.ID, name, existingID); e != nil {
				scan["status"] = "incomplete"
				return e
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			scan["status"] = "incomplete"
			return err
		}
		nfoName := strings.TrimSuffix(name, filepath.Ext(name)) + ".nfo"
		nfo, sourceID, e := readAssetNFO(nfoName)
		if os.IsNotExist(e) {
			nfo, sourceID, e = readAssetNFO(filepath.Join(filepath.Dir(name), "movie.nfo"))
		}
		if e != nil {
			unrecognized++
			code, reason := "LIBRARY_NFO_INVALID", "NFO 无效或身份不一致，请人工确认"
			if os.IsNotExist(e) {
				code, reason = "LIBRARY_NFO_MISSING", "未找到可用的 NFO，请人工确认影视身份"
			}
			if observeErr := a.observeLibraryCandidate(ctx, root, name, code, reason); observeErr != nil {
				scan["status"] = "incomplete"
				return observeErr
			}
			continue
		}
		locked, e := snapshotPath(name)
		if e != nil {
			scan["status"] = "incomplete"
			return bad("REVIEW_LIBRARY_SCAN", "扫描时视频发生变化，未判定缺失")
		}
		asset := Asset{ID: id("asset_"), RootID: root.ID, Path: name, Size: locked.Size, State: "external", ScrapeState: "external", FNOSState: "unverified", CreatedAt: now()}
		switch nfo.XMLName.Local {
		case "movie":
			if root.Kind == "tv" {
				unrecognized++
				if e = a.observeLibraryCandidate(ctx, root, name, "LIBRARY_IDENTITY_CONFLICT", "NFO 影视类型与媒体库不一致，请人工确认"); e != nil {
					scan["status"] = "incomplete"
					return e
				}
				continue
			}
			asset.Media = localMedia(nfo, sourceID, "movie")
		case "episodedetails":
			if root.Kind == "movie" || nfo.Season == nil || nfo.Episode == nil || *nfo.Season < 1 || *nfo.Episode < 1 {
				unrecognized++
				if e = a.observeLibraryCandidate(ctx, root, name, "LIBRARY_EPISODE_UNCONFIRMED", "NFO 季集缺失或与媒体库类型矛盾，请人工选择标准单集"); e != nil {
					scan["status"] = "incomplete"
					return e
				}
				continue
			}
			var seriesNFO assetLocalNFO
			var seriesID int64
			found := false
			parent := filepath.Dir(name)
			for depth := 0; depth < 4 && within(root.Path, parent); depth++ {
				seriesNFO, seriesID, e = readAssetNFO(filepath.Join(parent, "tvshow.nfo"))
				if e == nil && seriesNFO.XMLName.Local == "tvshow" {
					found = true
					break
				}
				if parent == root.Path {
					break
				}
				parent = filepath.Dir(parent)
			}
			if !found || (nfo.ShowTitle != "" && nfo.ShowTitle != seriesNFO.Title) {
				unrecognized++
				if e = a.observeLibraryCandidate(ctx, root, name, "LIBRARY_SHOW_UNCONFIRMED", "剧集 NFO 缺少可靠剧集身份或标题矛盾，请人工确认"); e != nil {
					scan["status"] = "incomplete"
					return e
				}
				continue
			}
			asset.Media = localMedia(seriesNFO, seriesID, "tv")
			asset.Season = *nfo.Season
			asset.Episodes = []int{*nfo.Episode}
		default:
			unrecognized++
			if e = a.observeLibraryCandidate(ctx, root, name, "LIBRARY_NFO_UNSUPPORTED", "NFO 类型无法确定，请人工确认"); e != nil {
				scan["status"] = "incomplete"
				return e
			}
			continue
		}
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			scan["status"] = "incomplete"
			return e
		}
		if e = txPut(ctx, tx, "asset", asset.ID, asset); e == nil {
			for _, episode := range importEpisodes(asset.Episodes) {
				_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO asset_slots VALUES(?,?,?,?)`, mediaKey(asset.Media), asset.Season, episode, asset.ID)
				if e != nil {
					break
				}
			}
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			scan["status"] = "incomplete"
			return e
		}
		if e = a.resolveLibraryCandidate(ctx, root.ID, name, asset.ID); e != nil {
			scan["status"] = "incomplete"
			return e
		}
		imported++
	}
	scan["imported"], scan["unrecognized"] = imported, unrecognized
	if incomplete {
		scan["status"] = "incomplete"
		return bad("REVIEW_LIBRARY_SCAN", "扫描达到数量或深度上限，请缩小媒体库范围；未判定既有资产缺失")
	}
	if err = a.finalizeLibraryCandidates(ctx, root, paths); err != nil {
		scan["status"] = "incomplete"
		return err
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id FROM records WHERE kind='asset' AND json_extract(payload,'$.root_id')=? AND json_extract(payload,'$.state') NOT IN ('removed','missing_confirmed')`, root.ID)
	if err != nil {
		scan["status"] = "incomplete"
		return err
	}
	assetIDs := []string{}
	for rows.Next() {
		var assetID string
		if err = rows.Scan(&assetID); err != nil {
			rows.Close()
			scan["status"] = "incomplete"
			return err
		}
		assetIDs = append(assetIDs, assetID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		scan["status"] = "incomplete"
		return err
	}
	for _, assetID := range assetIDs {
		if err = a.checkAssetLocked(ctx, assetID); err != nil {
			scan["status"] = "incomplete"
			return err
		}
	}
	scan["status"] = "complete"
	a.event(ctx, job, "INFO", "assets", "library_scanned", fmt.Sprintf("媒体库核对完成：%d 个视频，新增 %d 个外部资产，%d 个未识别文件", len(paths), imported, unrecognized))
	return nil
}
