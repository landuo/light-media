package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"light-media/internal/plugins"
	"os"
	"strings"
)

// PackageResource owns an already verified open file. The descriptor pins its
// inode across an update, so callers can stream bytes without holding a global
// installation lock during a slow network response. No host path is exported.
// Config, KV and instance data live outside this verified package tree.
type PackageResource struct {
	Reader io.ReadCloser
	Name   string
	Size   int64
	SHA256 string
}

func OpenPackageResource(root, codeID, relative string, validate func(plugins.Manifest, string) error) (PackageResource, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	if relative == "" || relative == "." {
		return PackageResource{}, os.ErrPermission
	}
	if !safeName(relative) || strings.HasSuffix(relative, "/") || relative == ".acquired.json" {
		return PackageResource{}, os.ErrNotExist
	}
	installed, err := readInstalled(root, codeID, validate)
	if err != nil {
		return PackageResource{}, err
	}
	files := installed.Files
	if len(files) == 0 {
		info, err := os.Stat(installed.ExecutablePath)
		if err != nil {
			return PackageResource{}, err
		}
		files = []PackageFile{{Path: installed.Manifest.Entry, Size: info.Size(), SHA256: installed.SHA256}}
	}
	for _, member := range files {
		if strings.HasPrefix(member.Path, relative+"/") {
			return PackageResource{}, os.ErrPermission
		}
		if member.Path != relative {
			continue
		}
		file, err := openPackageFile(installed.Command.Dir, member)
		if err != nil {
			return PackageResource{}, err
		}
		sum := sha256.New()
		size, err := io.Copy(sum, io.LimitReader(file, member.Size+1))
		if err != nil || size != member.Size || hex.EncodeToString(sum.Sum(nil)) != member.SHA256 {
			file.Close()
			return PackageResource{}, errors.New("package resource digest or size mismatch")
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			file.Close()
			return PackageResource{}, err
		}
		return PackageResource{Reader: file, Name: member.Path, Size: member.Size, SHA256: member.SHA256}, nil
	}
	return PackageResource{}, os.ErrNotExist
}
