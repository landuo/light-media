package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// publishResourcesInPlace preserves the worker and every ancestor inode (the
// actual process cwd), replacing only sibling resource trees and the receipt.
// root is the same exclusive, already-validated owned package root as Acquire.
// Every move is rooted and journaled; a failed move/validation rolls back while
// the existing process keeps its executable and cwd. A retained recovery folder
// is explicit if rollback itself fails. move=nil uses actual os.Root.Rename;
// the override is a bounded filesystem-error test seam, never plugin authority.
func publishResourcesInPlace(ctx context.Context, root, target, stage, entry string, validate func() error, move func(string, string) error) error {
	filesystem, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer filesystem.Close()
	backup, err := os.MkdirTemp(root, ".resources-backup-")
	if err != nil {
		return err
	}
	backup = filepath.Base(backup)
	if move == nil {
		move = filesystem.Rename
	}
	type movement struct{ from, to string }
	journal := []movement{}
	commitMove := func(from, to string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := move(from, to); err != nil {
			return err
		}
		journal = append(journal, movement{from, to})
		return nil
	}
	readNames := func(name string) (map[string]bool, error) {
		file, err := filesystem.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		values, err := file.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		out := map[string]bool{}
		for _, value := range values {
			out[value.Name()] = true
		}
		return out, nil
	}
	var synchronize func(string, []string) error
	synchronize = func(relative string, protected []string) error {
		oldBase, newBase := filepath.Join(target, relative), filepath.Join(stage, relative)
		oldNames, err := readNames(oldBase)
		if err != nil {
			return err
		}
		newNames, err := readNames(newBase)
		if err != nil {
			return err
		}
		all := map[string]bool{}
		for name := range oldNames {
			all[name] = true
		}
		for name := range newNames {
			all[name] = true
		}
		names := []string{}
		for name := range all {
			if relative != "" || name != ".acquired.json" {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		if relative == "" {
			names = append(names, ".acquired.json")
		} // commit identity last
		for _, name := range names {
			if len(protected) > 0 && name == protected[0] {
				if len(protected) > 1 {
					if err := synchronize(filepath.Join(relative, name), protected[1:]); err != nil {
						return err
					}
				}
				continue
			}
			if oldNames[name] {
				saved := filepath.Join(backup, relative, name)
				if err := filesystem.MkdirAll(filepath.Dir(saved), 0700); err != nil {
					return err
				}
				if err := commitMove(filepath.Join(oldBase, name), saved); err != nil {
					return err
				}
			}
			if newNames[name] {
				if err := commitMove(filepath.Join(newBase, name), filepath.Join(oldBase, name)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err = synchronize("", strings.Split(entry, "/"))
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && validate != nil {
		err = validate()
	}
	if err != nil {
		var rollback error
		for i := len(journal) - 1; i >= 0; i-- {
			action := journal[i]
			rollback = errors.Join(rollback, filesystem.Rename(action.to, action.from))
		}
		if rollback != nil {
			return &StageError{Stage: "resource_rollback", RecoveryPath: filepath.Join(root, backup), Err: errors.Join(err, rollback)}
		}
		_ = filesystem.RemoveAll(backup)
		return err
	}
	// Content is already committed; obsolete backup cleanup is best effort,
	// matching ordinary acquisition rather than reporting a failed install.
	_ = filesystem.RemoveAll(backup)
	return nil
}
