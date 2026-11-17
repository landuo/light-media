package app

import (
	"testing"

	"light-media/internal/plugins"
)

// The installation record may hold the source index's placeholder version while
// the loaded object runs a newer compiled build. Updates compare against the
// build that is actually executing.
func TestRunningPluginVersionIsTheLoadedBuild(t *testing.T) {
	a := &App{}
	a.pluginRunningInstances.Store("AutoSignIn", &originalPluginInstance{manifest: plugins.Manifest{PluginID: "AutoSignIn", Version: "2.9.14"}})
	if got := a.runningPluginVersion("AutoSignIn"); got != "2.9.14" {
		t.Fatalf("running version %q", got)
	}
	if got := a.runningPluginVersion("Missing"); got != "" {
		t.Fatalf("unknown instance returned %q", got)
	}
	a.pluginRunningInstances.Store("Broken", "not an instance")
	if got := a.runningPluginVersion("Broken"); got != "" {
		t.Fatalf("unexpected version %q", got)
	}
}
