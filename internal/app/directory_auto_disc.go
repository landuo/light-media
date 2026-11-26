package app

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"light-media/internal/media"
	"light-media/internal/releasemeta"
	"light-media/internal/transferextensions"
)

// Only the automatic monitor opts into disc-root grouping. Its ordinary scanner
// and browser callers keep their existing file selection behavior. A recognized
// disc with an unfinished member or no media stream is skipped as a whole.
func (a *App) automaticDiscCandidate(ctx context.Context, path string, classifier transferextensions.Classifier, unchanged func(string, importSnapshot) bool) (*importScanFile, bool, error) {
	disc, err := a.originalTransferIsBluray(ctx, map[string]any{"storage": "local", "type": "dir", "path": path})
	if err != nil || !disc {
		return nil, false, err
	}
	stamp, entries, err := snapshotOriginalDirectory(path)
	if err != nil {
		return nil, true, err
	}
	temporary, err := a.downloadTemporaryExtensions()
	if err != nil {
		return nil, true, err
	}
	hasStream := false
	for _, entry := range entries {
		if entry.Directory {
			continue
		}
		ext := filepath.Ext(entry.Path)
		for _, suffix := range temporary {
			if strings.EqualFold(ext, suffix) {
				return nil, true, nil
			}
		}
		if !hasStream && strings.HasPrefix(strings.ToLower(filepath.ToSlash(entry.Path)), "bdmv/stream/") {
			hasStream, err = classifier.Media(transferextensions.PathFile(entry.Path))
			if err != nil {
				return nil, true, err
			}
		}
	}
	if !hasStream {
		return nil, true, nil
	}
	item, err := originalLocalFileItem(path)
	if err != nil {
		return nil, true, err
	}
	file := importScanFile{Path: path, Size: stamp.Size, Snapshot: &stamp, originalDirectoryPlanned: true, originalItem: item, originalMetadataPath: path}
	if unchanged != nil && unchanged(path, stamp) {
		file.Unchanged = true
	} else {
		file.Parsed = a.parseMediaName(filepath.Base(path))
		file.InferredMedia, file.Numbering, file.IdentityReason = a.importNFOEvidence(importNFOAnchor(path, true))
		if file.InferredMedia != nil {
			file.IdentitySource = "nfo"
		}
	}
	return &file, true, nil
}

// importNFOEvidence derives a basename NFO then movie.nfo from this anchor;
// the anchor itself is never opened or treated as a real video.
func importNFOAnchor(path string, disc bool) string {
	if !disc {
		return path
	}
	return filepath.Join(path, filepath.Base(path)+".mkv")
}

var ordinalDiscName = regexp.MustCompile(`(?i)^(?:disc|disk|dvd|cd)[ ._-]*[0-9]{1,3}$`)

// A numbered Blu-ray folder can sit immediately below the named TV season
// release. Parse that one component on its own so a generic "Season 1" or a
// library/category ancestor cannot supply the show's title.
func parseTransferDiscMeta(path string, parse func(string) (*releasemeta.MetaObject, error)) (*releasemeta.MetaObject, error) {
	name := filepath.Base(path)
	if ordinalDiscName.MatchString(name) {
		release, err := parse(filepath.Base(filepath.Dir(path)))
		if err != nil {
			return nil, err
		}
		first, last := release.Number("begin_season"), release.Number("end_season")
		if release.Name() != "" && release.Text("type") == "电视剧" && first != nil && (last == nil || first.Cmp(last) == 0) {
			// A box can advertise the entire season's range on its cover.
			// It does not establish which episodes are on an individual disc.
			release.Set("begin_episode", nil)
			release.Set("end_episode", nil)
			release.Set("total_episode", nil)
			release.Set("part", name)
			return release, nil
		}
	}
	return parse(path)
}

func (a *App) originalDiscPathMeta(ctx context.Context, path string) (*releasemeta.MetaObject, error) {
	return parseTransferDiscMeta(path, func(p string) (*releasemeta.MetaObject, error) {
		return a.originalTransferPathMeta(ctx, p)
	})
}

// A disc's release name has no video extension. Use the same path metadata
// that MoviePilot passes to recognition, including its configured title words.
func (a *App) discReleaseIdentity(ctx context.Context, path string, meta *releasemeta.MetaObject) (media.ParseResult, error) {
	if meta == nil {
		var err error
		meta, err = a.originalDiscPathMeta(ctx, path)
		if err != nil {
			return media.ParseResult{}, err
		}
	}
	year, _ := strconv.Atoi(sourceStringValue(meta.Get("year")))
	p := media.ParseResult{Status: media.Parsed, Type: "movie", Title: meta.Name(), Year: year}
	for _, name := range []string{meta.Text("cn_name"), meta.Text("en_name")} {
		if name != "" && name != p.Title {
			p.AlternateTitles = append(p.AlternateTitles, name)
		}
	}
	if meta.Text("type") == "电视剧" {
		p.Type = "episode"
		seasons, episodes, err := larkMetaNumbers(meta)
		if err != nil {
			return media.ParseResult{}, err
		}
		if len(seasons) == 1 && (len(episodes) > 0 || meta.Number("begin_season") != nil) {
			n := &media.Numbering{Season: &seasons[0], Episodes: episodes}
			if len(episodes) > 0 {
				n.Form = "season_episode"
			}
			p.Original = n
		}
	}
	return p, nil
}

func (file importScanFile) isOriginalDisc() bool {
	return file.originalItem != nil && originalImportFileClass(ImportFileRequest{originalItem: file.originalItem}).Bluray
}

func (file importScanFile) validateImportNumbering(n *media.Numbering) error {
	if file.isOriginalDisc() {
		if n == nil || n.Season == nil || *n.Season < 0 {
			return bad("DIRECTORY_NUMBERING_REVIEW", "电视剧蓝光原盘缺少明确季号，请在“手动整理”中选择电视剧并指定季号，集号可留空")
		}
	} else if n == nil || n.Season == nil || *n.Season < 0 || len(n.Episodes) == 0 {
		return bad("DIRECTORY_NUMBERING_REVIEW", "剧集需要明确季号和单集编号，请在“手动整理”中选择电视剧类型并设置季、集，必要时查询或填写TheMovieDb编号")
	}
	return nil
}

// A known torrent anywhere inside a disc prevents treating that disc as an
// unmanaged stable directory. Every frozen leaf must have complete evidence
// from the same task; mixed or missing leaf evidence remains pending.
func automaticDiscDownloadOwner(path string, expected importSnapshot, files map[string]directoryDownloadFile) (directoryDownloadFile, bool, error) {
	var out directoryDownloadFile
	known := false
	for candidate := range files {
		if within(path, candidate) && candidate != path {
			known = true
			break
		}
	}
	if !known {
		return out, false, nil
	}
	actual, entries, err := snapshotOriginalDirectory(path)
	if err != nil {
		return out, true, err
	}
	if actual != expected {
		return out, true, bad("REVIEW_SOURCE_CHANGED", "光盘目录与监听快照不同")
	}
	out.Complete, out.Imported, out.Size = true, true, actual.Size
	first := true
	present := map[string]bool{}
	for _, entry := range entries {
		if entry.Directory {
			continue
		}
		present[filepath.Join(path, entry.Path)] = true
		leaf, ok := files[filepath.Join(path, entry.Path)]
		if !ok {
			out.Complete = false
			out.Imported = false
			continue
		}
		out.Complete = out.Complete && leaf.Complete && leaf.Size == entry.Snapshot.Size
		out.Managed = out.Managed || leaf.Managed
		out.Imported = out.Imported && leaf.Imported
		out.IdentityConflict = out.IdentityConflict || leaf.IdentityConflict
		if first {
			out.OwnerHash = leaf.OwnerHash
			first = false
		} else if leaf.OwnerHash != out.OwnerHash {
			out.Complete = false
			out.IdentityConflict = true
		}
		if leaf.Hint != nil {
			if out.Hint != nil && (out.Hint.ID != leaf.Hint.ID || out.Hint.Type != leaf.Hint.Type) {
				out.IdentityConflict = true
				out.Complete = false
			} else {
				out.Hint = leaf.Hint
			}
		}
	}
	for candidate := range files {
		if candidate != path && within(path, candidate) && !present[candidate] {
			out.Complete = false
			out.Imported = false
		}
	}
	return out, true, nil
}

// blurayDiscRoot returns the disc root for a file inside BDMV/STREAM. MoviePilot
// promotes such an event to the disc's parent directory (monitorBlurayDiscRoot):
// a Blu-ray is organized as one unit, never as a pile of STREAM fragments.
func blurayDiscRoot(path string) (string, bool) {
	normalized := filepath.ToSlash(path)
	lower := strings.ToLower(normalized)
	idx := strings.Index(lower, "/bdmv/stream/")
	if idx < 0 {
		return "", false
	}
	root := normalized[:idx]
	if root == "" {
		return "", false
	}
	return filepath.FromSlash(root), true
}
