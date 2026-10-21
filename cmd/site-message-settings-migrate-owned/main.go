// site-message-settings-migrate-owned is a delivery utility, not a public API.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"time"

	"light-media/internal/app"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout))
}

func run(ctx context.Context, args []string, input io.Reader, output io.Writer) int {
	var dir string
	var apply bool
	var validate bool
	flags := flag.NewFlagSet("site-message-settings-migrate-owned", flag.ContinueOnError)
	// A malformed argument must not echo its content into a diagnostic log.
	flags.SetOutput(io.Discard)
	flags.StringVar(&dir, "target-data-dir", "", "existing stopped owned target data directory")
	flags.BoolVar(&apply, "apply", false, "atomically import the reviewed selected settings; default is read-only")
	flags.BoolVar(&validate, "validate-input", false, "validate private stdin only, without opening a target")
	if e := flags.Parse(args); e != nil || flags.NArg() != 0 || validate && (apply || dir != "") {
		json.NewEncoder(output).Encode(map[string]string{"state": "failed", "code": "invalid_site_message_settings_input"})
		return 1
	}
	raw, e := io.ReadAll(io.LimitReader(input, (64<<10)+1))
	if e != nil || len(raw) > 64<<10 {
		json.NewEncoder(output).Encode(map[string]string{"state": "failed", "code": "invalid_site_message_settings_input"})
		return 1
	}
	var result app.SourceSiteMessageImportResult
	if validate {
		result, e = app.ValidateReviewedSiteMessageSettingsInput(raw)
	} else {
		result, e = app.ImportReviewedSiteMessageSettings(ctx, dir, raw, apply)
	}
	if e != nil {
		// Importer errors contain fixed codes, never settings or SQL arguments.
		json.NewEncoder(output).Encode(map[string]string{"state": "failed", "code": e.Error()})
		return 1
	}
	if json.NewEncoder(output).Encode(result) != nil {
		return 1
	}
	return 0
}
