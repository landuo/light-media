package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResourceTreePreservesRuntimeInodesAndRollback(t *testing.T) {
	for _, failMove := range []int{0, 3, -1} {
		t.Run(map[int]string{0: "success", 3: "partial_move_rollback", -1: "post_validation_rollback"}[failMove], func(t *testing.T) {
			root := t.TempDir()
			old := filepath.Join(root, "Owned")
			stage := filepath.Join(root, "prepared")
			for _, base := range []string{old, stage} {
				if err := os.MkdirAll(filepath.Join(base, "bin"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(base, "frontend"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(base, name, value string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(base, name), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, base := range []string{old, stage} {
				write(base, "bin/worker", "same actual worker identity")
			}
			write(old, "bin/other", "old sibling")
			write(stage, "bin/other", "new sibling")
			write(old, "frontend/remoteEntry.js", "old resources")
			write(stage, "frontend/remoteEntry.js", "new resources")
			write(old, ".acquired.json", "old receipt")
			write(stage, ".acquired.json", "new receipt")
			rootBefore, _ := os.Stat(old)
			workerBefore, _ := os.Stat(filepath.Join(old, "bin/worker"))
			parentBefore, _ := os.Stat(filepath.Join(old, "bin"))
			fs, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer fs.Close()
			moves := 0
			err = publishResourcesInPlace(context.Background(), root, "Owned", "prepared", "bin/worker", func() error {
				if failMove == -1 {
					return errors.New("owned post-publication validation failure")
				}
				return nil
			}, func(from, to string) error {
				moves++
				if moves == failMove {
					return errors.New("owned injected resource move failure")
				}
				return fs.Rename(from, to)
			})
			if (err != nil) != (failMove != 0) {
				t.Fatal("wrong source resource sync failure result", err)
			}
			rootAfter, _ := os.Stat(old)
			workerAfter, _ := os.Stat(filepath.Join(old, "bin/worker"))
			parentAfter, _ := os.Stat(filepath.Join(old, "bin"))
			if !os.SameFile(rootBefore, rootAfter) || !os.SameFile(workerBefore, workerAfter) || !os.SameFile(parentBefore, parentAfter) {
				t.Fatal("resource publication orphaned real runtime cwd/executable")
			}
			expected := "new"
			if failMove != 0 {
				expected = "old"
			}
			for path, suffix := range map[string]string{"frontend/remoteEntry.js": " resources", "bin/other": " sibling", ".acquired.json": " receipt"} {
				value, err := os.ReadFile(filepath.Join(old, path))
				if err != nil || string(value) != expected+suffix {
					t.Fatal("resource commit/rollback mismatched", path, string(value), err)
				}
			}
		})
	}
}
