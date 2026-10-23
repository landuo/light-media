package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func TestRepositoryProvenanceSurvivesReopenAndClone(t *testing.T) {
	req, client, _, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
	got, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepositoryURL != req.RepositoryURL {
		t.Fatalf("acquisition source changed: %q", got.RepositoryURL)
	}
	before := *hits
	reopened, err := ReadInstalled(req.Root, req.PluginID, req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := CloneInstalled(req.Root, req.PluginID, "OwnedClone", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	cloneReopened, err := ReadInstalled(req.Root, "OwnedClone", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []Acquired{reopened, clone, cloneReopened} {
		if value.RepositoryURL != req.RepositoryURL || value.Manifest.PluginID != req.PluginID || value.SHA256 != got.SHA256 {
			t.Fatalf("provenance/identity drift: %+v", value)
		}
	}
	if *hits != before {
		t.Fatal("provenance lookup downloaded artifact")
	}
	if _, err = ReadInstalled(req.Root, req.PluginID, func(plugins.Manifest, string) error { return errors.New("untrusted despite known source") }); err == nil {
		t.Fatal("provenance bypassed trusted validator")
	}
}
func TestReceiptRepositoryURLBackwardCompatibilityAndValidation(t *testing.T) {
	for _, value := range []string{"", "file:///owned", "https://name:secret@example.invalid/index", "https://example.invalid/index#fragment", "relative/index", "https://example.invalid/index?channel=stable"} {
		t.Run(value, func(t *testing.T) {
			req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
			if _, err := Acquire(context.Background(), req, client); err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(req.Root, req.PluginID, ".acquired.json")
			b, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			var receipt map[string]json.RawMessage
			if err = json.Unmarshal(b, &receipt); err != nil {
				t.Fatal(err)
			}
			if value == "" {
				delete(receipt, "repository_url")
			} else {
				receipt["repository_url"], err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
			}
			b, err = json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(receiptPath, b, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := ReadInstalled(req.Root, req.PluginID, req.Validate)
			valid := value == "" || value == "https://example.invalid/index?channel=stable"
			if valid {
				if err != nil || got.RepositoryURL != value {
					t.Fatalf("compatible receipt failed: %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatal("unsafe source URL accepted")
			}
		})
	}
}
