package app

import (
	"light-media/internal/plugins"
	"testing"
)

// Exercise the unmodified cmd/server registry and --plugin-worker entry point,
// not TestIYUUActualSDKWorker. The parent host remains the real App with owned
// HTTP/RPC daemons. No actual remote site/downloader or production seed is used.
func TestIYUUActualServerWorkerNativeCompletionAndUnknown(t *testing.T) {
	transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	command := plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", "IYUUAutoSeed"}}
	for _, kind := range []string{"qbittorrent", "transmission"} {
		for _, unknown := range []bool{false, true} {
			state := "checkpoint-ack-loss-cold-recovery"
			if unknown {
				state = "native-ack-loss-no-replay"
			}
			t.Run(kind+"/"+state, func(t *testing.T) {
				testIYUUActualAppSDKWithSetup(t, kind, unknown, &iyuuActualSDKSetup{WorkerCommand: &command})
			})
		}
	}
}
