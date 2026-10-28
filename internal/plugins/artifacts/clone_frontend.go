package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/encoding/unicode"
	"light-media/internal/plugins"
)

type frontendClassDerivation struct {
	SourceClass string `json:"source_class"`
	TargetClass string `json:"target_class"`
}

// DeriveInstalledFrontend is the platform's class-copy step, separate from
// generic CloneInstalled. The declared Go frontend root represents MoviePilot's
// fixed dist directory. The compiled worker/manifest stay unchanged; only the
// verified JS/CSS tree and its content inventory are derived. A copied clone's
// receipt records its actual frontend class for clone-of-clone and cold reads.
func DeriveInstalledFrontend(ctx context.Context, root, codeID string, validate func(plugins.Manifest, string) error) (Acquired, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	installed, err := readInstalled(root, codeID, validate)
	if err != nil {
		return Acquired{}, err
	}
	if installed.Manifest.Frontend == nil || installed.FrontendClass == codeID {
		return installed, nil
	}
	fail := func(err error) (Acquired, error) {
		return Acquired{}, fmt.Errorf("derive cloned plugin frontend: %w", err)
	}
	prefix := installed.Manifest.Frontend.Root + "/"
	original := map[string][]byte{}
	for _, file := range installed.Files {
		if !strings.HasPrefix(file.Path, prefix) {
			continue
		}
		data, err := readPackageFile(installed.Command.Dir, file)
		if err != nil {
			return fail(err)
		}
		original[strings.TrimPrefix(file.Path, prefix)] = data
	}
	derived, err := deriveFrontendFiles(original, installed.FrontendClass, codeID)
	if err != nil {
		return fail(err)
	}
	derivation := &frontendClassDerivation{SourceClass: installed.FrontendClass, TargetClass: codeID}
	files := []PackageFile{}
	for _, file := range installed.Files {
		if !strings.HasPrefix(file.Path, prefix) {
			files = append(files, file)
		}
	}
	names := make([]string, 0, len(derived))
	for name := range derived {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		data := derived[name]
		files = append(files, PackageFile{Path: prefix + name, Size: int64(len(data)), SHA256: digest(data)})
	}
	files, err = validateDerivedPackageFiles(installed.Manifest, files, installed.SHA256, derivation)
	if err != nil {
		return fail(err)
	}
	packageSHA, err := packageDigest(installed.Manifest, installed.HostContract, files)
	if err != nil {
		return fail(err)
	}
	receipt, err := json.Marshal(installedReceipt{Manifest: installed.Manifest, SHA256: installed.SHA256, HostContract: installed.HostContract, RepositoryURL: installed.RepositoryURL, Files: files, PackageSHA256: packageSHA, FrontendDerivation: derivation})
	if err != nil || int64(len(receipt)) > MaxIndexBytes {
		return fail(errors.Join(err, errors.New("derived package receipt exceeds size bound")))
	}
	ownedRoot := filepath.Dir(installed.Command.Dir)
	stage, err := os.MkdirTemp(ownedRoot, ".frontend-class-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(stage)
	if err = copyTree(installed.Command.Dir, stage); err != nil {
		return fail(err)
	}
	for name := range original {
		if _, exists := derived[name]; !exists {
			if err = os.Remove(filepath.Join(stage, filepath.FromSlash(prefix+name))); err != nil {
				return fail(err)
			}
		}
	}
	for _, name := range names {
		if err = os.WriteFile(filepath.Join(stage, filepath.FromSlash(prefix+name)), derived[name], 0600); err != nil {
			return fail(err)
		}
	}
	if err = os.WriteFile(filepath.Join(stage, ".acquired.json"), receipt, 0600); err != nil {
		return fail(err)
	}
	if err = verifyPackageTree(stage, files, true); err != nil {
		return fail(err)
	}
	// The existing journalled publication preserves the running worker inode/cwd
	// and rolls resources/receipt back together on any filesystem/check failure.
	if err = publishResourcesInPlace(ctx, ownedRoot, codeID, filepath.Base(stage), installed.Manifest.Entry, func() error { _, err := readInstalled(ownedRoot, codeID, validate); return err }, nil); err != nil {
		return fail(err)
	}
	return readInstalled(ownedRoot, codeID, validate)
}

func deriveFrontendFiles(files map[string][]byte, originalClass, cloneClass string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(files))
	names := make([]string, 0, len(files))
	for name, data := range files {
		result[name] = bytes.Clone(data)
		names = append(names, name)
	}
	slices.Sort(names)
	lowerOriginal, lowerClone := plugins.OriginalPluginLower(originalClass), plugins.OriginalPluginLower(cloneClass)
	for _, name := range names {
		extension := path.Ext(name)
		if extension != ".js" && extension != ".css" {
			continue
		}
		decoded, err := unicode.UTF8.NewDecoder().Bytes(result[name])
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(strings.ReplaceAll(string(decoded), "\r\n", "\n"), "\r", "\n")
		if extension == ".js" {
			text = strings.ReplaceAll(text, originalClass, cloneClass)
			text = strings.ReplaceAll(text, `"`+originalClass+`"`, `"`+cloneClass+`"`)
			text = strings.ReplaceAll(text, `'`+originalClass+`'`, `'`+cloneClass+`'`)
			text = strings.ReplaceAll(text, "css__"+originalClass+"__", "css__"+cloneClass+"__")
			text = strings.ReplaceAll(text, lowerOriginal, lowerClone)
		} else {
			text = strings.ReplaceAll(text, lowerOriginal, lowerClone)
			text = strings.ReplaceAll(text, originalClass, cloneClass)
		}
		result[name] = []byte(text)
	}
	for _, name := range names {
		if strings.Contains(name, "/") || !strings.Contains(plugins.OriginalPluginLower(name), lowerOriginal) {
			continue
		}
		target := strings.ReplaceAll(name, lowerOriginal, lowerClone)
		if _, exists := result[target]; exists {
			continue
		}
		// The fixed rename catches ENAMETOOLONG around the complete loop:
		// content edits survive and this/all later names keep their old spelling.
		if len(target) > 255 {
			break
		}
		result[target] = result[name]
		delete(result, name)
	}
	return result, nil
}

// Source rename can move remoteEntry.js itself (e.g. class Remote). Preserve
// that original broken frontend URL without rejecting the valid backend clone:
// only an exact, recorded top-level rename may explain a missing compiled entry.
func derivedEntryRenamed(m plugins.Manifest, files []PackageFile, d *frontendClassDerivation) bool {
	if m.Frontend == nil || d == nil || strings.Contains(m.Frontend.Entry, "/") {
		return false
	}
	renamed := strings.ReplaceAll(m.Frontend.Entry, plugins.OriginalPluginLower(d.SourceClass), plugins.OriginalPluginLower(d.TargetClass))
	if renamed == m.Frontend.Entry {
		return false
	}
	expected := path.Join(m.Frontend.Root, renamed)
	return slices.ContainsFunc(files, func(file PackageFile) bool { return file.Path == expected && file.Size > 0 })
}
