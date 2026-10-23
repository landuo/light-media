package artifacts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"light-media/internal/plugins"
)

// CloneInstalled makes a physically separate copy of verified installed code.
// Source and target code IDs are owned directory keys; the business manifest ID
// and receipt bytes remain unchanged. The trusted validator must verify the
// expected underlying plugin ID/version/host contract, as for ReadInstalled.
// Existing targets (including links) are never overwritten or removed. On a
// copy/verification failure only this attempt's newly created target is removed.
func CloneInstalled(root, sourceCodeID, targetCodeID string, validate func(plugins.Manifest, string) error) (Acquired, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	bad := func(err error) (Acquired, error) { return Acquired{}, fmt.Errorf("clone installed plugin: %w", err) }
	if !idPattern.MatchString(sourceCodeID) || !idPattern.MatchString(targetCodeID) || sourceCodeID == targetCodeID {
		return bad(errors.New("invalid or identical code IDs"))
	}
	source, err := readInstalled(root, sourceCodeID, validate)
	if err != nil {
		return bad(err)
	}
	target := filepath.Join(filepath.Dir(source.Command.Dir), targetCodeID)
	if _, err = os.Lstat(target); err == nil {
		return bad(errors.New("target already exists"))
	} else if !os.IsNotExist(err) {
		return bad(err)
	}
	// Mkdir reserves the target without following or replacing a concurrently
	// created node. Root's exclusive host ownership remains the API precondition.
	if err = os.Mkdir(target, 0700); err != nil {
		return bad(err)
	}
	fail := func(cause error) (Acquired, error) { return bad(errors.Join(cause, os.RemoveAll(target))) }
	if err = copyTree(source.Command.Dir, target); err != nil {
		return fail(err)
	}
	cloned, err := readInstalled(root, targetCodeID, validate)
	if err != nil {
		return fail(err)
	}
	if cloned.SHA256 != source.SHA256 {
		return fail(errors.New("copied executable digest differs from source"))
	}
	return cloned, nil
}
