package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"light-media/internal/catalog"
	"light-media/internal/media"
)

type ImportFileRequest struct {
	Path            string `json:"path"`
	Season          int    `json:"season"`
	SeasonExplicit  bool   `json:"season_explicit,omitempty"`
	SourceEpisodeID int64  `json:"source_episode_id,omitempty"`
	Episodes        []int  `json:"episodes"`
	Part            string `json:"part,omitempty"`
}

type ImportRequest struct {
	HistorySource     string              `json:"-"`
	Manual            bool                `json:"-"`
	ReorganizeAssetID string              `json:"-"`
	Renaming          *bool               `json:"renaming,omitempty"`
	Scraping          *bool               `json:"scraping,omitempty"`
	LibraryTypeFolder *bool               `json:"library_type_folder,omitempty"`
	MediaType         string              `json:"media_type"`
	TMDBID            int64               `json:"tmdb_id"`
	RootID            string              `json:"root_id"`
	TargetPath        string              `json:"target_path,omitempty"`
	Mode              string              `json:"mode"`
	IntentID          string              `json:"intent_id"`
	WorkflowID        string              `json:"workflow_id"`
	Files             []ImportFileRequest `json:"files"`
	Mappings          []media.MappingRule `json:"mappings"`
}

type importSidecar struct {
	Source   string         `json:"source"`
	Target   string         `json:"target"`
	Snapshot importSnapshot `json:"snapshot"`
}

type importMetadata struct {
	Reorganize *importReorganize          `json:"reorganize,omitempty"`
	RootPath   string                     `json:"root_path"`
	Methods    map[string]string          `json:"methods"`
	Episodes   map[string]catalog.Episode `json:"episodes"`
	Sidecars   map[string][]importSidecar `json:"sidecars"`
}

type importScanFile struct {
	Path           string               `json:"path"`
	Size           int64                `json:"size"`
	Parsed         media.ParseResult    `json:"parsed"`
	InferredMedia  *importInferredMedia `json:"inferred_media,omitempty"`
	Numbering      *media.Numbering     `json:"numbering,omitempty"`
	IdentitySource string               `json:"identity_source,omitempty"`
	IdentityReason string               `json:"identity_reason,omitempty"`
}

type importInferredMedia struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	EpisodeID int64  `json:"episode_id,omitempty"`
}

func parseManualImportFile(file ImportFileRequest) media.ParseResult {
	name := filepath.Base(file.Path)
	parsed := media.Parse(name)
	if file.SeasonExplicit && file.Season == 0 && parsed.Original == nil {
		// Keep global S00 recognition conservative. Once a user explicitly
		// selects specials, reuse only the filename's title and episode syntax;
		// the real season remains 0 and is checked against TMDB before import.
		if index := strings.Index(strings.ToLower(name), "s00e"); index >= 0 {
			parsed = media.Parse(name[:index] + "S01E" + name[index+4:])
		}
	}
	if parsed.Original != nil && parsed.Original.Season == nil && file.SeasonExplicit {
		// An explicit season supplies the missing context of an EP-only name.
		// Specials use a syntactic stand-in here; the actual requested season
		// remains 0 and previewImport verifies the real TMDB specials metadata.
		season := file.Season
		if season == 0 {
			season = 1
		}
		numbering := *parsed.Original
		numbering.Season = &season
		parsed.Original = &numbering
	}
	return parsed
}

func (a *App) scanFiles(ctx context.Context, directory string, recursive bool) (any, error) {
	if !a.sourceAllowed(directory) {
		return nil, bad("INVALID_SOURCE", "请扫描允许目录内的规范绝对路径")
	}
	root, err := importDir(directory, false)
	if err != nil {
		return nil, err
	}
	root.Close()
	files, visited, truncated := []importScanFile{}, 0, false
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if depth > 16 {
			truncated = true
			return nil
		}
		opened, err := importDir(dir, false)
		if err != nil {
			return err
		}
		defer opened.Close()
		for {
			entries, readErr := opened.ReadDir(100)
			for _, entry := range entries {
				visited++
				if visited > 20000 || len(files) >= 2000 {
					truncated = true
					return nil
				}
				if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				name := filepath.Join(dir, entry.Name())
				if entry.IsDir() {
					if recursive {
						if err := walk(name, depth+1); err != nil {
							return err
						}
					}
				} else if isVideo(name) {
					locked, err := snapshotPath(name)
					if err != nil {
						return err
					}
					file := importScanFile{Path: name, Size: locked.Size, Parsed: media.Parse(entry.Name())}
					file.InferredMedia, file.Numbering, file.IdentityReason = a.importNFOEvidence(name)
					if file.InferredMedia != nil {
						file.IdentitySource = "nfo"
					}
					files = append(files, file)
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	if err := walk(directory, 0); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return map[string]any{"files": files, "truncated": truncated}, nil
}

func (a *App) previewImport(ctx context.Context, request ImportRequest) (ImportPlan, error) {
	var prepareErr error
	request, prepareErr = a.prepareManualImport(ctx, request)
	if prepareErr != nil {
		return ImportPlan{}, prepareErr
	}
	if !validMedia(request.MediaType, request.TMDBID) || len(request.Files) == 0 || len(request.Files) > 2000 || (request.Mode != "hardlink" && request.Mode != "copy" && request.Mode != "auto") || len(request.Mappings) > 100 {
		return ImportPlan{}, bad("INVALID_IMPORT", "请选择明确影视身份、1–2000 个源文件及有效整理方式")
	}
	if request.TargetPath != "" {
		rootID, err := a.ensureImportTarget(ctx, request.TargetPath, request.MediaType, request.Files, request.Manual)
		if err != nil {
			return ImportPlan{}, err
		}
		request.RootID = rootID
	}
	root, err := a.importRoot(request.RootID)
	if err != nil {
		return ImportPlan{}, err
	}
	if root.Kind != "mixed" && root.Kind != request.MediaType {
		return ImportPlan{}, bad("INVALID_ROOT_KIND", "媒体类型与目标库类型不符")
	}
	rootDir, err := importDir(root.Path, false)
	if err != nil {
		return ImportPlan{}, err
	}
	rootInfo, err := rootDir.Stat()
	availableSpace, spaceErr := importAvailableSpace(rootDir)
	if spaceErr == nil {
		if ctx.Err() != nil {
			spaceErr = ctx.Err()
		} else {
			var finish func()
			finish, spaceErr = a.beginWrite()
			if spaceErr == nil {
				spaceErr = importProbeWrite(rootDir)
				finish()
			}
		}
	}
	rootDir.Close()
	if spaceErr != nil {
		return ImportPlan{}, spaceErr
	}
	if err != nil {
		return ImportPlan{}, err
	}
	rootStat, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return ImportPlan{}, bad("INVALID_ROOT", "无法确定目标文件系统")
	}
	identity, err := a.media(ctx, request.MediaType, request.TMDBID)
	if err != nil {
		return ImportPlan{}, err
	}
	if identity.ID != request.TMDBID || identity.MediaType != request.MediaType || importName(identity.Title) == "" {
		return ImportPlan{}, bad("INVALID_METADATA", "未取得可靠影视身份")
	}
	category := ""
	if importBool(request.LibraryTypeFolder, false) {
		identity, category, err = a.importMediaCategory(ctx, identity)
		if err != nil {
			return ImportPlan{}, err
		}
	}
	reorganize, err := a.prepareImportReorganize(ctx, request)
	if err != nil {
		return ImportPlan{}, err
	}
	plan := ImportPlan{ID: id("plan_"), WorkflowID: request.WorkflowID, Media: identity, RootID: root.ID, Mode: request.Mode, Renaming: request.Renaming, Scraping: request.Scraping, LibraryTypeFolder: request.LibraryTypeFolder, Files: []PlanFile{}, State: "preview", IntentID: request.IntentID, CreatedAt: now()}
	plan.Manual, plan.ReorganizeAssetID = request.Manual, request.ReorganizeAssetID
	if plan.WorkflowID == "" {
		plan.WorkflowID = id("wf_")
	}
	if err = a.importIntentActive(ctx, plan); err != nil {
		return ImportPlan{}, err
	}
	plan.SpaceAvailable = availableSpace
	plan.Warnings = []string{"空间是视频、字幕及有上限的刮削图片的保守预估；实际写入前仍会复核可用空间。"}
	meta := importMetadata{RootPath: root.Path, Methods: map[string]string{}, Episodes: map[string]catalog.Episode{}, Sidecars: map[string][]importSidecar{}}
	meta.Reorganize = reorganize
	seasons := map[int]catalog.Season{}
	seenSources, seenTargets, seenSlots := map[string]bool{}, map[string]bool{}, map[string]bool{}
	seenSourceInodes := map[string]bool{}
	for _, requested := range request.Files {
		file := PlanFile{ID: id("file_"), Source: requested.Path, Season: requested.Season, Episodes: append([]int(nil), requested.Episodes...), Part: strings.TrimSpace(requested.Part), Status: "ready"}
		if len(request.Files) == 1 {
			file.HistorySource = request.HistorySource
		}
		block := func(reason string) { file.Status = "blocked"; file.Reason = reason }
		if len([]rune(file.Part)) > 64 || importName(file.Part) != file.Part {
			block("Part 必须是有效的文件名片段，不能包含路径或特殊控制字符")
		}
		if !a.importSourceAllowed(file.Source, request.Manual) || !isVideo(file.Source) || seenSources[file.Source] {
			block("源路径未获允许、不是视频或重复选择")
		} else if !request.Manual {
			for _, configured := range a.Settings().Roots {
				if within(configured.Path, file.Source) {
					block("源文件已在配置的媒体库中，请使用资产核对功能")
				}
			}
		}
		seenSources[file.Source] = true
		if a.importSourceAllowed(file.Source, request.Manual) {
			if inferred, _, _ := a.importNFOEvidence(file.Source); inferred != nil && (inferred.Type != identity.MediaType || inferred.ID != identity.ID) {
				plan.Warnings = append(plan.Warnings, "源 NFO 身份与本次明确选择不同；本计划按你选择的 TMDB 身份整理，请核对后确认。")
			}
		}
		if file.Status == "ready" {
			locked, e := snapshotPath(file.Source)
			if e != nil {
				block("源文件不可访问、包含符号链接或不是普通文件")
			} else {
				file.Device, file.Inode, file.Size, file.ModNS = locked.Device, locked.Inode, locked.Size, locked.ModNS
				inodeKey := fmt.Sprintf("%d:%d", locked.Device, locked.Inode)
				if seenSourceInodes[inodeKey] {
					block("同一个源 inode 被重复选择，不能冒充不同影片或季集")
				}
				seenSourceInodes[inodeKey] = true
				if locked.Size <= 0 {
					block("空视频文件不能整理")
				}
			}
		}
		if request.MediaType == "movie" {
			file.Season = 0
			file.Episodes = nil
		} else if file.Status == "ready" {
			explicitSeason := requested.SeasonExplicit
			nfoEpisodeID := requested.SourceEpisodeID
			if inferred, numbering, reason := a.importNFOEvidence(file.Source); reason == "" && inferred != nil && inferred.Type == "tv" && inferred.ID == identity.ID && numbering != nil && numbering.Season != nil && len(numbering.Episodes) == 1 && len(file.Episodes) == 0 {
				if !explicitSeason && file.Season == 0 || file.Season == *numbering.Season {
					file.Season = *numbering.Season
					file.Episodes = append([]int(nil), numbering.Episodes...)
					explicitSeason = true
					nfoEpisodeID = inferred.EpisodeID
				}
			}
			parsed := parseManualImportFile(requested)
			if explicitSeason && len(file.Episodes) == 0 && parsed.Original != nil && len(parsed.Original.Episodes) == 1 && len(request.Mappings) == 0 {
				// An explicit season override (including 0) changes the season,
				// never the episode number or the video's actual file contents.
				file.Episodes = append([]int(nil), parsed.Original.Episodes...)
			}
			if len(file.Episodes) == 0 && parsed.Original != nil {
				seasonNumber := file.Season
				if seasonNumber == 0 && parsed.Original.Season != nil {
					seasonNumber = *parsed.Original.Season
				}
				if seasonNumber > 0 && seasonNumber <= 1000 {
					season, exists := seasons[seasonNumber]
					if !exists {
						season, err = a.season(ctx, identity.ID, seasonNumber)
						if err != nil {
							return ImportPlan{}, err
						}
						seasons[seasonNumber] = season
					}
					known := []media.Episode{}
					for _, episode := range season.Episodes {
						known = append(known, media.Episode{Season: seasonNumber, Episode: episode.EpisodeNumber})
					}
					resolved := media.Resolve(mediaKey(identity), *parsed.Original, media.Source{}, request.Mappings, known, nil)
					if resolved.Status == media.Parsed {
						for _, episode := range resolved.Standard {
							if file.Season != 0 && file.Season != episode.Season {
								block("映射跨季，需要人工拆分确认")
								break
							}
							file.Season = episode.Season
							file.Episodes = append(file.Episodes, episode.Episode)
						}
					}
				}
			}
			if file.Season < 0 || file.Season == 0 && !explicitSeason || file.Season > 1000 || len(file.Episodes) != 1 || file.Episodes[0] < 1 {
				block("需要明确一个标准季集；特别篇须明确第0季，多集视频不能猜测拆分")
			}
			if file.Status == "ready" {
				season, exists := seasons[file.Season]
				if !exists {
					season, err = a.season(ctx, identity.ID, file.Season)
					if err != nil {
						return ImportPlan{}, err
					}
					seasons[file.Season] = season
				}
				if file.Season == 0 && (season.ID <= 0 || season.SeriesID != identity.ID || season.SeasonNumber != 0) {
					block("未取得对应剧集的 TMDB 第0季信息，请人工核对")
				}
				found := false
				for _, episode := range season.Episodes {
					if file.Status != "ready" {
						break
					}
					if episode.SeasonNumber == file.Season && episode.EpisodeNumber == file.Episodes[0] && episode.ID > 0 {
						if nfoEpisodeID > 0 && episode.ID != nfoEpisodeID {
							block("NFO 单集来源 ID 与 TMDB 季集不符，需人工核对")
							break
						}
						meta.Episodes[file.ID] = episode
						found = true
						break
					}
				}
				if !found {
					block("TMDB 中没有该季集，需人工核对")
				}
			}
		}
		if file.Status == "ready" {
			if e := a.ensureNotExcluded(ctx, identity, file.Season, file.Episodes); e != nil {
				block(e.Error())
			}
		}
		if file.Status == "ready" {
			title := importName(identity.Title)
			folder := title
			if identity.Year > 0 {
				folder = fmt.Sprintf("%s (%d)", title, identity.Year)
			} else {
				folder = fmt.Sprintf("%s (tmdb-%d)", title, identity.ID)
			}
			base := folder
			baseDir := root.Path
			if category != "" {
				baseDir = filepath.Join(baseDir, category)
			}
			targetDir := filepath.Join(baseDir, folder)
			if request.MediaType == "tv" {
				base = fmt.Sprintf("%s S%02dE%02d", title, file.Season, file.Episodes[0])
				targetDir = filepath.Join(targetDir, fmt.Sprintf("Season %d", file.Season))
			}
			if file.Part != "" {
				base += "-" + file.Part
			}
			file.Target = filepath.Join(targetDir, base+strings.ToLower(filepath.Ext(file.Source)))
			if !importBool(request.Renaming, true) {
				// MoviePilot keeps the original basename directly in the destination
				// when smart naming is off, without title or Season subdirectories.
				file.Target = filepath.Join(baseDir, filepath.Base(file.Source))
			}
			inPlace := reorganize != nil && file.Target == reorganize.Asset.Path
			if !within(root.Path, file.Target) || file.Source == file.Target && !inPlace || seenTargets[file.Target] {
				block("目标路径重复、重叠或逃逸")
			} else {
				seenTargets[file.Target] = true
			}
			method := request.Mode
			if method == "auto" {
				method = "hardlink"
				if file.Device != uint64(rootStat.Dev) {
					method = "copy"
				}
			}
			meta.Methods[file.ID] = method
			file.Method = method
			if method == "hardlink" && file.Device != uint64(rootStat.Dev) {
				block("源与目标跨文件系统，硬链接不可用；请选择复制或自动")
			}
			if _, e := os.Lstat(file.Target); e == nil {
				if inPlace {
					actual, inspectErr := snapshotPath(file.Target)
					if inspectErr != nil || actual != reorganize.Video.Snapshot {
						block("原整理目标已被外部修改，不能原位重新整理")
					}
				} else {
					var existing Asset
					e = a.assetAtPath(ctx, file.Target, &existing)
					var ownership managedFile
					ownershipErr := a.Store.Get(ctx, "managed_file", tokenHash(file.Target), &ownership)
					actual, targetErr := snapshotPath(file.Target)
					if e == nil && mediaKey(existing.Media) == mediaKey(identity) && existing.RootID == root.ID && existing.SourcePath == file.Source && (existing.State == "present" || existing.State == "external") && ownershipErr == nil && ownership.RootID == root.ID && ownership.Kind == "video" && ownership.AssetID == existing.ID && ownership.SourceSnapshot == planSnapshot(file) && targetErr == nil && ownership.Snapshot == actual {
						file.Status = "already_imported"
						file.AssetID = existing.ID
					} else {
						block("目标已存在，禁止覆盖")
					}
				}
			} else if !os.IsNotExist(e) {
				block("无法检查目标冲突")
			}
			if file.Status == "ready" {
				for _, episode := range importEpisodes(file.Episodes) {
					slot := fmt.Sprintf("%d:%d", file.Season, episode)
					if seenSlots[slot] {
						block("同一季集被多次选择")
						break
					}
					seenSlots[slot] = true
					var count int
					if e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=? AND asset_id!=?`, mediaKey(identity), file.Season, episode, request.ReorganizeAssetID).Scan(&count); e != nil {
						return ImportPlan{}, e
					}
					if count > 0 {
						block("该影片或季集已存在资产，需先核对冲突")
						break
					}
					var owner string
					e := a.Store.DB.QueryRowContext(ctx, `SELECT intent_id FROM reservations WHERE media_key=? AND season=? AND episode=?`, mediaKey(identity), file.Season, episode).Scan(&owner)
					if e == nil && owner != request.IntentID {
						block("该影片或季集已有其他下载占用")
						break
					}
					if e != nil && !errors.Is(e, sql.ErrNoRows) {
						return ImportPlan{}, e
					}
				}
			}
			if file.Status == "ready" && !inPlace {
				sidecars, e := importSidecars(file)
				if e != nil {
					block("字幕旁车不可访问或发生冲突")
				} else {
					meta.Sidecars[file.ID] = sidecars
				}
			}
		}
		plan.Files = append(plan.Files, file)
	}
	plan.SpaceNeeded = importSpaceEstimate(plan, meta)
	if plan.SpaceNeeded > plan.SpaceAvailable {
		plan.Warnings = append(plan.Warnings, "当前可用空间不足，不能执行此计划")
		for index := range plan.Files {
			if plan.Files[index].Status == "ready" {
				plan.Files[index].Status = "blocked"
				plan.Files[index].Reason = "媒体库可用空间不足，请释放空间后重新预览"
			}
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return ImportPlan{}, err
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err == nil {
		err = txPut(ctx, tx, "import_meta", plan.ID, meta)
	}
	if err != nil {
		return ImportPlan{}, err
	}
	return plan, tx.Commit()
}

func importEpisodes(episodes []int) []int {
	if len(episodes) == 0 {
		return []int{0}
	}
	return episodes
}

func importSidecars(file PlanFile) ([]importSidecar, error) {
	dir, err := importDir(filepath.Dir(file.Source), false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(2001)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 2000 {
		return nil, bad("REVIEW_SIDECARS", "源目录过大，请缩小范围")
	}
	result := []importSidecar{}
	stem := strings.TrimSuffix(filepath.Base(file.Source), filepath.Ext(file.Source))
	targetStem := strings.TrimSuffix(file.Target, filepath.Ext(file.Target))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx":
		default:
			continue
		}
		sideStem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if sideStem != stem && !strings.HasPrefix(sideStem, stem+".") {
			continue
		}
		name := filepath.Join(filepath.Dir(file.Source), entry.Name())
		locked, e := snapshotPath(name)
		if e != nil || locked.Size > 64<<20 {
			return nil, bad("REVIEW_SIDECARS", "字幕旁车无效")
		}
		target := targetStem + strings.TrimPrefix(entry.Name(), stem)
		if _, e = os.Lstat(target); e == nil {
			return nil, bad("IMPORT_CONFLICT", "字幕目标已存在")
		}
		result = append(result, importSidecar{Source: name, Target: target, Snapshot: locked})
		if len(result) > 32 {
			return nil, bad("REVIEW_SIDECARS", "字幕旁车超过上限")
		}
	}
	return result, nil
}

func (a *App) assetAtPath(ctx context.Context, path string, dst *Asset) error {
	var raw []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='asset' AND json_extract(payload,'$.path')=? AND json_extract(payload,'$.state') NOT IN ('removed','superseded') LIMIT 1`, path).Scan(&raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
