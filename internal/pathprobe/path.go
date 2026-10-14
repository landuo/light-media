// Package pathprobe performs bounded filesystem experiments on its own files.
package pathprobe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContainedPath checks an existing path or a new leaf under an authorized root.
// It resolves symlinks before checking containment. It is a planning check,
// not protection against concurrent replacement by another process; a future
// importer must also bind execution to file identities and an operation lock.
func ContainedPath(root, relative string) (string, error) {
	if relative == "" || relative == "." || strings.ContainsRune(relative, 0) || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", fmt.Errorf("invalid_relative_path")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path_escape")
	}
	root, err := directory(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		// Only a missing final component is accepted. A dangling symlink must
		// fail instead of turning into an apparently safe new leaf.
		if _, statErr := os.Lstat(candidate); statErr == nil {
			return "", fmt.Errorf("dangling_symlink")
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(candidate))
		if parentErr != nil {
			return "", fmt.Errorf("parent_unavailable")
		}
		resolved = filepath.Join(parent, filepath.Base(candidate))
	}
	inside, err := filepath.Rel(root, resolved)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", fmt.Errorf("symlink_escape")
	}
	return resolved, nil
}

func directory(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("directory_unavailable")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("not_directory")
	}
	return resolved, nil
}
