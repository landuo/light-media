// Test-only real AutoSignIn SDK entry; host remains the actual App.
package main

import (
	"context"
	"flag"
	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	id := flag.String("plugin-worker", "", "compiled plugin identity")
	flag.Parse()
	if flag.NArg() != 0 || *id != "AutoSignIn" {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if plugins.Serve(ctx, os.Stdin, os.Stdout, autosignin.Manifest(), autosignin.Handle) != nil {
		os.Exit(2)
	}
}
