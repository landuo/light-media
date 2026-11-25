package app

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"light-media/internal/media"
	"light-media/internal/releasemeta"
)

// Only the automatic monitor opts into disc-root grouping. Its ordinary scanner
// and browser callers keep their existing file selection behavior.
func (a *App) automaticDiscCandidate(ctx context.Context, path string, unchanged func(string, importSnapshot) bool) (importScanFile, bool, error) {
	disc, err := a.originalTransferIsBluray(ctx, map[string]any{"storage": "local", "type": "dir", "path": path})
	if err != nil || !disc {
		return importScanFile{}, false, err
	}
	stamp, _, err := snapshotOriginalDirectory(path)
	if err != nil {
		return importScanFile{}, true, err
	}
	item, err := originalLocalFileItem(path)
	if err != nil {
		return importScanFile{}, true, err
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
	return file, true, nil
}

// importNFOEvidence derives a basename NFO then movie.nfo from this anchor;
// the anchor itself is never opened or treated as a real video.
func importNFOAnchor(path string, disc bool) string {
	if !disc {
		return path
	}
	return filepath.Join(path, filepath.Base(path)+".mkv")
}

// A disc's release name has no video extension. Use the same path metadata
// that MoviePilot passes to recognition, including its configured title words.
func (a *App) discReleaseIdentity(ctx context.Context, path string, meta *releasemeta.MetaObject) (media.ParseResult, error) {
	if meta == nil {
		var err error
		meta, err = a.originalTransferPathMeta(ctx, path)
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
		if len(seasons) == 1 && len(episodes) > 0 {
			p.Original = &media.Numbering{Season: &seasons[0], Episodes: episodes, Form: "season_episode"}
		}
	}
	return p, nil
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
