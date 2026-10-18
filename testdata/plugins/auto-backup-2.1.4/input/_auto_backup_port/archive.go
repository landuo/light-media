package autobackup

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DatabaseDumper interface {
	Dump(context.Context, string) error
}
type ArchiveOptions struct {
	ConfigDir    string
	ReadRoots    []string
	WriteRoots   []string
	DatabaseType string
	Postgres     DatabaseDumper
	Now          func() time.Time
	Log          func(context.Context, string, string)
}
type Archive struct{ options ArchiveOptions }

func NewArchive(o ArchiveOptions) (*Archive, error) {
	if !filepath.IsAbs(o.ConfigDir) || len(o.WriteRoots) == 0 {
		return nil, errors.New("unbound backup filesystem")
	}
	o.ConfigDir = filepath.Clean(o.ConfigDir)
	if len(o.ReadRoots) == 0 {
		o.ReadRoots = []string{o.ConfigDir}
	}
	for _, roots := range [][]string{o.ReadRoots, o.WriteRoots} {
		for _, r := range roots {
			if !filepath.IsAbs(r) {
				return nil, errors.New("relative backup grant")
			}
		}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Archive{o}, nil
}
func contained(p, root string) bool {
	rel, e := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func canonicalBoundPath(p string) (string, error) {
	original := filepath.Clean(p)
	q := original
	for {
		resolved, e := filepath.EvalSymlinks(q)
		if e == nil {
			tail, e := filepath.Rel(q, original)
			if e != nil {
				return "", e
			}
			return filepath.Join(resolved, tail), nil
		}
		if !errors.Is(e, fs.ErrNotExist) {
			return "", e
		}
		next := filepath.Dir(q)
		if next == q {
			return original, nil
		}
		q = next
	}
}
func authorize(p string, roots []string) error {
	if !filepath.IsAbs(p) {
		return errors.New("unbound backup path")
	}
	canonical, e := canonicalBoundPath(p)
	if e != nil {
		return e
	}
	for _, r := range roots {
		rr, e := canonicalBoundPath(r)
		if e != nil {
			return e
		}
		if contained(canonical, rr) {
			return nil
		}
	}
	return errors.New("backup path outside bound host grants")
}

func (a *Archive) BackupFile(ctx context.Context, dest string) (*string, error) {
	if e := authorize(dest, a.options.WriteRoots); e != nil {
		return nil, e
	}
	staging := filepath.Join(dest, "bk_"+a.options.Now().Format("20060102150405"))
	zipPath := staging + ".zip"
	if e := authorize(staging, a.options.WriteRoots); e != nil {
		return nil, e
	}
	if e := authorize(zipPath, a.options.WriteRoots); e != nil {
		return nil, e
	}
	err := a.backup(ctx, staging, zipPath)
	if err != nil {
		var p *fs.PathError
		var l *os.LinkError
		var known *DumpError
		if errors.As(err, &p) || errors.As(err, &l) || errors.As(err, &known) {
			if a.options.Log != nil {
				a.options.Log(ctx, "error", "创建备份失败: "+err.Error())
			}
			return nil, nil
		}
		return nil, err
	}
	return &zipPath, nil
}
func (a *Archive) backup(ctx context.Context, staging, zipPath string) error {
	if _, e := os.Stat(staging); errors.Is(e, fs.ErrNotExist) {
		if e = os.MkdirAll(staging, 0755); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	copyIf := func(name string) error {
		src := filepath.Join(a.options.ConfigDir, name)
		if e := authorize(src, a.options.ReadRoots); e != nil {
			return e
		}
		if _, e := os.Stat(src); errors.Is(e, fs.ErrNotExist) {
			return nil
		} else if e != nil {
			return e
		}
		return a.copy(ctx, src, filepath.Join(staging, name))
	}
	if e := copyIf("category.yaml"); e != nil {
		return e
	}
	if a.options.DatabaseType == "sqlite" {
		entries, e := os.ReadDir(a.options.ConfigDir)
		if e != nil && !errors.Is(e, fs.ErrNotExist) {
			return e
		}
		for _, f := range entries {
			if strings.HasPrefix(f.Name(), "user.db") {
				if e = copyIf(f.Name()); e != nil {
					return e
				}
			}
		}
	}
	if a.options.DatabaseType == "postgresql" {
		if a.options.Postgres == nil {
			return &DumpError{Kind: "client_unavailable"}
		}
		if e := a.options.Postgres.Dump(ctx, filepath.Join(staging, "postgresql_backup.sql")); e != nil {
			if a.options.Log != nil {
				a.options.Log(ctx, "error", "PostgreSQL数据库备份失败: "+e.Error())
			}
			return e
		}
		if a.options.Log != nil {
			a.options.Log(ctx, "info", "PostgreSQL数据库备份成功: "+filepath.Join(staging, "postgresql_backup.sql"))
		}
	}
	if e := copyIf("app.env"); e != nil {
		return e
	}
	cookieSource := filepath.Join(a.options.ConfigDir, "cookies")
	if e := authorize(cookieSource, a.options.ReadRoots); e != nil {
		return e
	}
	if _, e := os.Stat(cookieSource); e == nil {
		if e = a.copyTree(ctx, cookieSource, filepath.Join(staging, "cookies")); e != nil {
			return e
		}
	} else if !errors.Is(e, fs.ErrNotExist) {
		return e
	}
	out, e := os.Create(zipPath)
	if e != nil {
		return e
	}
	z := zip.NewWriter(out)
	err := filepath.WalkDir(staging, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if p == staging {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(staging, p)
		if e != nil {
			return e
		}
		header, e := zip.FileInfoHeader(info)
		if e != nil {
			return e
		}
		header.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		w, e := z.CreateHeader(header)
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		_, e = io.Copy(w, f)
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	})
	ze, oe := z.Close(), out.Close()
	if err != nil {
		return err
	}
	if ze != nil {
		return ze
	}
	if oe != nil {
		return oe
	}
	return os.RemoveAll(staging)
}
func (a *Archive) copy(ctx context.Context, src, dest string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := authorize(src, a.options.ReadRoots); e != nil {
		return e
	}
	if e := authorize(dest, a.options.WriteRoots); e != nil {
		return e
	}
	info, e := os.Stat(src)
	if e != nil {
		return e
	}
	if info.IsDir() {
		return &fs.PathError{Op: "copy", Path: src, Err: errors.New("is a directory")}
	}
	f, e := os.Open(src)
	if e != nil {
		return e
	}
	defer f.Close()
	o, e := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if e != nil {
		return e
	}
	_, e = io.Copy(o, f)
	ce := o.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Chmod(dest, info.Mode().Perm())
}
func (a *Archive) copyTree(ctx context.Context, src, dest string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := authorize(src, a.options.ReadRoots); e != nil {
		return e
	}
	if e := authorize(dest, a.options.WriteRoots); e != nil {
		return e
	}
	info, e := os.Stat(src)
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "scandir", Path: src, Err: errors.New("not a directory")}
	}
	if e = os.Mkdir(dest, info.Mode().Perm()); e != nil {
		return e
	}
	items, e := os.ReadDir(src)
	if e != nil {
		return e
	}
	for _, item := range items {
		from, to := filepath.Join(src, item.Name()), filepath.Join(dest, item.Name())
		st, e := os.Stat(from)
		if e != nil {
			return e
		}
		if st.IsDir() {
			e = a.copyTree(ctx, from, to)
		} else {
			e = a.copy(ctx, from, to)
		}
		if e != nil {
			return e
		}
	}
	return os.Chtimes(dest, info.ModTime(), info.ModTime())
}
