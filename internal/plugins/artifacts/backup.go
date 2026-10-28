package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
)

// BackupInstalled stores only validated owned code and its provenance receipt.
// It never includes instance configuration, data, logs, or database files.
func BackupInstalled(root, backupRoot, id string, replace bool, validate func(plugins.Manifest, string) error) error {
	operationMu.Lock()
	defer operationMu.Unlock()
	return backupInstalled(root, backupRoot, id, replace, validate)
}

func backupInstalled(root, backupRoot, id string, replace bool, validate func(plugins.Manifest, string) error) error {
	if _, err := readInstalled(root, id, validate); err != nil {
		return err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	backupRoot, err = filepath.Abs(backupRoot)
	if err != nil {
		return err
	}
	if root == backupRoot || withinOwnedCode(filepath.Join(root, id), backupRoot) {
		return errors.New("backup root must not overlap owned plugin code")
	}
	if err = ensureRoot(backupRoot); err != nil {
		return err
	}
	target := filepath.Join(backupRoot, id)
	if err = safeExistingTree(target); err != nil {
		return err
	}
	if _, err = os.Lstat(target); err == nil && !replace {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(backupRoot, ".backup-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	prepared := filepath.Join(stage, id)
	if err = copyTree(filepath.Join(root, id), prepared); err != nil {
		return err
	}
	if _, err = readInstalled(stage, id, validate); err != nil {
		return err
	}
	if err = os.RemoveAll(target); err != nil {
		return err
	}
	return os.Rename(prepared, target)
}

// RestoreMissingInstalled refuses to overwrite existing code. Go packages
// already live in persistent application storage; only a missing runtime copy
// needs container-start restoration. The backup is retained on every outcome.
func RestoreMissingInstalled(root, backupRoot, id string, validate func(plugins.Manifest, string) error) (bool, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	if _, err := readInstalled(backupRoot, id, validate); err != nil {
		return false, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	backupRoot, err = filepath.Abs(backupRoot)
	if err != nil {
		return false, err
	}
	if root == backupRoot || withinOwnedCode(filepath.Join(backupRoot, id), root) {
		return false, errors.New("runtime root must not overlap backed-up plugin code")
	}
	if err = ensureRoot(root); err != nil {
		return false, err
	}
	target := filepath.Join(root, id)
	if _, err = os.Lstat(target); err == nil {
		_, err = readInstalled(root, id, validate)
		return false, err
	} else if !os.IsNotExist(err) {
		return false, err
	}
	stage, err := os.MkdirTemp(root, ".restore-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)
	prepared := filepath.Join(stage, id)
	if err = copyTree(filepath.Join(backupRoot, id), prepared); err != nil {
		return false, err
	}
	if _, err = readInstalled(stage, id, validate); err != nil {
		return false, err
	}
	if err = os.Rename(prepared, target); err != nil {
		return false, err
	}
	return true, nil
}

func withinOwnedCode(code, path string) bool {
	rel, err := filepath.Rel(code, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}
