package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	ab "light-media/internal/plugins/autobackup"
)

// This adapter deliberately uses the actual App layout. The frozen MoviePilot
// raw-user.db archive oracle remains in the pure package, not here.
func autoBackupFilePin(path string) (autoBackupPin, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return autoBackupPin{}, e
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return autoBackupPin{}, errors.New("backup symlink denied")
	}
	return autoBackupInfoPin(st)
}
func autoBackupInfoPin(st os.FileInfo) (autoBackupPin, error) {
	v, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return autoBackupPin{}, errors.New("unsupported backup file identity")
	}
	return autoBackupPin{Dev: uint64(v.Dev), Ino: uint64(v.Ino), Mode: uint32(st.Mode()), Size: st.Size(), Mod: st.ModTime().UnixNano()}, nil
}
func autoBackupHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}
func autoBackupAuthorize(path string, roots []string) error {
	p, e := filepath.Abs(path)
	if e != nil || p != filepath.Clean(path) {
		return errors.New("absolute backup path required")
	}
	grant := ""
	for _, root := range roots {
		root = filepath.Clean(root)
		rel, e := filepath.Rel(root, p)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			grant = root
			break
		}
	}
	if grant == "" {
		return errors.New("backup path outside configured roots")
	}
	// Only the grant and its descendants are untrusted path input. macOS /var
	// is a platform symlink above the owned t.TempDir, not a supplied child.
	current := p
	for {
		st, e := os.Lstat(current)
		if e == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				return errors.New("backup path symlink denied")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if current == grant {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			return errors.New("invalid backup grant")
		}
		current = parent
	}
	return nil
}
func (c *autoBackupCall) archive(ctx context.Context, dest string) (*string, error) {
	if err := autoBackupAuthorize(c.defaultPath(), []string{c.o.a.Dir}); err != nil {
		return nil, err
	}
	if err := autoBackupAuthorize(dest, c.writeRoots()); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		return nil, err
	}
	if err := autoBackupAuthorize(dest, c.writeRoots()); err != nil {
		return nil, err
	}
	output, err := os.OpenRoot(dest)
	if err != nil {
		return nil, err
	}
	defer output.Close()
	stageBase := filepath.Join(c.o.a.Dir, "plugin-private", c.o.instance, "staging")
	if err = autoBackupAuthorize(stageBase, []string{c.o.a.Dir}); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(stageBase, 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(stageBase, "owned-"+c.run.ID+"-")
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(stage, 0700); err != nil {
		return nil, err
	}
	// Only owned temporary data is removed. Long ZIP/network work begins after the
	// consistent native SQLite snapshot has released the database connection.
	defer os.RemoveAll(stage)
	snapshot, err := c.effect(ctx, "sqlite_snapshot", map[string]string{"layout": "light-media.db", "staging": stage}, func() (any, error) {
		name, e := c.o.a.Store.Backup(ctx, stage)
		if e != nil {
			return nil, e
		}
		if e = c.snapshotCut(ctx, filepath.Join(stage, name)); e != nil {
			return nil, e
		}
		hash, e := autoBackupHash(filepath.Join(stage, name))
		if e != nil {
			return nil, e
		}
		return map[string]string{"name": name, "sha256": hash}, nil
	})
	if err != nil {
		return nil, err
	}
	dbName := snapshot.(map[string]string)["name"]
	if _, err = c.effect(ctx, "restore_key_copy", map[string]string{"name": "secret.key", "staging": stage}, func() (any, error) {
		path := filepath.Join(stage, "secret.key")
		if e := os.WriteFile(path, append([]byte(nil), c.o.a.Store.key...), 0600); e != nil {
			return nil, e
		}
		pin, e := autoBackupFilePin(path)
		return pin, e
	}); err != nil {
		return nil, err
	}
	category, err := c.copyCategory(ctx, stage)
	if err != nil {
		return nil, err
	}
	name := "bk_" + c.now().Format("20060102150405") + ".zip"
	temporary := ".autobackup-" + c.run.ID + ".zip.tmp"
	_, err = c.effect(ctx, "archive_write", map[string]string{"directory": dest, "filename": name, "temporary": temporary, "layout": "light-media-v1"}, func() (any, error) {
		f, e := output.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		complete := false
		defer func() {
			_ = f.Close()
			if !complete {
				_ = output.Remove(temporary)
			}
		}()
		z := zip.NewWriter(f)
		sources := []struct{ disk, name string }{{filepath.Join(stage, dbName), "light-media.db"}, {filepath.Join(stage, "secret.key"), "secret.key"}}
		if category {
			sources = append(sources, struct{ disk, name string }{filepath.Join(stage, "category.yaml"), "category.yaml"})
		}
		for _, source := range sources {
			if e = ctx.Err(); e != nil {
				_ = z.Close()
				return nil, e
			}
			input, e := os.Open(source.disk)
			if e != nil {
				_ = z.Close()
				return nil, e
			}
			header := &zip.FileHeader{Name: source.name, Method: zip.Deflate}
			header.SetMode(0600)
			header.SetModTime(c.now())
			writer, e := z.CreateHeader(header)
			if e == nil {
				_, e = io.Copy(writer, autoBackupContextReader{ctx: ctx, r: input})
			}
			_ = input.Close()
			if e != nil {
				_ = z.Close()
				return nil, e
			}
		}
		if e = z.Close(); e != nil {
			return nil, e
		}
		if e = f.Sync(); e != nil {
			return nil, e
		}
		if e = f.Close(); e != nil {
			return nil, e
		}
		if e = output.Rename(temporary, name); e != nil {
			return nil, e
		}
		complete = true
		path := filepath.Join(dest, name)
		pin, e := autoBackupFilePin(path)
		if e != nil {
			return nil, e
		}
		hash, e := autoBackupHash(path)
		if e != nil {
			return nil, e
		}
		return map[string]any{"pin": pin, "sha256": hash, "bytes": pin.Size}, nil
	})
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dest, name)
	return &path, nil
}
func (c *autoBackupCall) remove(ctx context.Context, path string, tree bool) error {
	pin, ok := c.pins[path]
	if !ok {
		return errors.New("backup removal requires original listing identity")
	}
	if !strings.HasPrefix(filepath.Base(path), "bk") {
		return errors.New("outside backup entry grant")
	}
	if err := autoBackupAuthorize(path, c.writeRoots()); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(path)
	st, err := root.Lstat(name)
	if err != nil {
		return err
	}
	current, err := autoBackupInfoPin(st)
	if err != nil || current != pin {
		return errors.New("backup entry changed since listing")
	}
	manifest := map[string]autoBackupPin{name: pin}
	if tree {
		err = fs.WalkDir(root.FS(), name, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			st, e := root.Lstat(p)
			if e != nil {
				return e
			}
			v, e := autoBackupInfoPin(st)
			if e != nil {
				return e
			}
			manifest[p] = v
			return nil
		})
		if err != nil {
			return err
		}
	}
	stage := ".auto-backup-retention-" + id("")
	_, err = c.effect(ctx, "local_retention", map[string]any{"path": path, "pin": pin, "tree": tree, "stage": stage, "manifest": manifest}, func() (any, error) {
		parent, e := root.Open(".")
		if e != nil {
			return nil, e
		}
		defer parent.Close()
		if e = autoBackupRenameNoReplace(int(parent.Fd()), name, stage); e != nil {
			return nil, e
		}
		moved, e := root.Lstat(stage)
		if e != nil {
			return nil, e
		}
		movedPin, e := autoBackupInfoPin(moved)
		if e != nil || movedPin != pin {
			_ = autoBackupRenameNoReplace(int(parent.Fd()), stage, name)
			return nil, errors.New("backup retention target changed before capture")
		}
		if !tree {
			return map[string]bool{"removed": true}, root.Remove(stage)
		}
		e = c.removeTreePinned(ctx, root, stage, name, manifest)
		return map[string]bool{"removed": e == nil}, e
	})
	return err
}
func (c *autoBackupCall) removeTreePinned(ctx context.Context, root *os.Root, actual, original string, manifest map[string]autoBackupPin) error {
	expected, ok := manifest[original]
	if !ok {
		return errors.New("unlisted backup retention leaf")
	}
	if hook := c.o.a.pluginAutoBackup.beforeRetentionLeaf; hook != nil {
		hook(original)
	}
	st, err := root.Lstat(actual)
	if err != nil {
		return err
	}
	pin, err := autoBackupInfoPin(st)
	// Directory metadata changes as its pinned children are removed. Every
	// non-directory leaf must still match the original manifest in full,
	// including size and mtime, before a new capture pin can be established.
	if err != nil || pin.Dev != expected.Dev || pin.Ino != expected.Ino || pin.Mode != expected.Mode || (!st.IsDir() && pin != expected) {
		return errors.New("backup retention leaf changed")
	}
	if st.IsDir() {
		directory, err := root.Open(actual)
		if err != nil {
			return err
		}
		entries, err := directory.ReadDir(-1)
		_ = directory.Close()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = c.removeTreePinned(ctx, root, filepath.Join(actual, entry.Name()), filepath.Join(original, entry.Name()), manifest); err != nil {
				return err
			}
		}
	}
	parent, err := root.Open(filepath.Dir(actual))
	if err != nil {
		return err
	}
	defer parent.Close()
	stage := ".auto-backup-leaf-" + id("")
	capture := filepath.Join(filepath.Dir(actual), stage)
	_, err = c.effect(ctx, "local_retention_step", map[string]any{"from": actual, "stage": capture, "pin": pin}, func() (any, error) {
		if e := autoBackupRenameNoReplace(int(parent.Fd()), filepath.Base(actual), stage); e != nil {
			return nil, e
		}
		st, e := root.Lstat(capture)
		if e != nil {
			return nil, e
		}
		got, e := autoBackupInfoPin(st)
		if e != nil || got.Dev != pin.Dev || got.Ino != pin.Ino || got.Mode != pin.Mode || (!st.IsDir() && got != pin) {
			_ = autoBackupRenameNoReplace(int(parent.Fd()), stage, filepath.Base(actual))
			return nil, errors.New("backup retention leaf replaced")
		}
		if e = root.Remove(capture); e != nil {
			return nil, e
		}
		return map[string]bool{"removed": true}, nil
	})
	return err
}

type autoBackupDAV struct {
	o      *pluginAutoBackupOwner
	native ab.DAV
}

func (d *autoBackupDAV) List(ctx context.Context, path string) ([]string, error) {
	return d.native.List(ctx, path)
}
func (d *autoBackupDAV) Check(ctx context.Context, path string) (bool, error) {
	return d.native.Check(ctx, path)
}
func (d *autoBackupDAV) Upload(ctx context.Context, remote, local string, callback func() error) error {
	// Upload receipt confirms the HTTP write separately from the source's later
	// verification callback. A lost response is uncertain and never replayed.
	_, e := d.o.active.effect(ctx, "webdav_put", map[string]string{"remote": remote, "local": local}, func() (any, error) {
		err := d.native.Upload(ctx, remote, local, func() error { return nil })
		return map[string]bool{"accepted": err == nil}, err
	})
	if e != nil {
		return e
	}
	if callback != nil {
		return callback()
	}
	return nil
}
func (d *autoBackupDAV) Delete(ctx context.Context, remote string) error {
	_, e := d.o.active.effect(ctx, "webdav_delete", map[string]string{"remote": remote}, func() (any, error) {
		err := d.native.Delete(ctx, remote)
		return map[string]bool{"accepted": err == nil}, err
	})
	return e
}

func (c *autoBackupCall) copyCategory(ctx context.Context, stage string) (bool, error) {
	root, err := os.OpenRoot(c.o.a.Dir)
	if err != nil {
		return false, err
	}
	defer root.Close()
	st, err := root.Lstat("category.yaml")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, errors.New("backup category config is not a regular file")
	}
	pin, err := autoBackupInfoPin(st)
	if err != nil {
		return false, err
	}
	_, err = c.effect(ctx, "category_config_copy", map[string]any{"name": "category.yaml", "pin": pin}, func() (any, error) {
		in, err := root.Open("category.yaml")
		if err != nil {
			return nil, err
		}
		defer in.Close()
		actual, err := in.Stat()
		if err != nil {
			return nil, err
		}
		got, err := autoBackupInfoPin(actual)
		if err != nil || got != pin {
			return nil, errors.New("backup category config identity changed")
		}
		out, err := os.OpenFile(filepath.Join(stage, "category.yaml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(out, autoBackupContextReader{ctx: ctx, r: in})
		if err == nil {
			err = out.Sync()
		}
		_ = out.Close()
		if err != nil {
			return nil, err
		}
		hash, err := autoBackupHash(filepath.Join(stage, "category.yaml"))
		return map[string]string{"sha256": hash}, err
	})
	return err == nil, err
}

type autoBackupContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r autoBackupContextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
