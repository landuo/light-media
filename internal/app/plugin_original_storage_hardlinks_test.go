package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"light-media/internal/plugins"
)

// Installed RemoveLink2.18 _unlink_tracked_file:1246-1274 invokes Path.unlink;
// the remaining names of that inode retain their original content. The normal
// file-manager policy remains distinct from that finite plugin storage call.
func TestOriginalPluginStorageActualSDKUnlinksOnlySelectedHardlink(t *testing.T) {
	a, _, object := ownedHostServicesFixture(t)
	folder := t.TempDir()
	keep := filepath.Join(folder, "retained.mkv")
	remove := filepath.Join(folder, "selected.mkv")
	payload := []byte("owned source-derived unlink fixture")
	if err := os.WriteFile(keep, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(keep, remove); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(keep)
	if err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.AllowedRoots, settings.Roots = []string{folder}, nil
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err = browserManifest(context.Background(), remove); fileCode(err) != "FILE_LINK_PROTECTED" {
		t.Fatalf("interactive protection lost: %v", err)
	}
	raw, err := ownedHostServicesCall(t, object, plugins.StorageDeleteCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, remove)})
	if err != nil {
		t.Fatal("actual SDK unlink", err)
	}
	var deleted bool
	if json.Unmarshal(raw, &deleted) != nil || !deleted {
		t.Fatalf("source unlink did not succeed: %s", raw)
	}
	if _, err = os.Lstat(remove); !os.IsNotExist(err) {
		t.Fatalf("selected entry remains: %v", err)
	}
	after, err := os.Stat(keep)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("other inode changed: %v", err)
	}
	content, err := os.ReadFile(keep)
	if err != nil || string(content) != string(payload) {
		t.Fatalf("other hardlink contents changed: %v", err)
	}
	// Guard denial remains an actual error and never removes a different root.
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	if err = os.WriteFile(outside, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ownedHostServicesCall(t, object, plugins.StorageDeleteCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, outside)}); err == nil {
		t.Fatal("outside-root unlink accepted")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != string(payload) {
		t.Fatal("outside file changed", err)
	}
}

func TestOriginalPluginStorageActualSDKUnlinksUnreadableHardlink(t *testing.T) {
	a, _, object := ownedHostServicesFixture(t)
	folder := t.TempDir()
	keep, remove := filepath.Join(folder, "retained.mkv"), filepath.Join(folder, "selected.mkv")
	payload := []byte("owned unreadable hardlink source fixture")
	if err := os.WriteFile(keep, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(keep, remove); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(keep)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(keep, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(keep, 0600)
	settings := a.Settings()
	settings.AllowedRoots, settings.Roots = []string{folder}, nil
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	raw, err := ownedHostServicesCall(t, object, plugins.StorageDeleteCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, remove)})
	if err != nil || string(raw) != "true" {
		t.Fatal("source unlink needs writable parent, not readable file", string(raw), err)
	}
	if _, err = os.Lstat(remove); !os.IsNotExist(err) {
		t.Fatal("unreadable selected entry remains", err)
	}
	if err = os.Chmod(keep, 0600); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(keep)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("retained inode changed", err)
	}
	data, err := os.ReadFile(keep)
	if err != nil || string(data) != string(payload) {
		t.Fatal("retained content changed", err)
	}
}

func TestOriginalPluginStorageActualSDKUnlinksWriteExecuteParent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("delivered Linux parent-search permission boundary; not executed on this platform")
	}
	a, _, object := ownedHostServicesFixture(t)
	folder := t.TempDir()
	parent := filepath.Join(folder, "no-read")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	keep, remove := filepath.Join(folder, "retained.mkv"), filepath.Join(parent, "selected.mkv")
	payload := []byte("owned source write-execute parent fixture")
	if err := os.WriteFile(keep, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(keep, remove); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(keep)
	if err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.AllowedRoots, settings.Roots = []string{folder}, nil
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(parent, 0300); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0700)
	raw, err := ownedHostServicesCall(t, object, plugins.StorageDeleteCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, remove)})
	if err != nil || string(raw) != "true" {
		t.Fatal("source unlink requires parent write+execute, without read", string(raw), err)
	}
	if err = os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(remove); !os.IsNotExist(err) {
		t.Fatal("selected entry remains", err)
	}
	after, err := os.Stat(keep)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("retained inode changed", err)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != string(payload) {
		t.Fatal("retained bytes changed", err)
	}
}
