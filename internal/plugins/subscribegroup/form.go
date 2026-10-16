package subscribegroup

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// Source form, including original labels and option shapes, is returned by the
// native handler without inventing a second configuration UI.
//
//go:embed form.json
var sourceForm json.RawMessage

func Form() json.RawMessage { return slices.Clone(sourceForm) }
