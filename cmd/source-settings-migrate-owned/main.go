// source-settings-migrate-owned is a delivery utility, not a public API.
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
	var dir string
	var apply bool
	var validate bool
	flag.StringVar(&dir, "target-data-dir", "", "existing stopped owned target data directory")
	flag.BoolVar(&apply, "apply", false, "atomically import the reviewed selected settings; default is read-only")
	flag.BoolVar(&validate, "validate-input", false, "validate private stdin only, without opening a target")
	flag.Parse()
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || flag.NArg() != 0 || validate && (apply || dir != "") {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"state": "failed", "code": "invalid_source_settings_input"})
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result app.SourceSettingsImportResult
	if validate {
		result, e = app.ValidateReviewedSourceSettingsInput(raw)
	} else {
		result, e = app.ImportReviewedSourceSettings(ctx, dir, raw, apply)
	}
	if e != nil {
		// Importer errors contain fixed codes, never settings or SQL arguments.
		json.NewEncoder(os.Stdout).Encode(map[string]string{"state": "failed", "code": e.Error()})
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(result)
}
