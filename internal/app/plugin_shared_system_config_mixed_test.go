package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This portable timing observer is test-only: it runs after the real SQL Scan,
// does not alter query/results/transaction semantics, and is never in a product
// binary. The child test still builds and installs the actual typed SDK worker.
func TestSharedSystemConfigMixedInterleavings(t *testing.T) {
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	statePath := filepath.Join(root, "internal/app/plugin_shared_system_config_state.go")
	state, e := os.ReadFile(statePath)
	if e != nil {
		t.Fatal(e)
	}
	needle := "func readSharedSystemConfigRow(ctx context.Context, db sharedSystemConfigReader) (sharedSystemConfigSnapshot, error) {"
	if strings.Count(string(state), needle) != 1 {
		t.Fatal("SQL timing observation point changed")
	}
	instrumented := strings.Replace(string(state), needle, needle+"\n"+`defer func(){ if observer,ok:=ctx.Value(ownedConfigSnapshotObserverKey{}).(func());ok {observer()} }()`, 1)
	temp := t.TempDir()
	stateOverlay := filepath.Join(temp, "state-observed.go")
	if e = os.WriteFile(stateOverlay, []byte(instrumented), 0600); e != nil {
		t.Fatal(e)
	}
	overlay := map[string]any{"Replace": map[string]string{statePath: stateOverlay, filepath.Join(root, "internal/app/zz_owned_shared_config_mixed_test.go"): filepath.Join(root, "testdata/plugin-shared-host-20261030/system-config/mixed_test.go.txt")}}
	raw, _ := json.Marshal(overlay)
	overlayPath := filepath.Join(temp, "overlay.json")
	if e = os.WriteFile(overlayPath, raw, 0600); e != nil {
		t.Fatal(e)
	}
	args := []string{"test", "-buildvcs=false", "-overlay", overlayPath, "-count=1", "-run", "^TestOwnedSharedSystemConfigMixed$", "-v"}
	if os.Getenv("OWNED_SYSTEM_CONFIG_RACE") == "1" {
		args = append(args, "-race")
	}
	args = append(args, "./internal/app")
	command := exec.Command("go", args...)
	command.Dir = root
	output, e := command.CombinedOutput()
	t.Logf("actual timing child go %q\n%s", args, output)
	if e != nil {
		t.Fatal(e)
	}
}
