package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Acceptance-only directory provider. Its caller has checked both owned roots.
func (f *fixture) transferOwnedDirectory(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("owned directory rejects symlink")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		output := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(output, 0750)
		}
		if f.config.TransferMode == "copy" || f.config.TransferMode == "corrupt" {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if f.config.TransferMode == "corrupt" {
				for i := range data {
					data[i] = 'x'
				}
			}
			return os.WriteFile(output, data, 0640)
		}
		return os.Link(path, output)
	})
}
