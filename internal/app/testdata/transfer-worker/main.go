// This test-only executable retains the real Transfer SDK and stdio protocol,
// without loading the App test binary or unrelated built-in plugin packages.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"light-media/internal/plugins"
	"light-media/internal/plugins/torrenttransfer"
)

func main() {
	id := flag.String("plugin-worker", "", "Transfer plugin identity")
	flag.Parse()
	if flag.NArg() != 0 || (*id != "TorrentTransfer" && *id != "TorrentTransfermusic") {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if plugins.Serve(ctx, os.Stdin, os.Stdout, torrenttransfer.Manifest(*id), torrenttransfer.Handle) != nil {
		os.Exit(2)
	}
}
