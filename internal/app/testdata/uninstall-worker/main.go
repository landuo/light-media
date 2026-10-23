// Real Uninstall SDK peer without the unrelated App test initialization.
package main

import (
	"context"
	"flag"
	"light-media/internal/plugins"
	"light-media/internal/plugins/uninstall"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	id := flag.String("plugin-worker", "", "Uninstall plugin identity")
	flag.Parse()
	if flag.NArg() != 0 || *id != "PluginUnInstall" {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if plugins.Serve(ctx, os.Stdin, os.Stdout, uninstall.Manifest(), uninstall.Handle) != nil {
		os.Exit(2)
	}
}
