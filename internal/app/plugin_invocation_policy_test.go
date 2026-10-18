package app

import (
	"context"
	"testing"
	"time"
)

func TestPluginInvocationPolicyRequiresExactCompiledDeferredAdmission(t *testing.T) {
	a, in, run := deferredFixture(t)
	d, err := a.preparePluginDeferred(context.Background(), in, run)
	if err != nil {
		t.Fatal(err)
	}
	p := pluginInvocationPolicy(in, run, d)
	if !p.Lifecycle || !p.ExcludeHostTime || p.WorkerTimeout != 2*time.Minute {
		t.Fatal("exact native admission did not receive lifecycle policy", p)
	}
	for _, name := range []string{"missing", "version", "generation", "revision", "input", "method", "raw", "phase", "not-compiled"} {
		t.Run(name, func(t *testing.T) {
			other := &pluginDeferredCall{a: d.a, installation: d.installation, run: d.run, record: d.record}
			i, r := in, run
			switch name {
			case "missing":
				other = nil
			case "version":
				i.Version = "99.0"
			case "generation":
				r.Generation++
			case "revision":
				r.ConfigRevision++
			case "input":
				r.Input = []byte(`{"action":"reload"}`)
			case "method":
				other.record.Method = "data.read"
			case "raw":
				other.record.Raw = []byte(`{"worker":"forged"}`)
			case "phase":
				other.record.Phase = "started"
			case "not-compiled":
				i.PluginID = "Untrusted"
			}
			got := pluginInvocationPolicy(i, r, other)
			if got.Lifecycle || got.ExcludeHostTime || got.WorkerTimeout != 2*time.Minute {
				t.Fatal("nonmatching input received lifecycle policy", got)
			}
		})
	}
}
