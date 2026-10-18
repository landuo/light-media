package cloudflarespeedtest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrArtifactUnverified = errors.New("CloudflareSpeedTest artifact provenance has not been approved")
var ErrStorageScope = errors.New("CloudflareSpeedTest storage scope invalid")

// OwnedFiles is a fresh trusted host directory, never a path supplied by the
// SDK worker. The marker prevents adopting an existing unowned data directory.
// Cross-process serialization and lifecycle authority still belong to the host.
type OwnedFiles struct{ root, owner string }

func NewOwnedFiles(root, owner string) (*OwnedFiles, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) == string(filepath.Separator) || owner == "" || len(owner) > 128 || strings.ContainsAny(owner, "\x00\r\n/") {
		return nil, ErrStorageScope
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStorageScope
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, ErrStorageScope
	}
	// macOS /var is itself a system symlink. Canonicalize trusted ancestors
	// once; the actual owned directory must not be a symlink and every later
	// access verifies this canonical root has not been replaced.
	root = resolved
	f := &OwnedFiles{root: root, owner: owner}
	marker := filepath.Join(root, ".cloudflare-owner")
	data, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		entries, e := os.ReadDir(root)
		if e != nil {
			return nil, e
		}
		if len(entries) != 0 {
			return nil, ErrStorageScope
		}
		file, e := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, e = file.WriteString(owner)
		closeErr := file.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if err != nil {
		return nil, err
	} else if string(data) != owner {
		return nil, ErrStorageScope
	}
	return f, nil
}
func (f *OwnedFiles) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(f.root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrStorageScope
	}
	resolved, err := filepath.EvalSymlinks(f.root)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(f.root) {
		return ErrStorageScope
	}
	marker := filepath.Join(f.root, ".cloudflare-owner")
	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return ErrStorageScope
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != f.owner {
		return ErrStorageScope
	}
	return nil
}
func (f *OwnedFiles) file(name string) (string, error) {
	if name == "" || name == ".cloudflare-owner" || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return "", ErrStorageScope
	}
	return filepath.Join(f.root, name), nil
}
func (f *OwnedFiles) Exists(ctx context.Context, ref FileRef, p Platform) (bool, error) {
	if err := f.check(ctx); err != nil {
		return false, err
	}
	if ref.Backslash && p.OS != "windows" {
		return false, nil
	}
	name, err := f.file(ref.Name)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, ErrStorageScope
	}
	return true, nil
}
func (f *OwnedFiles) Ensure(ctx context.Context) error { return f.check(ctx) }
func (f *OwnedFiles) Reset(ctx context.Context) error {
	if err := f.check(ctx); err != nil {
		return err
	}
	entries, err := os.ReadDir(f.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".cloudflare-owner" {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = os.RemoveAll(filepath.Join(f.root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
func (f *OwnedFiles) Remove(ctx context.Context, name string) error {
	if err := f.check(ctx); err != nil {
		return err
	}
	p, err := f.file(name)
	if err != nil {
		return err
	}
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrStorageScope
	}
	return os.Remove(p)
}
func (f *OwnedFiles) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	if err := f.check(ctx); err != nil {
		return nil, err
	}
	p, err := f.file(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrStorageScope
	}
	file, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrStorageScope
	}
	return data, nil
}
func (f *OwnedFiles) ReadResult(ctx context.Context) ([]byte, error) {
	data, err := f.read(ctx, "result_hosts.txt", 16<<20)
	// The old sed/PowerShell helper returns an empty line on missing output.
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}
func (f *OwnedFiles) write(ctx context.Context, name string, data []byte, mode os.FileMode) error {
	if err := f.check(ctx); err != nil {
		return err
	}
	target, err := f.file(name)
	if err != nil {
		return err
	}
	if info, e := os.Lstat(target); e == nil && !info.Mode().IsRegular() {
		return ErrStorageScope
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	temp, err := os.CreateTemp(f.root, ".stage-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err = temp.Chmod(mode); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = f.check(ctx); err != nil {
		return err
	}
	return os.Rename(tempName, target)
}

// Pins are provisioned by the trusted host/release catalog. A hash downloaded
// alongside an untrusted artifact is not an approval. No source binary is run
// or implicitly adopted by constructing the adapter.
type Pins struct {
	Archives map[string][32]byte
	Binaries map[string][][32]byte
}

func (p Pins) archive(req InstallRequest, data []byte) error {
	want, ok := p.Archives[req.URL]
	if !ok || sha256.Sum256(data) != want {
		return ErrArtifactUnverified
	}
	return nil
}
func (p Pins) binary(name string, data []byte) error {
	got := sha256.Sum256(data)
	for _, want := range p.Binaries[name] {
		if got == want {
			return nil
		}
	}
	return ErrArtifactUnverified
}

// extractArchive stages and validates all members before any result is
// published. Links, traversal and special files cannot escape an owned root.
func extractArchive(ctx context.Context, f *OwnedFiles, req InstallRequest, data []byte) error {
	type member struct {
		name string
		data []byte
		mode os.FileMode
	}
	var members []member
	total := int64(0)
	seen := map[string]bool{}
	add := func(name string, mode os.FileMode, r io.Reader, size int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean := path.Clean(name)
		if clean != name || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return ErrStorageScope
		}
		// Official archives put their files at their root. Directories are
		// ignored; nested members are rejected rather than silently flattened.
		if strings.Contains(name, "/") || name == ".cloudflare-owner" || seen[name] || len(members) >= 256 || size < 0 || size > 128<<20 || total+size > 128<<20 {
			return ErrStorageScope
		}
		if !mode.IsRegular() {
			return ErrStorageScope
		}
		b, e := io.ReadAll(io.LimitReader(r, size+1))
		if e != nil {
			return &ExtractionError{}
		}
		if int64(len(b)) != size {
			return &ExtractionError{}
		}
		seen[name] = true
		total += size
		members = append(members, member{name, b, mode.Perm()})
		return nil
	}
	if strings.HasSuffix(req.Archive, ".zip") {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return &ExtractionError{}
		}
		for _, entry := range z.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			r, err := entry.Open()
			if err != nil {
				return &ExtractionError{}
			}
			err = add(entry.Name, entry.Mode(), r, int64(entry.UncompressedSize64))
			closeErr := r.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return &ExtractionError{}
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return &ExtractionError{}
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return &ExtractionError{}
			}
			if h.Typeflag == tar.TypeDir {
				continue
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
				return ErrStorageScope
			}
			if err = add(h.Name, os.FileMode(h.Mode), tr, h.Size); err != nil {
				return err
			}
		}
	}
	for _, entry := range members {
		if err := f.write(ctx, entry.name, entry.data, entry.mode&0700|0600); err != nil {
			return fmt.Errorf("publish CloudflareSpeedTest archive: %w", err)
		}
	}
	return nil
}
