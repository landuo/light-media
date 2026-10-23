package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestReadInstalledValidatesBytesAndAuthority(t *testing.T) {
	for _, change := range []string{"none", "missing", "replacement", "symlink", "receipt", "contract", "id", "path", "validator", "unknown_field"} {
		t.Run(change, func(t *testing.T) {
			req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
			got, err := Acquire(context.Background(), req, client)
			if err != nil {
				t.Fatal(err)
			}
			receipt := filepath.Join(req.Root, req.PluginID, ".acquired.json")
			switch change {
			case "missing":
				os.Remove(got.ExecutablePath)
			case "replacement":
				os.WriteFile(got.ExecutablePath, []byte("replaced-owned-bytes"), 0700)
			case "symlink":
				os.Remove(got.ExecutablePath)
				if err = os.Symlink(filepath.Join(t.TempDir(), "missing"), got.ExecutablePath); err != nil {
					t.Skip(err)
				}
			case "receipt":
				os.Remove(receipt)
			case "contract":
				req.Validate = func(plugins.Manifest, string) error { return errors.New("no longer authorized") }
			case "validator":
				req.Validate = nil
			case "id", "path", "unknown_field":
				b, _ := os.ReadFile(receipt)
				s := string(b)
				if change == "id" {
					s = strings.Replace(s, `"plugin_id":"Owned"`, `"plugin_id":"Other"`, 1)
				}
				if change == "path" {
					s = strings.Replace(s, `"entry":"bin/worker"`, `"entry":"../escape"`, 1)
				}
				if change == "unknown_field" {
					s = strings.Replace(s, `{"manifest":`, `{"unexpected":1,"manifest":`, 1)
				}
				os.WriteFile(receipt, []byte(s), 0600)
			}
			validator := req.Validate
			if validator != nil {
				policy := validator
				validator = func(m plugins.Manifest, c string) error {
					if m.PluginID != "Owned" || m.Version != "1.2.3" {
						return errors.New("installation identity mismatch")
					}
					return policy(m, c)
				}
			}
			loaded, err := ReadInstalled(req.Root, req.PluginID, validator)
			if change == "none" {
				if err != nil || loaded.SHA256 != got.SHA256 || loaded.Command.Path != got.Command.Path {
					t.Fatalf("%+v %v", loaded, err)
				}
			} else if err == nil {
				t.Fatal("invalid installed state accepted")
			}
		})
	}
}
func TestReadInstalledDoesNotCreateMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	if _, err := ReadInstalled(root, "Owned", func(plugins.Manifest, string) error { return nil }); err == nil {
		t.Fatal("missing accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("read created root: %v", err)
	}
}

func TestReadInstalledCloneDirectoryUsesTrustedBusinessIdentity(t *testing.T) {
	req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	if _, err := Acquire(context.Background(), req, client); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(req.Root, req.PluginID), filepath.Join(req.Root, "OwnedClone")); err != nil {
		t.Fatal(err)
	}
	validate := func(m plugins.Manifest, c string) error {
		if m.PluginID != "Owned" || m.Version != "1.2.3" || c != "owned/v1" {
			return errors.New("wrong underlying installation")
		}
		return nil
	}
	got, err := ReadInstalled(req.Root, "OwnedClone", validate)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.PluginID != "Owned" || got.Command.Dir != filepath.Join(req.Root, "OwnedClone") {
		t.Fatalf("wrong clone identity: %+v", got)
	}
}
