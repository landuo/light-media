package artifacts

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestFrontendClassDerivationFixedSourceTrees(t *testing.T) {
	testFrontendFixedTreeOracle(t, "source-oracle.json", 9)
}
func TestFrontendClassDerivationUnicodeAndNameLimitSourceTrees(t *testing.T) {
	testFrontendFixedTreeOracle(t, "identifier-source-oracle.json", 14)
}
func testFrontendFixedTreeOracle(t *testing.T, name string, count int) {
	raw, err := os.ReadFile("../../../testdata/standard-clone-frontend/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name     string
			SourceID string            `json:"source_id"`
			CloneID  string            `json:"clone_id"`
			Input    map[string]string `json:"input_hex"`
			Expected map[string]string `json:"expected_hex"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != count {
		t.Fatal("fixed source trees missing")
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			decode := func(values map[string]string) map[string][]byte {
				out := map[string][]byte{}
				for name, text := range values {
					out[name], err = hex.DecodeString(text)
					if err != nil {
						t.Fatal(err)
					}
				}
				return out
			}
			got, err := deriveFrontendFiles(decode(c.Input), c.SourceID, c.CloneID)
			if err != nil || !reflect.DeepEqual(got, decode(c.Expected)) {
				t.Fatalf("source tree mismatch: %+v %v", got, err)
			}
		})
	}
}
func TestFrontendClassDerivationPackageCloneColdGrandcloneAndWorkerIdentity(t *testing.T) {
	req, client, _, _ := frontendFixture(t, "zip")
	original, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := CloneInstalled(req.Root, req.PluginID, "OwnedClone", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(copied.ExecutablePath)
	derived, err := DeriveInstalledFrontend(context.Background(), req.Root, "OwnedClone", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(derived.ExecutablePath)
	if derived.FrontendClass != "OwnedClone" || derived.Manifest.PluginID != "Owned" || derived.SHA256 != original.SHA256 || derived.PackageSHA256 == original.PackageSHA256 || !os.SameFile(before, after) {
		t.Fatal("frontend derivation replaced worker identity or skipped resources", derived)
	}
	asset, err := ReadFrontendAsset(req.Root, "OwnedClone", derived.PackageSHA256, "chunk.js", req.Validate)
	if err != nil || !strings.Contains(string(asset.Data), "OwnedCloneVueFixture") {
		t.Fatal("class content not derived", err, string(asset.Data))
	}
	source, err := ReadFrontendAsset(req.Root, "Owned", original.PackageSHA256, "chunk.js", req.Validate)
	if err != nil || strings.Contains(string(source.Data), "OwnedClone") {
		t.Fatal("source code changed", err)
	}
	reopened, err := ReadInstalled(req.Root, "OwnedClone", req.Validate)
	if err != nil || reopened.FrontendClass != "OwnedClone" || reopened.PackageSHA256 != derived.PackageSHA256 {
		t.Fatal("cold receipt identity lost", err)
	}
	again, err := DeriveInstalledFrontend(context.Background(), req.Root, "OwnedClone", req.Validate)
	if err != nil || again.PackageSHA256 != derived.PackageSHA256 {
		t.Fatal("derivation was not idempotent", err)
	}
	if _, err = CloneInstalled(req.Root, "OwnedClone", "OwnedCloneAgain", req.Validate); err != nil {
		t.Fatal(err)
	}
	grandchild, err := DeriveInstalledFrontend(context.Background(), req.Root, "OwnedCloneAgain", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	asset, err = ReadFrontendAsset(req.Root, "OwnedCloneAgain", grandchild.PackageSHA256, "chunk.js", req.Validate)
	if err != nil || !strings.Contains(string(asset.Data), "OwnedCloneAgainVueFixture") {
		t.Fatal("grandclone used canonical instead of actual source class", err, string(asset.Data))
	}
	if _, err = ReadFrontendAsset(req.Root, "OwnedClone", derived.PackageSHA256, "chunk.js", req.Validate); err != nil {
		t.Fatal("grandclone changed its source", err)
	}
}
func TestFrontendClassDerivationPublicationFailureRestoresReceiptResourcesAndWorker(t *testing.T) {
	req, client, _, _ := frontendFixture(t, "zip")
	if _, err := Acquire(context.Background(), req, client); err != nil {
		t.Fatal(err)
	}
	before, err := CloneInstalled(req.Root, "Owned", "OwnedClone", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := os.ReadFile(filepath.Join(before.Command.Dir, ".acquired.json"))
	inode, _ := os.Stat(before.ExecutablePath)
	count := 0
	validate := func(m plugins.Manifest, c string) error {
		count++
		if count == 2 {
			return errors.New("controlled post-publish validation rejection")
		}
		return req.Validate(m, c)
	}
	if _, err = DeriveInstalledFrontend(context.Background(), req.Root, "OwnedClone", validate); err == nil {
		t.Fatal("validation rejection hidden")
	}
	recovered, err := ReadInstalled(req.Root, "OwnedClone", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	now, _ := os.ReadFile(filepath.Join(recovered.Command.Dir, ".acquired.json"))
	worker, _ := os.Stat(recovered.ExecutablePath)
	if !bytes.Equal(receipt, now) || recovered.PackageSHA256 != before.PackageSHA256 || !os.SameFile(inode, worker) {
		t.Fatal("failed publication changed previous package")
	}
}
func TestFrontendClassDerivationPreservesSourceBrokenEntryRename(t *testing.T) {
	req, client, index, _ := frontendFixture(t, "zip")
	req.PluginID = "Remote"
	index.PluginID = "Remote"
	index.Releases[0].Manifest.PluginID = "Remote"
	original, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CloneInstalled(req.Root, "Remote", "Remotecopy", req.Validate); err != nil {
		t.Fatal(err)
	}
	clone, err := DeriveInstalledFrontend(context.Background(), req.Root, "Remotecopy", req.Validate)
	if err != nil {
		t.Fatal("source clone must retain its backend despite broken remoteEntry URL", err)
	}
	if clone.SHA256 != original.SHA256 {
		t.Fatal("compiled worker changed")
	}
	if _, err = ReadFrontendAsset(req.Root, "Remotecopy", clone.PackageSHA256, "remoteEntry.js", req.Validate); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source renamed entry URL unexpectedly worked", err)
	}
	if _, err = ReadFrontendAsset(req.Root, "Remotecopy", clone.PackageSHA256, "remotecopyEntry.js", req.Validate); err != nil {
		t.Fatal("source top-level renamed file missing", err)
	}
	if _, err = ReadInstalled(req.Root, "Remotecopy", req.Validate); err != nil {
		t.Fatal("cold backend rejected exact source broken-frontend state", err)
	}
}
