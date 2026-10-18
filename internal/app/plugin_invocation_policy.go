package app

import (
	"bytes"
	"slices"
	"time"

	"light-media/internal/plugins/builtin"
)

type pluginRunInvocationPolicy struct {
	WorkerTimeout   time.Duration
	ExcludeHostTime bool
	Lifecycle       bool
}

// A package name, self-declared capability or action is not enough to obtain
// an unbounded native execution lifetime. preparePluginDeferred first creates
// the exact private admission in the same transaction as core-run validation.
// Read-only/form callers do not use this run policy and keep their short limits.
func pluginInvocationPolicy(in PluginInstallation, run PluginRun, d *pluginDeferredCall) pluginRunInvocationPolicy {
	p := pluginRunInvocationPolicy{WorkerTimeout: 2 * time.Minute}
	compiled, _, available := builtin.Lookup(in.PluginID)
	if !available || compiled.Version != in.Version || d == nil || d.a == nil ||
		d.installation.PluginID != in.PluginID || d.installation.InstanceID != in.InstanceID ||
		d.installation.Version != in.Version || d.installation.Generation != in.Generation ||
		d.run.ID != run.ID || d.run.InstanceID != run.InstanceID || d.run.Version != run.Version ||
		d.run.Action != run.Action || d.run.Generation != run.Generation || d.run.ConfigRevision != run.ConfigRevision ||
		!bytes.Equal(d.run.Input, run.Input) || d.record.Phase != "prepared" ||
		!deferredSameRun(d.record, in, run) || !slices.Contains(compiled.Capabilities, d.record.Method) {
		return p
	}
	method, raw, err := pluginDeferredSpec(in, run)
	if err != nil || method == "" || method != d.record.Method || !bytes.Equal(raw, d.record.Raw) {
		return p
	}
	p.ExcludeHostTime, p.Lifecycle = true, true
	return p
}
