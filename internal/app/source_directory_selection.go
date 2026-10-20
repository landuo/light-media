package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"

	"golang.org/x/sys/unix"
	"light-media/internal/directorysource"
)

// Native directories are explicitly declared inputs. This projection does not
// claim to be a capture of a running original MoviePilot process/configuration.
type nativeSourceDirectory struct {
	ID, SHA        string
	OverwriteKnown bool
	Rule           directorysource.Rule
}
type nativeSourceDirectories struct {
	Version             int
	Namespace, InputSHA string
	Rules               []nativeSourceDirectory
}
type nativeDirectoryDiskObservation struct {
	Source, Target             string
	SourceExists, TargetExists bool
	SourceDevice, TargetDevice uint64
}
type nativeDirectorySelection struct {
	Version                                     int
	Method, InputSHA, DirectoryID, DirectorySHA string
	Inputs                                      nativeSourceDirectories
	Query                                       directorysource.Query
	Files                                       []string
	MediaType                                   string
	TMDBID                                      int64
	Rule                                        directorysource.Rule
	Disks                                       []nativeDirectoryDiskObservation
}

type nativeDirectorySelectionReference struct {
	Version                                   int
	Plan, InputSHA, DirectoryID, DirectorySHA string
}

func (p *nativeDirectorySelection) reference(plan string) *nativeDirectorySelectionReference {
	if p == nil {
		return nil
	}
	return &nativeDirectorySelectionReference{1, plan, p.InputSHA, p.DirectoryID, p.DirectorySHA}
}

func (p *nativeDirectorySelection) UnmarshalJSON(raw []byte) error {
	type selection nativeDirectorySelection
	var value selection
	if e := cleanupJournalStrict(raw, &value); e != nil {
		return e
	}
	*p = nativeDirectorySelection(value)
	return nil
}

func nativeSourceType(kind string) any {
	switch kind {
	case "movie":
		return "电影"
	case "tv":
		return "电视剧"
	case "mixed", "":
		return nil
	}
	return kind
}
func nativeOptionalText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nativeDirectoryInputs(c Settings) (nativeSourceDirectories, error) {
	out := nativeSourceDirectories{Version: 1, Namespace: "native.directories", Rules: []nativeSourceDirectory{}}
	for _, r := range c.Directories {
		source, e := directorySourceWithSettings(c, r)
		if e != nil {
			return out, e
		}
		dest := ruleLibraryPath(c, r)
		monitor := ""
		if r.Enabled && r.MonitorType != "none" {
			monitor = r.MonitorType
		}
		transfer := r.Mode
		if transfer == "hardlink" {
			transfer = "link"
		}
		var mode any
		if r.OverwriteModeKnown && r.OverwriteMode != nil {
			mode = *r.OverwriteMode
		}
		var libraryStorage any
		if dest != "" {
			libraryStorage = "local"
		}
		values := directorysource.Object{"name": r.Name, "priority": json.Number(strconv.Itoa(r.Priority)), "storage": "local", "download_path": source, "media_type": nativeSourceType(r.MediaType), "media_category": nativeOptionalText(r.MediaCategory), "monitor_type": nativeOptionalText(monitor), "transfer_type": nativeOptionalText(transfer), "overwrite_mode": mode, "library_path": nativeOptionalText(dest), "library_storage": libraryStorage, "renaming": importBool(r.Renaming, true), "scraping": importBool(r.Scraping, true), "download_type_folder": r.DownloadTypeFolder, "download_category_folder": r.DownloadCategoryFolder, "library_category_folder": importBool(r.LibraryTypeFolder, false), "library_type_folder": false}
		rule, e := directorysource.Normalize(values)
		if e != nil {
			return out, e
		}
		out.Rules = append(out.Rules, nativeSourceDirectory{r.ID, directoryFingerprint(r), r.OverwriteModeKnown, rule})
	}
	out.InputSHA = tokenHash(string(mustCleanSeedJSON(struct {
		Version   int
		Namespace string
		Rules     []nativeSourceDirectory
	}{out.Version, out.Namespace, out.Rules})))
	return out, nil
}

func nativeDirectoryDevice(path string) (uint64, bool, error) {
	if !importPath(path) {
		return 0, false, bad("REVIEW_DIRECTORY_PATH", "目录选择路径无效")
	}
	parent, e := importDir(filepath.Dir(path), false)
	if errors.Is(e, os.ErrNotExist) {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	defer parent.Close()
	var st unix.Stat_t
	e = unix.Fstatat(int(parent.Fd()), filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(e, os.ErrNotExist) {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG && st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return 0, false, bad("REVIEW_DIRECTORY_PATH", "同盘选择需要可确认的本地普通文件或目录")
	}
	return uint64(st.Dev), true, nil
}

func (a *App) selectNativeImportDirectory(ctx context.Context, request ImportRequest) (*nativeDirectorySelection, *DirectoryRule, error) {
	c := a.Settings()
	inputs, e := nativeDirectoryInputs(c)
	if e != nil {
		return nil, nil, e
	}
	proof := &nativeDirectorySelection{Version: 1, Method: "get_dir", Inputs: inputs, Files: []string{}, MediaType: request.MediaType, TMDBID: request.TMDBID, Disks: []nativeDirectoryDiskObservation{}}
	for _, f := range request.Files {
		proof.Files = append(proof.Files, f.Path)
	}
	if request.directoryRule != nil {
		r, e := a.getDirectory(request.directoryRule.ID)
		if e != nil || directoryFingerprint(r) != directoryFingerprint(*request.directoryRule) {
			return nil, nil, bad("REVIEW_DIRECTORY_CHANGED", "显式整理目录已经变化")
		}
		if !r.Enabled || r.MonitorType == "none" {
			return nil, nil, bad("DIRECTORY_MONITOR_DISABLED", "显式监听目录已停用")
		}
		source, e := directorySourceWithSettings(c, r)
		if e != nil {
			return nil, nil, e
		}
		for _, f := range request.Files {
			if !sharedDirectoryContains(source, f.Path) {
				return nil, nil, bad("REVIEW_DIRECTORY_CHANGED", "文件已离开显式目录作用域")
			}
		}
		proof.Method = "target_directory"
		proof.DirectoryID = r.ID
		proof.DirectorySHA = directoryFingerprint(r)
		for _, x := range inputs.Rules {
			if x.ID == r.ID {
				proof.Rule = x.Rule
			}
		}
		proof.InputSHA = tokenHash(string(mustCleanSeedJSON(proof)))
		return proof, &r, nil
	}
	mediaType, _ := nativeSourceType(request.MediaType).(string)
	media := directorysource.Media{Type: mediaType, CategoryKnown: false}
	query := directorysource.Query{Media: &media}
	dest := request.TargetPath
	// RootID is the native UI's explicit physical destination. Actual download
	// and watcher producers use their source path/explicit target_directory.
	if dest == "" && request.IntentID == "" && request.RootID != "" {
		for _, r := range c.Roots {
			if r.ID == request.RootID {
				dest = r.Path
				break
			}
		}
	}
	local := "local"
	query.TargetStorage = &local
	if dest != "" {
		query.DestPath = &dest
	} else {
		query.Storage = &local
	}
	rules := make([]directorysource.Rule, len(inputs.Rules))
	for n, r := range inputs.Rules {
		rules[n] = r.Rule
	}
	var selected *directorysource.Rule
	for n, f := range request.Files {
		if dest == "" {
			path := f.Path
			query.SourcePath = &path
		}
		var disks []nativeDirectoryDiskObservation
		same := func(ctx context.Context, source, target string) (bool, error) {
			if e := ctx.Err(); e != nil {
				return false, e
			}
			sd, se, e := nativeDirectoryDevice(source)
			if e != nil {
				return false, e
			}
			td, te, e := nativeDirectoryDevice(target)
			if e != nil {
				return false, e
			}
			disks = append(disks, nativeDirectoryDiskObservation{source, target, se, te, sd, td})
			return se && te && sd == td, nil
		}
		got, e := directorysource.GetDir(ctx, true, rules, query, same)
		if errors.Is(e, directorysource.ErrDependency) && !media.CategoryKnown {
			if !validMedia(request.MediaType, request.TMDBID) {
				return nil, nil, e
			}
			identity, ce := a.media(ctx, request.MediaType, request.TMDBID)
			if ce != nil {
				return nil, nil, ce
			}
			_, category, ce := a.importMediaCategory(ctx, identity)
			if ce != nil {
				return nil, nil, ce
			}
			media.CategoryKnown = true
			if category != "" {
				media.Category = &category
			}
			got, e = directorysource.GetDir(ctx, true, rules, query, same)
		}
		if e != nil {
			return nil, nil, e
		}
		if n == 0 {
			selected = got
			proof.Query = query
		} else if !reflect.DeepEqual(selected, got) {
			return nil, nil, bad("REVIEW_DIRECTORY_SELECTION", "文件按原目录规则属于不同目标，请分别整理")
		}
		proof.Disks = append(proof.Disks, disks...)
	}
	if selected == nil {
		proof.InputSHA = tokenHash(string(mustCleanSeedJSON(proof)))
		return proof, nil, nil
	}
	for n, r := range inputs.Rules {
		if reflect.DeepEqual(r.Rule, *selected) {
			if r.ID == "" {
				return nil, nil, bad("REVIEW_DIRECTORY_SELECTION", "已选目录缺少原生身份")
			}
			rule := c.Directories[n]
			proof.DirectoryID = r.ID
			proof.DirectorySHA = r.SHA
			proof.Rule = r.Rule
			proof.InputSHA = tokenHash(string(mustCleanSeedJSON(proof)))
			return proof, &rule, nil
		}
	}
	return nil, nil, directorysource.ErrDependency
}

func (a *App) guardNativeDirectorySelection(p *nativeDirectorySelection) error {
	if p == nil {
		return nil
	}
	if p.Version != 1 || p.Inputs.Namespace != "native.directories" {
		return bad("REVIEW_DIRECTORY_SELECTION", "目录选择来源无效")
	}
	copyProof := *p
	copyProof.InputSHA = ""
	if p.InputSHA != tokenHash(string(mustCleanSeedJSON(copyProof))) {
		return bad("REVIEW_DIRECTORY_SELECTION", "目录选择输入已变化")
	}
	current, e := nativeDirectoryInputs(a.Settings())
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(current, p.Inputs) {
		return bad("REVIEW_DIRECTORY_CHANGED", "目录规则或配置顺序已变化，请重新预览")
	}
	for _, d := range p.Disks {
		sd, se, e := nativeDirectoryDevice(d.Source)
		if e != nil {
			return e
		}
		td, te, e := nativeDirectoryDevice(d.Target)
		if e != nil {
			return e
		}
		if sd != d.SourceDevice || td != d.TargetDevice || se != d.SourceExists || te != d.TargetExists {
			return bad("REVIEW_DIRECTORY_CHANGED", "原目录同盘选择事实已变化，请重新预览")
		}
	}
	return nil
}
