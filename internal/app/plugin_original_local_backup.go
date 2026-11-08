package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"light-media/internal/plugins"
)

type originalLocalBackupAPIKey struct{}

func originalLocalBackupScope(ctx context.Context, scope originalBaseCallScope) bool {
	object := scope.object
	if object == nil || object.installation.PluginID != "AutoBackup" || scope.localBackup == nil {
		return false
	}
	switch scope.phase {
	case "private_once":
		return object.originalPrivateSchedulerScopeAllowed(ctx)
	case "backup":
		service, _ := ctx.Value(originalServiceCallKey{}).(*originalServiceCall)
		return service != nil && service.job != nil && service.job.object == object &&
			service.job.definition.Handler == scope.phase && object.originalPublicServiceScopeAllowed(ctx)
	case "http":
		owner, _ := ctx.Value(originalLocalBackupAPIKey{}).(*originalPluginInstance)
		lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
		return owner == object && lease != nil && lease.sourceKind == originalSourceAPI && object.originalPublicServiceScopeAllowed(ctx)
	}
	return false
}

func (a *App) originalLocalBackupResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	if !originalLocalBackupScope(ctx, scope) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "本地备份须绑定实际自动备份任务或接口")
	}
	var request plugins.LocalBackupRequestV1
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if err := originalLocalBackupConfig(scope.object, request); err != nil {
		return nil, err
	}
	if !scope.localBackup.CompareAndSwap(false, true) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "同一次自动备份调用只能执行一次")
	}
	a.originalLocalBackupMu.Lock()
	defer a.originalLocalBackupMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	release, err := a.beginWrite()
	if err != nil {
		return nil, err
	}
	defer release()
	dataDir, err := filepath.Abs(a.Dir)
	if err != nil {
		return nil, err
	}
	defaultPath := filepath.Join(dataDir, "plugin-data", scope.installation.InstanceID)
	destination := request.Path
	if destination == "" {
		if _, err = a.originalBaseDataPath(ctx, scope.installation.InstanceID); err != nil {
			return nil, err
		}
		destination = defaultPath
	}
	if filepath.IsAbs(destination) {
		destination = filepath.Clean(destination)
	}
	if err = a.originalLocalBackupAllowed(dataDir, defaultPath, destination); err != nil {
		return nil, err
	}
	result := plugins.LocalBackupResultV1{SchemaVersion: 1, DeleteCount: "0", RemainingCount: "0"}
	root, err := originalLocalBackupOpenDir(destination)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) || errors.Is(err, syscall.EIO) {
			return json.Marshal(result)
		}
		return nil, err
	}
	defer root.Close()
	guard := func() error {
		if err := scope.validate(ctx); err != nil {
			return err
		}
		if err := a.originalLocalBackupAllowed(dataDir, defaultPath, destination); err != nil {
			return err
		}
		return originalLocalBackupCheckRoot(root, destination)
	}
	if err = guard(); err != nil {
		return nil, err
	}
	at := time.Now()
	filename := "bk_" + at.Format("20060102150405") + ".zip"
	if err = a.originalLocalBackupArchive(ctx, dataDir, root, at, func(temporary string) error {
		return a.originalLocalBackupPublish(ctx, root, destination, defaultPath, temporary, filename, guard)
	}); err == nil {
		result.ArchiveCreated = true
		result.ArchivePath = filepath.Join(destination, filename)
	} else {
		var boundary *APIError
		if errors.As(err, &boundary) {
			return nil, err
		}
		if err = guard(); err != nil {
			return nil, err
		}
	}
	if err = a.originalLocalBackupRotate(ctx, root, destination, defaultPath, request.Retain, &result, guard); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func originalLocalBackupConfig(object *originalPluginInstance, request plugins.LocalBackupRequestV1) error {
	object.configMu.RLock()
	raw := copyOriginalBaseJSON(object.appliedConfig)
	object.configMu.RUnlock()
	var config map[string]json.RawMessage
	if len(raw) != 0 && json.Unmarshal(raw, &config) != nil {
		return bad("INVALID_PLUGIN_CONFIG", "自动备份运行配置无效")
	}
	path := ""
	if value := config["back_path"]; originalBaseJSONTruthy(value) && json.Unmarshal(value, &path) != nil {
		return bad("INVALID_PLUGIN_CONFIG", "自动备份路径须为字符串")
	}
	configured := config["cnt"]
	if !originalBaseJSONTruthy(configured) {
		configured = json.RawMessage(`null`)
	}
	requested := request.Retain
	if !originalBaseJSONTruthy(requested) {
		requested = json.RawMessage(`null`)
	}
	if path != request.Path || !originalBaseJSONEqual(configured, requested) {
		return bad("PLUGIN_CAPABILITY_DENIED", "本地备份参数与运行配置不符")
	}
	return nil
}

func (a *App) originalLocalBackupAllowed(dataDir, defaultPath, path string) error {
	if !importPath(path) || safeRoot(path) != nil {
		return bad("INVALID_BACKUP_PATH", "备份目录须为有效的绝对路径")
	}
	if within(defaultPath, path) {
		return nil
	}
	if !within(dataDir, path) {
		for _, root := range a.Settings().AllowedRoots {
			if within(root, path) {
				return nil
			}
		}
	}
	return bad("PLUGIN_CAPABILITY_DENIED", "备份目录不在当前实例或已授权的本地目录内")
}

func originalLocalBackupOpenDir(path string) (*os.Root, error) {
	// importDir opens and, if needed, creates every path component using
	// descriptor-relative O_NOFOLLOW. Compare the pinned descriptor to os.Root.
	directory, err := importDir(path, true)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	if err = originalLocalBackupMatchRoot(root, directory); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func originalLocalBackupCheckRoot(root *os.Root, path string) error {
	directory, err := importDir(path, false)
	if err != nil {
		return err
	}
	defer directory.Close()
	return originalLocalBackupMatchRoot(root, directory)
}

func originalLocalBackupMatchRoot(root *os.Root, directory *os.File) error {
	bound, err := directory.Stat()
	if err != nil {
		return err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(bound, actual) {
		return bad("FILE_PARENT_CHANGED", "备份目录已变化")
	}
	return nil
}

func (a *App) originalLocalBackupArchive(ctx context.Context, dataDir string, root *os.Root, at time.Time, publish func(string) error) error {
	stage, err := os.MkdirTemp(dataDir, ".autobackup-snapshot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	snapshot, err := a.Store.Backup(ctx, stage)
	if err != nil {
		return err
	}
	database, err := importOpen(filepath.Join(stage, snapshot))
	if err != nil {
		return err
	}
	defer database.Close()
	category, err := importOpen(filepath.Join(dataDir, "category.yaml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if category != nil {
		defer category.Close()
	}
	temporary := ".autobackup-" + id("") + ".zip.tmp"
	output, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		_ = output.Close()
		_ = root.Remove(temporary)
	}()
	archive := zip.NewWriter(output)
	write := func(name string, input io.Reader) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: at}
		header.SetMode(0600)
		entry, err := archive.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(entry, originalLocalBackupReader{ctx: ctx, reader: input})
		}
		return err
	}
	if err = write("light-media.db", database); err == nil {
		err = write("secret.key", bytes.NewReader(a.Store.key))
	}
	if err == nil && category != nil {
		err = write("category.yaml", category)
	}
	closeErr := archive.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	return publish(temporary)
}

func (a *App) originalLocalBackupPublish(ctx context.Context, root *os.Root, destination, defaultPath, temporary, name string, guard func() error) error {
	external := !within(defaultPath, destination)
	if external {
		a.importMu.Lock()
		defer a.importMu.Unlock()
	}
	if err := guard(); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		directory, err := root.Open(".")
		if err != nil {
			return err
		}
		defer directory.Close()
		return importRenameExclusive(int(directory.Fd()), temporary, name)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return bad("FILE_PROTECTED", "备份文件路径已被其他类型条目占用")
	}
	if external {
		if err := a.browserMutationAllowed(ctx, filepath.Join(destination, name)); err != nil {
			return err
		}
	}
	actual, err := root.Lstat(name)
	if err != nil || !os.SameFile(info, actual) || info.Mode() != actual.Mode() || info.Size() != actual.Size() || !info.ModTime().Equal(actual.ModTime()) {
		return bad("FILE_SNAPSHOT_CHANGED", "原有备份文件在覆盖前发生变化")
	}
	if err = guard(); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}

type originalLocalBackupReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r originalLocalBackupReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
