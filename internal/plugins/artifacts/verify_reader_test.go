package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLargeVerificationChecksCompleteContent(t *testing.T) {
	root := t.TempDir()
	b := make([]byte, (2<<20)+17)
	for i := range b {
		b[i] = byte(i % 251)
	}
	p := filepath.Join(root, "worker")
	if err := os.WriteFile(p, b, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	want := hex.EncodeToString(sum[:])
	member := PackageFile{Path: "worker", Size: int64(len(b)), SHA256: want}
	if err := verifyPackageFile(root, member); err != nil {
		t.Fatal(err)
	}
	if err := verifyLegacyWorker(p, want); err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 1
	if err := os.WriteFile(p, b, 0700); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackageFile(root, member); err == nil {
		t.Fatal("changed trailing byte accepted")
	}
	if err := verifyLegacyWorker(p, want); err == nil {
		t.Fatal("changed legacy worker accepted")
	}
}
