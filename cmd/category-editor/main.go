// CategoryEditor is a persistent standard SDK executable, not a native adapter.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"light-media/internal/plugins"
	"light-media/internal/plugins/categoryeditor"
)

func main() {
	manifest := categoryeditor.Manifest()
	if err := manifest.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	plugin := categoryeditor.New()
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, plugin.Handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
