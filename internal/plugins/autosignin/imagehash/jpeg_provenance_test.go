package imagehash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const jpegFixtureRoot = "../../../../testdata/plugins/auto-signin-image-hash/jpeg"

func TestFrozenInstalledJPEGSourceAndReferences(t *testing.T) {
	var manifest struct {
		LibJPEG struct {
			Version, Commit string
			Sources         map[string]string
		} `json:"libjpeg_turbo"`
		Pillow struct {
			Version string
			Sources map[string]string
		}
		Oracles    map[string]string `json:"original_oracles"`
		Generators map[string]string `json:"offline_generators"`
		Binary     string            `json:"installed_binary_provenance_sha256"`
	}
	path := filepath.Join(jpegFixtureRoot, "reference/source-checksums.json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		t.Fatal(e)
	}
	if manifest.LibJPEG.Version != "3.1.4.1" || manifest.LibJPEG.Commit != "9217719d3a58633923b096af4c1d50d304768a64" || manifest.Pillow.Version != "12.2.0" || len(manifest.Oracles) != 6 {
		t.Fatal("wrong installed JPEG reference")
	}
	check := func(path, want string) {
		t.Helper()
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatalf("source/reference checksum changed: %s", path)
		}
	}
	for name, want := range manifest.LibJPEG.Sources {
		check(filepath.Join(jpegFixtureRoot, "reference", name), want)
	}
	for name, want := range manifest.Pillow.Sources {
		check(filepath.Join(jpegFixtureRoot, "reference", name), want)
	}
	for name, want := range manifest.Generators {
		check(filepath.Join(jpegFixtureRoot, "reference", name), want)
	}
	for name, want := range manifest.Oracles {
		path := filepath.Join(jpegFixtureRoot, name)
		if name == "pillow-12.2.0-reference.json.gz" {
			path = filepath.Join(jpegFixtureRoot, "..", name)
		}
		check(path, want)
	}
	check(filepath.Join(jpegFixtureRoot, "reference/installed-binary-provenance.json"), manifest.Binary)
}
