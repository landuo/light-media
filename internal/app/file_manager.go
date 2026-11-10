package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/transferextensions"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"

	"golang.org/x/sys/unix"
)

// Configured browser roots grant file operations. The container root is only
// accepted by the directory listing endpoint.
type browserRoot struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}

const containerBrowserRootID = "container"

func containerBrowserRoot() browserRoot {
	return browserRoot{ID: containerBrowserRootID, Name: "本地", Path: "/", Kind: "container"}
}

type browserEntry struct {
	sourceNatural []originalBrowserNaturalPart
	Name          string `json:"name"`
	Path          string `json:"path"`
	Type          string `json:"type"`
	Size          int64  `json:"size"`
	Modified      string `json:"modified"`
	Video         bool   `json:"video"`
	Protected     string `json:"protected,omitempty"`
}
type browserStamp struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Size   int64  `json:"size"`
	ModNS  int64  `json:"mod_ns"`
	Mode   uint32 `json:"mode"`
}
type browserOperationItem struct {
	OriginalFileItem    *plugins.ModuleFileItem `json:"original_fileitem,omitempty"`
	SourceModuleStarted bool                    `json:"source_module_started,omitempty"`
	Unrecoverable       bool                    `json:"unrecoverable,omitempty"`
	Source              string                  `json:"source"`
	Target              string                  `json:"target"`
	Manifest            []browserStamp          `json:"manifest"`
	Done                bool                    `json:"done"`
}
type browserOperation struct {
	ModuleError *APIError              `json:"module_error,omitempty"`
	ID          string                 `json:"id"`
	Root        browserRoot            `json:"root"`
	Action      string                 `json:"action"`
	Items       []browserOperationItem `json:"items"`
	Parent      browserStamp           `json:"parent"`
	State       string                 `json:"state"`
	CreatedAt   string                 `json:"created_at"`
	ExpiresAt   string                 `json:"expires_at"`
	Warnings    []string               `json:"warnings"`
}
type browserOperationRequest struct {
	Action string   `json:"action"`
	RootID string   `json:"root_id"`
	Path   string   `json:"path"`
	Paths  []string `json:"paths"`
	Name   string   `json:"name"`
}

func (a *App) browserRoots() []browserRoot {
	roots := []browserRoot{}
	seen := map[string]bool{}
	add := func(path, name, kind string) {
		if !importPath(path) || path == "/" || seen[path] {
			return
		}
		seen[path] = true
		if name == "" {
			name = filepath.Base(path)
		}
		h := sha256.Sum256([]byte(path))
		roots = append(roots, browserRoot{ID: "root_" + hex.EncodeToString(h[:8]), Path: path, Name: name, Kind: kind})
	}
	for _, r := range a.Settings().AllowedRoots {
		add(r, filepath.Base(r), "source")
	}
	for _, r := range a.Settings().Roots {
		add(r.Path, r.Name, "library")
	}
	return roots
}
func (a *App) browserRoot(id, path string) (browserRoot, error) {
	for _, root := range a.browserRoots() {
		if root.ID == id && importPath(path) && within(root.Path, path) && !browserInternal(path) {
			return root, nil
		}
	}
	return browserRoot{}, bad("FILE_ROOT_REJECTED", "路径不在当前允许的浏览目录内")
}
func browserInternal(path string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".light-media-") {
			return true
		}
	}
	return false
}
func browserInfo(path string, info os.FileInfo) (browserStamp, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (!info.IsDir() && !info.Mode().IsRegular()) {
		return browserStamp{}, bad("FILE_SPECIAL_REJECTED", "不允许符号链接或特殊文件")
	}
	return browserStamp{path, uint64(s.Dev), uint64(s.Ino), info.Size(), info.ModTime().UnixNano(), uint32(info.Mode())}, nil
}
func browserStat(path string) (browserStamp, error) {
	parent, e := importDir(filepath.Dir(path), false)
	if e != nil {
		return browserStamp{}, e
	}
	defer parent.Close()
	var st unix.Stat_t
	if e = unix.Fstatat(int(parent.Fd()), filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		return browserStamp{}, e
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return browserStamp{}, bad("FILE_SYMLINK_REJECTED", "不允许符号链接")
	}
	var f *os.File
	if st.Mode&unix.S_IFMT == unix.S_IFDIR {
		f, e = importDir(path, false)
	} else {
		f, e = importOpen(path)
	}
	if e != nil {
		return browserStamp{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return browserStamp{}, e
	}
	return browserInfo(path, info)
}

// unlink needs directory access, not read access to the selected file. Keep a
// no-follow, directory-bound metadata snapshot for plugin storage operations.
func browserEntryStat(path string) (browserStamp, error) {
	info, err := pluginStorageEntryInfo(path)
	if err != nil {
		return browserStamp{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return browserStamp{}, bad("FILE_SYMLINK_REJECTED", "不允许符号链接")
	}
	return browserInfo(path, info)
}
func sameBrowserIdentity(a, b browserStamp) bool {
	return a.Device == b.Device && a.Inode == b.Inode && a.Mode == b.Mode
}

func (a *App) fileManagerAPI(r *http.Request, p string) (any, error) {
	ctx := r.Context()
	switch {
	case p == "organize" && r.Method == "POST":
		var in manualOrganizeRequest
		if e := decodeBody(nil, r, &in); e != nil {
			return nil, e
		}
		if in.Background && !in.Preview {
			return a.submitManualOrganize(ctx, in)
		}
		return a.manualOrganize(ctx, in)
	case p == "recognize" && r.Method == "POST":
		var in struct {
			Path string `json:"path"`
		}
		if e := decodeBody(nil, r, &in); e != nil {
			return nil, e
		}
		return a.recognizeBrowserFile(ctx, in.Path)
	case p == "roots" && r.Method == "GET":
		roots := a.browserRoots()
		if r.URL.Query().Get("scope") == "container" {
			roots = append(roots, containerBrowserRoot())
		}
		return roots, nil
	case p == "list" && r.Method == "GET":
		return a.browserList(ctx, r.URL.Query().Get("root_id"), r.URL.Query().Get("path"), r.URL.Query().Get("sort"), r.URL.Query().Get("order"))
	case p == "scan" && r.Method == "POST":
		var in struct {
			Paths     []string `json:"paths"`
			Recursive bool     `json:"recursive"`
		}
		if e := decodeBody(nil, r, &in); e != nil {
			return nil, e
		}
		return a.browserScan(ctx, in.Paths, in.Recursive)
	case p == "operation-preview" && r.Method == "POST":
		var in browserOperationRequest
		if e := decodeBody(nil, r, &in); e != nil {
			return nil, e
		}
		return a.previewBrowserOperation(ctx, in)
	case p == "operation-confirm" && r.Method == "POST":
		var in struct {
			PlanID  string `json:"plan_id"`
			Confirm bool   `json:"confirm"`
		}
		if e := decodeBody(nil, r, &in); e != nil {
			return nil, e
		}
		if !in.Confirm {
			return nil, bad("CONFIRM_REQUIRED", "请明确确认文件操作")
		}
		return a.confirmBrowserOperation(ctx, in.PlanID)
	case p == "operation-restore" && r.Method == "POST":
		var in struct {
			PlanID  string `json:"plan_id"`
			Confirm bool   `json:"confirm"`
		}
		if e := decodeBody(nil, r, &in); e != nil {
			return nil, e
		}
		if !in.Confirm {
			return nil, bad("CONFIRM_REQUIRED", "请明确确认从回收区恢复")
		}
		return a.restoreBrowserOperation(ctx, in.PlanID)
	case p == "recycle" && r.Method == "GET":
		return a.browserRecycle(ctx, r.URL.Query().Get("root_id"))
	case p == "scrape" && r.Method == "POST":
		var in struct {
			Paths   []string `json:"paths"`
			Confirm bool     `json:"confirm"`
		}
		if e := decodeBody(nil, r, &in); e != nil {
			return nil, e
		}
		if !in.Confirm {
			return nil, bad("CONFIRM_REQUIRED", "请确认刮削已登记的媒体")
		}
		return a.browserScrape(ctx, in.Paths)
	default:
		return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
	}
}
func (a *App) browserList(ctx context.Context, rootID, path, sorting, order string) (any, error) {
	extensions, e := a.sourceFileExtensions(ctx)
	if e != nil {
		return nil, e
	}
	display, e := transferextensions.New(extensions)
	if e != nil {
		return nil, e
	}
	root := containerBrowserRoot()
	if rootID == containerBrowserRootID {
		if !importPath(path) || browserInternal(path) {
			return nil, bad("INVALID_PATH", "容器目录路径无效")
		}
	} else {
		root, e = a.browserRoot(rootID, path)
		if e != nil {
			return nil, e
		}
		if entries, truncated, handled, err := a.originalBrowserStorageList(ctx, path, display); handled {
			if err != nil {
				return nil, err
			}
			return browserListResult(root, path, sorting, order, entries, truncated), nil
		}
	}
	f, e := importDir(path, false)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	entries := []browserEntry{}
	truncated := false
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		items, err := f.ReadDir(100)
		for _, item := range items {
			if strings.HasPrefix(item.Name(), ".light-media-") {
				continue
			}
			if len(entries) >= 10000 {
				truncated = true
				break
			}
			info, ie := item.Info()
			if ie != nil {
				continue
			}
			kind := "file"
			protected := ""
			if item.IsDir() {
				kind = "dir"
			} else if item.Type()&os.ModeSymlink != 0 {
				kind = "symlink"
				protected = "符号链接禁止访问"
			} else if !info.Mode().IsRegular() {
				kind = "special"
				protected = "特殊文件禁止操作"
			}
			name := filepath.Join(path, item.Name())
			if protected == "" {
				if rootID == containerBrowserRootID {
					protected = "容器目录仅供浏览"
				} else {
					protected = a.browserStaticProtection(name)
				}
			}
			video, _ := display.Media(transferextensions.PathFile(name))
			entries = append(entries, browserEntry{Name: item.Name(), Path: name, Type: kind, Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339), Video: video, Protected: protected})
		}
		if truncated || err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return browserListResult(root, path, sorting, order, entries, truncated), nil
}
func browserListResult(root browserRoot, path, sorting, order string, entries []browserEntry, truncated bool) any {
	if sorting != "time" && sorting != "size" {
		sorting = "name"
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.Type == "dir") != (b.Type == "dir") {
			return a.Type == "dir"
		}
		less := strings.ToLower(a.Name) < strings.ToLower(b.Name)
		if sorting == "name" && a.sourceNatural != nil && b.sourceNatural != nil {
			compared := originalBrowserNaturalCompare(a.sourceNatural, b.sourceNatural)
			if order == "desc" {
				return compared > 0
			}
			return compared < 0
		}
		if sorting == "time" && a.Modified != b.Modified {
			less = a.Modified < b.Modified
		}
		if sorting == "size" && a.Size != b.Size {
			less = a.Size < b.Size
		}
		if order == "desc" {
			return !less && a.Name != b.Name
		}
		return less
	})
	parent := ""
	if path != root.Path {
		parent = filepath.Dir(path)
	}
	return map[string]any{"root": root, "path": path, "parent": parent, "items": entries, "truncated": truncated}
}
func (a *App) browserScan(ctx context.Context, paths []string, recursive bool) (any, error) {
	if len(paths) == 0 || len(paths) > 200 {
		return nil, bad("INVALID_SELECTION", "请选择 1–200 个文件或目录")
	}
	classifier, e := a.transferMediaClassifier(ctx)
	if e != nil {
		return nil, e
	}
	files := []importScanFile{}
	seen := map[string]bool{}
	truncated := false
	for _, path := range paths {
		allowed := a.sourceAllowed(path)
		for _, root := range a.Settings().Roots {
			allowed = allowed || (importPath(path) && within(root.Path, path))
		}
		if !allowed || browserInternal(path) {
			return nil, bad("INVALID_SOURCE", "选择的文件不在允许目录内")
		}
		stamp, e := browserStat(path)
		if e != nil {
			return nil, e
		}
		if os.FileMode(stamp.Mode).IsDir() {
			collected, limited, e := a.browserCollectFiles(ctx, path, recursive)
			if e != nil {
				return nil, e
			}
			truncated = truncated || limited
			for _, f := range collected {
				if !seen[f.Path] {
					seen[f.Path] = true
					files = append(files, f)
				}
			}
		} else if video, _ := classifier.Media(transferextensions.PathFile(path)); video && !seen[path] {
			seen[path] = true
			f := importScanFile{Path: path, Size: stamp.Size, Parsed: a.parseMediaName(filepath.Base(path))}
			f.InferredMedia, f.Numbering, f.IdentityReason = a.importNFOEvidence(path)
			if f.InferredMedia != nil {
				f.IdentitySource = "nfo"
			}
			files = append(files, f)
		}
		if len(files) > 2000 {
			return nil, bad("FILE_SELECTION_LIMIT", "一次最多识别 2000 个视频，请缩小选择范围")
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return map[string]any{"files": files, "truncated": truncated}, nil
}

func (a *App) browserCollectFiles(ctx context.Context, path string, recursive bool) ([]importScanFile, bool, error) {
	classifier, e := a.transferMediaClassifier(ctx)
	if e != nil {
		return nil, false, e
	}
	files := []importScanFile{}
	visited := 0
	truncated := false
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if depth > 16 {
			truncated = true
			return nil
		}
		f, e := importDir(path, false)
		if e != nil {
			return e
		}
		defer f.Close()
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entries, readErr := f.ReadDir(100)
			for _, entry := range entries {
				visited++
				if visited > 20000 || len(files) >= 2000 {
					truncated = true
					return nil
				}
				if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				name := filepath.Join(path, entry.Name())
				if entry.IsDir() {
					if recursive {
						if e = walk(name, depth+1); e != nil {
							return e
						}
					}
					continue
				}
				if media, _ := classifier.Media(transferextensions.PathFile(name)); !media {
					continue
				}
				stamp, e := snapshotPath(name)
				if e != nil {
					return e
				}
				file := importScanFile{Path: name, Size: stamp.Size, Parsed: a.parseMediaName(entry.Name())}
				file.InferredMedia, file.Numbering, file.IdentityReason = a.importNFOEvidence(name)
				if file.InferredMedia != nil {
					file.IdentitySource = "nfo"
				}
				files = append(files, file)
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	e = walk(path, 0)
	return files, truncated, e
}

func (a *App) browserStaticProtection(path string) string {
	if within(a.Dir, path) || within(path, a.Dir) {
		return "应用数据目录受保护"
	}
	for _, root := range a.Settings().Roots {
		if mapped, overlap := sharedDirectoryPath(root.Path, path); overlap && within(mapped, root.Path) {
			return "媒体库根目录不能改名或删除"
		}
	}
	for _, root := range a.browserRoots() {
		if path == root.Path {
			return "允许根目录不能改名或删除"
		}
	}
	for _, d := range a.Settings().Downloaders {
		paths := []string{d.DownloadPath}
		for _, m := range d.Mappings {
			if within(m.From, d.DownloadPath) {
				rel, _ := filepath.Rel(m.From, d.DownloadPath)
				paths = append(paths, filepath.Join(m.To, rel))
			}
		}
		for _, p := range paths {
			if importPath(p) && (within(p, path) || within(path, p)) {
				return "下载器目录受保护，防止破坏下载与做种"
			}
		}
	}
	return ""
}
func (a *App) browserMutationAllowed(ctx context.Context, path string) error {
	return a.browserMutationAllowedWithReferences(ctx, path, true)
}

// Interactive file operations protect registered media. Plugin storage follows
// LocalModule/Path.unlink: registration does not prevent an explicitly granted
// deletion, while static protection and active imports still apply.
func (a *App) browserMutationAllowedWithReferences(ctx context.Context, path string, protectReferences bool) error {
	if reason := a.browserStaticProtection(path); reason != "" {
		return bad("FILE_PROTECTED", reason)
	}
	return a.browserMutationReferences(ctx, path, protectReferences)
}

func (a *App) browserMutationReferences(ctx context.Context, path string, protectReferences bool) error {
	identities := map[string]mountedDirectoryID{}
	identityErrors := map[string]error{}
	identity := func(dir string) (mountedDirectoryID, error) {
		if value, ok := identities[dir]; ok {
			return value, identityErrors[dir]
		}
		value, err := mountedDirectoryIdentity(dir)
		identities[dir], identityErrors[dir] = value, err
		return value, err
	}
	contains := func(referenced string) bool {
		return referenced != "" && sharedDirectoryContainsWithIdentity(path, referenced, identity)
	}
	query := `SELECT kind,payload FROM records WHERE kind IN ('asset','managed_file') UNION ALL SELECT kind,payload FROM records INDEXED BY import_plan_state WHERE kind='import_plan' AND json_extract(payload,'$.state') IN ('queued','running')`
	if !protectReferences {
		query = `SELECT kind,payload FROM records INDEXED BY import_plan_state WHERE kind='import_plan' AND json_extract(payload,'$.state') IN ('queued','running')`
	}
	rows, e := a.Store.DB.QueryContext(ctx, query)
	if e != nil {
		return e
	}
	defer rows.Close()
	assets := []Asset{}
	states := map[string]string{}
	managed := []managedFile{}
	for rows.Next() {
		var kind string
		var raw []byte
		if e = rows.Scan(&kind, &raw); e != nil {
			return e
		}
		switch kind {
		case "asset":
			var asset Asset
			if e = json.Unmarshal(raw, &asset); e != nil {
				return e
			}
			states[asset.ID] = asset.State
			if asset.State == "removed" || asset.State == "superseded" {
				continue
			}
			assets = append(assets, asset)
			if asset.Path != "" && (contains(asset.Path) || contains(asset.SourcePath)) {
				return bad("FILE_IN_USE", "文件被有效整理记录引用，请通过整理历史操作")
			}
		case "managed_file":
			var file managedFile
			if e = json.Unmarshal(raw, &file); e != nil {
				return e
			}
			if file.Path != "" && contains(file.Path) {
				managed = append(managed, file)
			}
		case "import_plan":
			var plan ImportPlan
			if e = json.Unmarshal(raw, &plan); e != nil {
				return e
			}
			if plan.State == "queued" || plan.State == "running" {
				for _, f := range plan.Files {
					if contains(f.Source) || contains(f.Target) {
						return bad("FILE_IN_USE", "文件正在整理任务中使用")
					}
				}
			}
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	for _, file := range managed {
		if file.AssetID != "" {
			if states[file.AssetID] != "removed" && states[file.AssetID] != "superseded" {
				return bad("FILE_IN_USE", "文件属于自管视频或旁车，请通过整理历史操作")
			}
			continue
		}
		// Show NFOs and season/show images are shared by episodes rather than
		// owned by one asset. Keep them protected while their show is in use.
		for _, asset := range assets {
			if asset.RootID == file.RootID && mediaKey(asset.Media) == file.MediaKey && within(filepath.Dir(file.Path), asset.Path) {
				return bad("FILE_IN_USE", "文件属于仍在使用的影视元数据，请通过整理历史操作")
			}
		}
	}
	return nil
}
func browserManifest(ctx context.Context, path string) ([]browserStamp, error) {
	return browserManifestWithHardlinkGuard(ctx, path, true)
}

// The interactive file manager protects hardlinks. Ordinary plugin storage
// deletion has the source LocalModule/Path.unlink semantics: remove this owned
// directory entry, keeping every other hardlink and its inode contents intact.
// Both callers retain the same traversal, snapshot and path protections.
func browserManifestWithHardlinkGuard(ctx context.Context, path string, protectHardlinks bool) ([]browserStamp, error) {
	result := []browserStamp{}
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if depth > 16 || len(result) >= 10000 {
			return bad("FILE_OPERATION_LIMIT", "目录过深或超过 10000 项，请缩小操作范围")
		}
		stat := browserStat
		if !protectHardlinks {
			stat = browserEntryStat
		}
		st, e := stat(path)
		if e != nil {
			return e
		}
		result = append(result, st)
		if !os.FileMode(st.Mode).IsDir() {
			if !protectHardlinks {
				return nil
			}
			f, e := importOpen(path)
			if e != nil {
				return e
			}
			info, e := f.Stat()
			f.Close()
			if e != nil {
				return e
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
				return bad("FILE_LINK_PROTECTED", "文件存在硬链接，为保护整理资产和做种源，禁止破坏性操作")
			}
			return nil
		}
		f, e := importDir(path, false)
		if e != nil {
			return e
		}
		defer f.Close()
		for {
			items, readErr := f.ReadDir(100)
			for _, item := range items {
				if strings.HasPrefix(item.Name(), ".light-media-") {
					return bad("FILE_INTERNAL_PROTECTED", "目录包含应用回收文件，不能整体变更")
				}
				if e = walk(filepath.Join(path, item.Name()), depth+1); e != nil {
					return e
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
	e := walk(path, 0)
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, e
}
func validBrowserName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 240 && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00\r\n") && !strings.HasPrefix(name, ".light-media-")
}
func (a *App) previewBrowserOperation(ctx context.Context, in browserOperationRequest) (browserOperation, error) {
	root, e := a.browserRoot(in.RootID, in.Path)
	if e != nil {
		return browserOperation{}, e
	}
	if in.Action != "mkdir" && in.Action != "rename" && in.Action != "delete" {
		return browserOperation{}, bad("INVALID_FILE_ACTION", "不支持的文件操作")
	}
	parent, e := browserStat(in.Path)
	if e != nil {
		return browserOperation{}, e
	}
	if !os.FileMode(parent.Mode).IsDir() {
		return browserOperation{}, bad("INVALID_DIRECTORY", "请选择父目录")
	}
	plan := browserOperation{ID: id("fop_"), Root: root, Action: in.Action, Parent: parent, State: "preview", CreatedAt: now(), ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339), Items: []browserOperationItem{}, Warnings: []string{}}
	if in.Action == "mkdir" {
		if !validBrowserName(in.Name) {
			return plan, bad("INVALID_FILE_NAME", "目录名无效")
		}
		target := filepath.Join(in.Path, in.Name)
		if e = a.browserMutationAllowed(ctx, target); e != nil {
			return plan, e
		}
		if _, e = browserStat(target); !errors.Is(e, os.ErrNotExist) {
			return plan, bad("FILE_TARGET_EXISTS", "目标已存在或不可访问，不会覆盖")
		}
		plan.Items = append(plan.Items, browserOperationItem{Target: target})
	} else {
		if len(in.Paths) == 0 || len(in.Paths) > 100 || (in.Action == "rename" && len(in.Paths) != 1) {
			return plan, bad("INVALID_SELECTION", "请选择文件；重命名每次只能选择一个项目")
		}
		if in.Action == "rename" && !validBrowserName(in.Name) {
			return plan, bad("INVALID_FILE_NAME", "新名称无效")
		}
		seen := map[string]bool{}
		totalManifest := 0
		for _, path := range in.Paths {
			if _, e = a.browserRoot(root.ID, path); e != nil {
				return plan, e
			}
			if filepath.Dir(path) != in.Path || seen[path] {
				return plan, bad("INVALID_SELECTION", "只能操作当前目录下不重复的选中项目")
			}
			seen[path] = true
			if e = a.browserMutationAllowed(ctx, path); e != nil {
				return plan, e
			}
			manifest, e := browserManifest(ctx, path)
			if e != nil {
				return plan, e
			}
			totalManifest += len(manifest)
			if totalManifest > 10000 {
				return plan, bad("FILE_OPERATION_LIMIT", "一次操作最多 10000 个文件和目录，请缩小范围")
			}
			target := filepath.Join(in.Path, in.Name)
			if in.Action == "delete" {
				target = filepath.Join(in.Path, ".light-media-trash-"+plan.ID+"-"+strconv.Itoa(len(plan.Items)))
			}
			if _, e = browserStat(target); !errors.Is(e, os.ErrNotExist) {
				return plan, bad("FILE_TARGET_EXISTS", "目标已存在或不可访问，不会覆盖")
			}
			plan.Items = append(plan.Items, browserOperationItem{Source: path, Target: target, Manifest: manifest})
		}
	}
	if in.Action == "delete" {
		plan.Warnings = append(plan.Warnings, "删除将所选项目原子移入原目录内的隐藏回收区，保留数据；不会递归擦除文件，也不改变媒体库和下载器文件。回收路径会显示在执行结果中。")
	}
	if in.Action == "rename" || in.Action == "delete" {
		if e = a.guardBrowserDownloaderSources(ctx, in.Paths); e != nil {
			return plan, e
		}
	}
	return plan, a.Store.Put(ctx, "file_operation", plan.ID, plan)
}

// Existing torrents need not have an application intent. Check their actual
// save directories once per relevant instance at each destructive-operation
// stage; a failed or unmappable daemon response is never treated as an empty
// source set. Unrelated local browser roots do not make network requests.
func (a *App) guardBrowserDownloaderSources(ctx context.Context, paths []string) error {
	for _, configured := range a.Settings().Downloaders {
		potential := []string{configured.DownloadPath}
		if configured.SharedPaths || configured.DownloadPath == "" {
			// A daemon-managed default need not be entered in application settings.
			// Without a frozen default, verify mounted browser roots before a
			// destructive operation so existing seeding files remain protected.
			potential = append(potential, a.Settings().AllowedRoots...)
		}
		for _, mapping := range configured.Mappings {
			potential = append(potential, mapping.To)
		}
		relevant := false
		for _, root := range potential {
			if !importPath(root) {
				continue
			}
			for _, path := range paths {
				if within(root, path) || within(path, root) {
					relevant = true
					break
				}
			}
		}
		if !relevant {
			continue
		}
		client, e := download.New(configured.Config)
		if e != nil {
			return bad("FILE_DOWNLOADER_UNVERIFIED", "无法连接相关下载器核对做种源，已阻止重命名或删除，请检查连接配置")
		}
		tasks, e := client.List(ctx)
		if e != nil {
			return bad("FILE_DOWNLOADER_UNVERIFIED", "无法读取相关下载器的现有任务，不能确认做种源，已阻止重命名或删除")
		}
		for _, task := range tasks {
			mapped, e := mapDownloaderPath(a.Settings(), configured, task.DownloadDir)
			if e != nil || !importPath(mapped) {
				return bad("FILE_DOWNLOADER_MAPPING_UNVERIFIED", "下载器返回的保存路径在当前容器中不可见，无法核对做种源，请检查容器挂载")
			}
			for _, path := range paths {
				if _, overlap := sharedDirectoryPath(mapped, path); overlap {
					return bad("FILE_DOWNLOADER_SOURCE_PROTECTED", "所选项目位于下载器现有任务的保存目录内，为保护下载和做种，禁止重命名或删除")
				}
			}
		}
	}
	return nil
}
func (a *App) confirmBrowserOperation(ctx context.Context, id string) (browserOperation, error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var plan browserOperation
	if e := a.Store.Get(ctx, "file_operation", id, &plan); e != nil {
		return plan, e
	}
	if plan.State == "completed" {
		return plan, nil
	}
	if plan.ModuleError != nil {
		plan.ModuleError.Status = 400
		return plan, plan.ModuleError
	}
	if plan.State == "restored" {
		return plan, bad("FILE_PREVIEW_CONSUMED", "该删除已恢复，请重新预览操作")
	}
	exp, e := time.Parse(time.RFC3339, plan.ExpiresAt)
	if e != nil || (plan.State == "preview" && time.Now().After(exp)) {
		return plan, bad("FILE_PREVIEW_EXPIRED", "文件操作预览已过期，请重新预览")
	}
	root, e := a.browserRoot(plan.Root.ID, plan.Parent.Path)
	if e != nil || root != plan.Root {
		return plan, bad("FILE_ROOT_CHANGED", "允许目录设置已变化，请重新预览")
	}
	parent, e := importDir(plan.Parent.Path, false)
	if e != nil {
		return plan, e
	}
	defer parent.Close()
	info, e := parent.Stat()
	if e != nil {
		return plan, e
	}
	actual, e := browserInfo(plan.Parent.Path, info)
	if e != nil || !sameBrowserIdentity(plan.Parent, actual) {
		return plan, bad("FILE_PARENT_CHANGED", "父目录已变化，请重新预览")
	}
	// Validate every selected subtree before changing any one of them.
	for index := range plan.Items {
		item := &plan.Items[index]
		path := item.Source
		if path == "" {
			path = item.Target
		}
		if e = a.browserMutationAllowed(ctx, path); e != nil {
			return plan, e
		}
		if plan.Action != "mkdir" && !item.Done {
			// A process can stop after atomic rename and before receipt persistence.
			// Reconcile only the exact original inode subtree at the frozen target.
			if plan.State == "executing" || plan.State == "partial" {
				if _, statErr := browserStat(item.Source); errors.Is(statErr, os.ErrNotExist) {
					moved, checkErr := browserManifest(ctx, item.Target)
					if checkErr == nil && sameRebasedManifest(item.Manifest, moved, item.Source, item.Target) {
						item.Done = true
						continue
					}
				}
			}
			manifest, e := browserManifest(ctx, item.Source)
			if e != nil {
				return plan, e
			}
			if !sameBrowserManifest(manifest, item.Manifest) {
				return plan, bad("FILE_SNAPSHOT_CHANGED", "选中的文件或目录在预览后已变化，请重新预览")
			}
		}
		if !item.Done {
			if _, e = browserStat(item.Target); !errors.Is(e, os.ErrNotExist) {
				return plan, bad("FILE_TARGET_EXISTS", "目标已存在，不会覆盖")
			}
		}
	}
	if plan.Action == "rename" || plan.Action == "delete" {
		paths := []string{}
		for _, item := range plan.Items {
			if !item.Done {
				paths = append(paths, item.Source)
			}
		}
		if e = a.guardBrowserDownloaderSources(ctx, paths); e != nil {
			return plan, e
		}
		// The read-only daemon requests can take seconds. Recheck every subtree
		// after that wait: rewriting an existing child does not change its parent
		// directory mtime, so checking only the root inode would miss that change.
		for _, item := range plan.Items {
			if item.Done {
				continue
			}
			current, checkErr := browserManifest(ctx, item.Source)
			if checkErr != nil {
				return plan, checkErr
			}
			if !sameBrowserManifest(current, item.Manifest) {
				return plan, bad("FILE_SNAPSHOT_CHANGED", "选中的文件或目录在核对下载器期间已变化，请重新预览")
			}
		}
	}
	release, e := a.beginWrite()
	if e != nil {
		return plan, e
	}
	defer release()
	plan.State = "executing"
	if e = a.Store.Put(ctx, "file_operation", plan.ID, plan); e != nil {
		return plan, e
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Done {
			continue
		}
		if plan.Action != "mkdir" {
			current, checkErr := browserStat(item.Source)
			if checkErr != nil || len(item.Manifest) == 0 || current != item.Manifest[0] {
				return plan, bad("FILE_SNAPSHOT_CHANGED", "文件在执行前已变化，请刷新核对")
			}
		}
		if item.SourceModuleStarted {
			return plan, bad("REVIEW_PLUGIN_FILE_OPERATION", "文件插件已调用，尚无可核对的完成记录，请刷新实际文件后重新操作")
		}
		done, operationErr := a.originalBrowserMutation(ctx, &plan, item, parent, func() error {
			item.SourceModuleStarted = true
			return a.Store.Put(ctx, "file_operation", plan.ID, plan)
		})
		if operationErr != nil {
			item.Done = done
			plan.State = "partial"
			if item.SourceModuleStarted {
				var api *APIError
				if errors.As(operationErr, &api) {
					plan.ModuleError = api
				}
			}
			_ = a.Store.Put(ctx, "file_operation", plan.ID, plan)
			return plan, operationErr
		}
		item.Done = true
		_ = unix.Fsync(int(parent.Fd()))
		if e = a.Store.Put(ctx, "file_operation", plan.ID, plan); e != nil {
			return plan, e
		}
	}
	plan.State = "completed"
	if e = a.Store.Put(ctx, "file_operation", plan.ID, plan); e != nil {
		return plan, e
	}
	a.log("INFO", "files", plan.Action, "文件操作已完成（经预览确认）", "", "")
	return plan, nil
}

func sameBrowserManifest(a, b []browserStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameRebasedManifest(original, current []browserStamp, from, to string) bool {
	if len(original) != len(current) {
		return false
	}
	expected := make([]browserStamp, len(original))
	copy(expected, original)
	for i := range expected {
		relative, e := filepath.Rel(from, expected[i].Path)
		if e != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			return false
		}
		expected[i].Path = filepath.Join(to, relative)
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].Path < expected[j].Path })
	return sameBrowserManifest(expected, current)
}
func (a *App) browserRecycle(ctx context.Context, rootID string) (any, error) {
	var root browserRoot
	for _, r := range a.browserRoots() {
		if r.ID == rootID {
			root = r
		}
	}
	if root.ID == "" {
		return nil, bad("FILE_ROOT_REJECTED", "目录未获允许")
	}
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='file_operation' AND json_extract(payload,'$.root.id')=? AND json_extract(payload,'$.action')='delete' AND json_extract(payload,'$.state') IN ('completed','partial','executing') ORDER BY updated_at DESC LIMIT 100`, rootID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []browserOperation{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var plan browserOperation
		if e = json.Unmarshal(raw, &plan); e != nil {
			return nil, e
		}
		for i := range plan.Items {
			plan.Items[i].Manifest = nil
		}
		items = append(items, plan)
	}
	return map[string]any{"items": items, "limit": 100}, rows.Err()
}
func (a *App) restoreBrowserOperation(ctx context.Context, id string) (browserOperation, error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var plan browserOperation
	if e := a.Store.Get(ctx, "file_operation", id, &plan); e != nil {
		return plan, e
	}
	if plan.State == "restored" {
		return plan, nil
	}
	if plan.Action != "delete" || (plan.State != "completed" && plan.State != "partial" && plan.State != "executing") {
		return plan, bad("FILE_RESTORE_REJECTED", "该记录不是可恢复的删除操作")
	}
	root, e := a.browserRoot(plan.Root.ID, plan.Parent.Path)
	if e != nil || root != plan.Root {
		return plan, bad("FILE_ROOT_CHANGED", "允许目录已变化，不能恢复")
	}
	parent, e := importDir(plan.Parent.Path, false)
	if e != nil {
		return plan, e
	}
	defer parent.Close()
	info, e := parent.Stat()
	if e != nil {
		return plan, e
	}
	current, e := browserInfo(plan.Parent.Path, info)
	if e != nil || !sameBrowserIdentity(plan.Parent, current) {
		return plan, bad("FILE_PARENT_CHANGED", "原父目录已变化，不能自动恢复")
	}
	for _, item := range plan.Items {
		if item.Unrecoverable {
			return plan, bad("FILE_RESTORE_UNAVAILABLE", "存储插件已直接删除该文件，没有可恢复的本地副本")
		}
		if e = a.browserMutationAllowed(ctx, item.Source); e != nil {
			return plan, e
		}
		source, e := browserManifest(ctx, item.Source)
		if e == nil && sameRebasedManifest(item.Manifest, source, item.Source, item.Source) {
			if _, e = browserStat(item.Target); errors.Is(e, os.ErrNotExist) {
				continue
			}
		}
		if e == nil || !errors.Is(e, os.ErrNotExist) {
			return plan, bad("FILE_TARGET_EXISTS", "原路径已存在或变化，不会覆盖恢复")
		}
		quarantined, e := browserManifest(ctx, item.Target)
		if e != nil || !sameRebasedManifest(item.Manifest, quarantined, item.Source, item.Target) {
			return plan, bad("FILE_SNAPSHOT_CHANGED", "回收文件已变化，不能自动恢复")
		}
	}
	release, e := a.beginWrite()
	if e != nil {
		return plan, e
	}
	defer release()
	for i := range plan.Items {
		item := &plan.Items[i]
		if _, e = browserStat(item.Target); errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e = importRenameExclusive(int(parent.Fd()), filepath.Base(item.Target), filepath.Base(item.Source)); e != nil {
			return plan, bad("FILE_RESTORE_FAILED", "恢复未全部完成，请刷新核对")
		}
		item.Done = false
		_ = unix.Fsync(int(parent.Fd()))
		if e = a.Store.Put(ctx, "file_operation", plan.ID, plan); e != nil {
			return plan, e
		}
	}
	plan.State = "restored"
	e = a.Store.Put(ctx, "file_operation", plan.ID, plan)
	if e == nil {
		a.log("INFO", "files", "restored", "文件已从回收区恢复", "", "")
	}
	return plan, e
}
func (a *App) browserScrape(ctx context.Context, paths []string) (any, error) {
	if len(paths) == 0 || len(paths) > 100 {
		return nil, bad("INVALID_SELECTION", "请选择 1–100 个已登记文件或目录")
	}
	for _, path := range paths {
		allowed := a.sourceAllowed(path)
		for _, root := range a.Settings().Roots {
			allowed = allowed || (importPath(path) && within(root.Path, path))
		}
		if !allowed || browserInternal(path) {
			return nil, bad("INVALID_SOURCE", "选择超出允许目录")
		}
	}
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='asset'`)
	if e != nil {
		return nil, e
	}
	assets := []Asset{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return nil, e
		}
		var asset Asset
		if e = json.Unmarshal(b, &asset); e != nil {
			rows.Close()
			return nil, e
		}
		for _, path := range paths {
			if within(path, asset.Path) && asset.State != "removed" {
				assets = append(assets, asset)
				break
			}
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if len(assets) == 0 {
		return nil, bad("FILE_MEDIA_UNIDENTIFIED", "未找到已登记媒体，请先识别整理，或在媒体库登记外部文件后刮削")
	}
	if len(assets) > 200 {
		return nil, bad("FILE_SELECTION_LIMIT", "一次最多刮削 200 个媒体文件，请缩小选择范围")
	}
	jobs := []Job{}
	for _, asset := range assets {
		existing, findErr := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused') ORDER BY created_at DESC LIMIT 1`, asset.ID))
		if findErr == nil {
			jobs = append(jobs, existing)
			continue
		}
		j, e := a.enqueue(ctx, "scrape", map[string]string{"id": asset.ID}, id("wf_"))
		if e != nil {
			return map[string]any{"jobs": jobs}, e
		}
		jobs = append(jobs, j)
	}
	return map[string]any{"jobs": jobs}, nil
}
