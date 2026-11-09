package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"light-media/internal/plugins"
)

// PackageFile describes exact regular-file bytes, not a filesystem permission.
// For frontend packages the list covers every extracted file, including worker.
type PackageFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type installedReceipt struct {
	Manifest           plugins.Manifest         `json:"manifest"`
	SHA256             string                   `json:"sha256"`
	HostContract       string                   `json:"host_contract"`
	RepositoryURL      string                   `json:"repository_url,omitempty"`
	Files              []PackageFile            `json:"files,omitempty"`
	PackageSHA256      string                   `json:"package_sha256,omitempty"`
	FrontendDerivation *frontendClassDerivation `json:"frontend_derivation,omitempty"`
}

// FrontendAsset contains already verified bounded bytes. No file handle or
// package path escapes the operation lock to be opened after an upgrade.
type FrontendAsset struct {
	Data        []byte
	SHA256      string
	ContentType string
}

func validatePackageFiles(m plugins.Manifest, files []PackageFile, executableSHA string) ([]PackageFile, error) {
	return validateDerivedPackageFiles(m, files, executableSHA, nil)
}
func validateDerivedPackageFiles(m plugins.Manifest, files []PackageFile, executableSHA string, derivation *frontendClassDerivation) ([]PackageFile, error) {
	if len(files) == 0 {
		if m.Frontend != nil {
			return nil, errors.New("frontend package requires complete file inventory")
		}
		return nil, nil
	}
	if len(files) > MaxArchiveEntries || !plugins.ValidFrontendPath(m.Entry) {
		return nil, errors.New("invalid package file count or worker entry")
	}
	out := slices.Clone(files)
	slices.SortFunc(out, func(a, b PackageFile) int { return strings.Compare(a.Path, b.Path) })
	seen := map[string]bool{}
	spellings := map[string]string{}
	var total int64
	worker, entry := false, m.Frontend == nil
	for i := range out {
		f := &out[i]
		if !plugins.ValidFrontendPath(f.Path) || f.Size < 0 || f.Size > MaxExpandedBytes-total || !validDigest(f.SHA256) || seen[f.Path] {
			return nil, errors.New("invalid package file path, size, digest or duplicate")
		}
		f.SHA256 = strings.ToLower(f.SHA256)
		total += f.Size
		seen[f.Path] = true
		parts := strings.Split(f.Path, "/")
		for n := 1; n <= len(parts); n++ {
			name := strings.Join(parts[:n], "/")
			folded := strings.ToLower(name)
			if previous, ok := spellings[folded]; ok && previous != name {
				return nil, errors.New("case-colliding package paths")
			}
			spellings[folded] = name
			if n < len(parts) && seen[name] {
				return nil, errors.New("package file shadows directory")
			}
		}
		if f.Path == m.Entry {
			if f.Size == 0 || f.SHA256 != strings.ToLower(executableSHA) {
				return nil, errors.New("package worker digest mismatch")
			}
			worker = true
		}
		if m.Frontend != nil && f.Path == path.Join(m.Frontend.Root, m.Frontend.Entry) {
			if f.Size == 0 {
				return nil, errors.New("empty frontend entry")
			}
			entry = true
		}
	}
	if !worker || (!entry && !derivedEntryRenamed(m, out, derivation)) {
		return nil, errors.New("package inventory lacks worker or frontend entry")
	}
	if m.Frontend != nil && (m.Entry == m.Frontend.Root || strings.HasPrefix(m.Entry, m.Frontend.Root+"/")) {
		return nil, errors.New("frontend root contains executable")
	}
	return out, nil
}

func packageDigest(m plugins.Manifest, contract string, files []PackageFile) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	// JSON structs and the normalized sorted inventory give deterministic bytes.
	// Instance/display metadata and provenance are intentionally not included.
	b, err := json.Marshal(struct {
		Schema   string           `json:"schema"`
		Manifest plugins.Manifest `json:"manifest"`
		Contract string           `json:"host_contract"`
		Files    []PackageFile    `json:"files"`
	}{"light-media.package-content/v1", m, contract, files})
	if err != nil {
		return "", err
	}
	return digest(b), nil
}

func verifyPackageTree(root string, files []PackageFile, hasReceipt bool) error {
	if len(files) == 0 {
		return nil
	}
	expected := make(map[string]PackageFile, len(files))
	for _, f := range files {
		expected[f.Path] = f
	}
	seen := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !info.Mode().IsRegular() && !info.IsDir() {
			return errors.New("package contains link or special node")
		}
		if rel == ".acquired.json" && hasReceipt && info.Mode().IsRegular() {
			return nil
		}
		if !plugins.ValidFrontendPath(rel) {
			return errors.New("package contains invalid path")
		}
		if info.IsDir() {
			return nil
		}
		f, ok := expected[rel]
		if !ok {
			return errors.New("package contains unlisted file")
		}
		if _, err := readPackageFile(root, f); err != nil {
			return err
		}
		seen++
		return nil
	})
	if err == nil && seen != len(files) {
		err = errors.New("package inventory has missing files")
	}
	return err
}

func openPackageFile(root string, f PackageFile) (*os.File, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	current := ""
	var info os.FileInfo
	parts := strings.Split(f.Path, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err = r.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return nil, errors.New("package links and unsafe ancestors are forbidden")
		}
	}
	if !info.Mode().IsRegular() || info.Size() != f.Size || f.Size < 0 || f.Size > MaxExpandedBytes {
		return nil, errors.New("package member size or type mismatch")
	}
	in, err := r.Open(f.Path)
	if err != nil {
		return nil, err
	}
	actual, err := in.Stat()
	if err != nil || !os.SameFile(info, actual) {
		in.Close()
		return nil, errors.New("package member changed while opening")
	}
	return in, nil
}

func readPackageFile(root string, f PackageFile) ([]byte, error) {
	in, err := openPackageFile(root, f)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(in, f.Size+1))
	err = errors.Join(err, in.Close())
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != f.Size || digest(b) != f.SHA256 {
		return nil, errors.New("package member digest or size mismatch")
	}
	return b, nil
}

// ErrInstalledFrontendPackage identifies a failure verifying the whole package
// before the requested frontend member is selected.
var ErrInstalledFrontendPackage = errors.New("installed frontend package verification failed")

// ReadFrontendAsset revalidates the entire installed package, exact content pin,
// and requested inventory member under the same acquisition/clone lock. It does
// not grant capabilities, initialize a worker, or serve arbitrary package files.
func ReadFrontendAsset(root, codeID, packageSHA, relative string, validate func(plugins.Manifest, string) error) (FrontendAsset, error) {
	if !validDigest(packageSHA) || packageSHA != strings.ToLower(packageSHA) || !plugins.ValidFrontendPath(relative) {
		return FrontendAsset{}, errors.New("plugin frontend validation: invalid frontend pin or path")
	}
	asset, err := ReadInstalledFrontendAsset(root, codeID, relative, validate, func(installed Acquired) error {
		if installed.PackageSHA256 != packageSHA {
			return errors.New("frontend unavailable or content pin changed")
		}
		return nil
	})
	if err != nil {
		return FrontendAsset{}, fmt.Errorf("plugin frontend validation: %w", err)
	}
	return asset, nil
}

// ReadInstalledFrontendAsset verifies the whole package once and safely reads
// the requested frontend member while holding the acquisition/clone lock.
// check must bind the verified package to the caller's authority before reading
// the member and must not call an artifact operation that takes the same lock.
func ReadInstalledFrontendAsset(root, codeID, relative string, validate func(plugins.Manifest, string) error, check func(Acquired) error) (FrontendAsset, error) {
	return readInstalledFrontendAsset(relative, check, func() (Acquired, error) {
		return readInstalled(root, codeID, validate)
	})
}

// ReadAsset checks the current package binding and reads the requested member.
// As in ReadInstalled, check must treat the verified package as read-only.
func (r *FrontendReader) ReadAsset(root, codeID, relative string, validate func(plugins.Manifest, string) error, check func(Acquired) error) (FrontendAsset, error) {
	return readInstalledFrontendAsset(relative, check, func() (Acquired, error) {
		return r.readInstalled(root, codeID, validate)
	})
}

func readInstalledFrontendAsset(relative string, check func(Acquired) error, resolve func() (Acquired, error)) (FrontendAsset, error) {
	if !plugins.ValidFrontendPath(relative) || check == nil {
		return FrontendAsset{}, errors.New("valid frontend path and package check required")
	}
	operationMu.Lock()
	defer operationMu.Unlock()
	installed, err := resolve()
	if err != nil {
		return FrontendAsset{}, fmt.Errorf("%w: %w", ErrInstalledFrontendPackage, err)
	}
	if err = check(installed); err != nil {
		return FrontendAsset{}, err
	}
	if installed.Manifest.Frontend == nil {
		return FrontendAsset{}, errors.New("frontend unavailable or content pin changed")
	}
	name := path.Join(installed.Manifest.Frontend.Root, relative)
	for _, f := range installed.Files {
		if f.Path != name {
			continue
		}
		kind := frontendContentType(relative)
		if kind == "" {
			return FrontendAsset{}, errors.New("unsupported frontend resource type")
		}
		b, err := readPackageFile(installed.Command.Dir, f)
		if err != nil {
			return FrontendAsset{}, err
		}
		return FrontendAsset{Data: b, SHA256: f.SHA256, ContentType: kind}, nil
	}
	return FrontendAsset{}, os.ErrNotExist
}

func frontendContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	default:
		return ""
	}
}
