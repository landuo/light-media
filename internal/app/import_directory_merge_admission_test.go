package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Mechanism-only admission tests: seeded owner records are not publisher evidence.
func TestSharedDirectoryNewModesKeepRootAdmissionClosed(t *testing.T) {
	for _, mode := range []string{"softlink", "move"} {
		for _, boundary := range []string{"managed-root", "unknown-link"} {
			t.Run(mode+"/"+boundary, func(t *testing.T) {
				a, sourceRoot, library := importTestApp(t)
				source, target := filepath.Join(sourceRoot, "disc"), filepath.Join(library, "disc")
				importWrite(t, filepath.Join(source, "BDMV/STREAM/00001.m2ts"), []byte("original"))
				importWrite(t, filepath.Join(target, "unrelated.txt"), []byte("unrelated"))
				if boundary == "managed-root" {
					if e := a.Store.Put(t.Context(), "managed_file", tokenHash(target), managedFile{Path: target, AssetID: "other-asset", Kind: "video"}); e != nil {
						t.Fatal(e)
					}
				} else {
					if e := os.Symlink(filepath.Join(source, "BDMV/STREAM/00001.m2ts"), filepath.Join(target, "unknown.m2ts")); e != nil {
						t.Fatal(e)
					}
				}
				receipt := importReceipt{ID: "admission-unit", Source: source, Target: target, State: "planned"}
				if err := a.beginDirectoryMerge(t.Context(), &receipt, PlanFile{Source: source, Target: target, Method: mode}); err == nil {
					t.Fatal("existing managed root or unknown symlink admitted")
				}
				if receipt.SharedDirectory != nil || receipt.DirectoryMerge != nil || receipt.State != "planned" {
					t.Fatal("root ownership created before admission", receipt)
				}
				if got, e := os.ReadFile(filepath.Join(target, "unrelated.txt")); e != nil || string(got) != "unrelated" {
					t.Fatal("unrelated target changed", e)
				}
			})
		}
	}
}
