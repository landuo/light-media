package subscribegroup

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// Source form is retained for the native handler's eventual platform wiring;
// exposing this value does not register an executable plugin/catalog entry.
//
//go:embed form.json
var sourceForm json.RawMessage

func Form() json.RawMessage { return slices.Clone(sourceForm) }
