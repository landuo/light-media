package systemcommands

import (
	"context"
	"strings"
	"testing"
)

// Reuses immutable original23 raw and its already accepted recording helpers.
// Selects only the first restart call of eight existing cases, never runs the
// Python collector, version calls or finish calls. No expected result comes
// from the target. The backup seam delegates the original recording FS body
// so that its full existing raw trace remains comparable here.
func TestNativeRestartExactOriginalEightCalls(t *testing.T) {
	count := 0
	for _, value := range sourceCases(t) {
		original := obj(value)
		if !strings.HasPrefix(str(original["id"]), "restart-") {
			continue
		}
		count++
		t.Run(str(original["id"]), func(t *testing.T) {
			input := obj(original["input"])
			call := obj(arr(input["calls"])[0])
			if call["method"] != "restart" {
				t.Fatal("unexpected selected method")
			}
			p := newRecording(obj(input["ports"]))
			legacy := p.engine(t)
			ports := p.ports()
			native, err := NewNativeRestart("__system_restart__", NativeRestartPorts{
				Guard: ports.Guard, Notify: ports.Notify, Log: ports.Log, Cache: p, Codec: recordingCodec{p}, Restart: ports.Restart,
				Backup: func(ctx context.Context) error {
					docker, err := ports.IsDocker(ctx)
					if err != nil || !docker {
						return err
					}
					return legacy.backupBody(ctx)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			channel := channelNamed(call["channel"])
			var source *string
			if call["source"] != nil {
				value := str(call["source"])
				source = &value
			}
			err = native.Restart(context.Background(), channel, call["userid"], source)
			observed := object{"method": "restart", "args": []any{channelData(channel), call["userid"], call["source"]}, "trace_start": 0, "trace_end": len(p.trace), "cache_after": clone(p.cache)}
			if err == nil {
				observed["return"] = nil
			} else {
				observed["exception"] = errorData(err)
			}
			want := obj(arr(original["calls"])[0])
			same(t, "exact original first restart call", observed, want)
			same(t, "exact original ordered effects", p.trace, arr(original["trace"])[:number(want["trace_end"])])
			if p.requests != 0 {
				t.Fatal("native restart acquired a remote version dependency")
			}
		})
	}
	if count != 8 {
		t.Fatal("unexpected finite original restart count", count)
	}
}
