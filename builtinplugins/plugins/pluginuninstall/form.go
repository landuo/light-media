package pluginuninstall

import (
	_ "embed"
	"encoding/json"
)

//go:embed form.json
var sourceForm json.RawMessage

// Form preserves the literal original form and replaces only the runtime
// installed-plugin options returned by get_local_plugins.
func Form(options []Option) (json.RawMessage, error) {
	if options == nil {
		options = []Option{}
	}
	var form struct {
		Conf  []map[string]any `json:"conf"`
		Model map[string]any   `json:"model"`
	}
	if err := json.Unmarshal(sourceForm, &form); err != nil {
		return nil, err
	}
	var visit func(any)
	visit = func(node any) {
		switch n := node.(type) {
		case []any:
			for _, child := range n {
				visit(child)
			}
		case map[string]any:
			if props, ok := n["props"].(map[string]any); ok && props["model"] == "plugin_ids" {
				props["items"] = options
			}
			visit(n["content"])
		}
	}
	for _, node := range form.Conf {
		visit(node)
	}
	return json.Marshal(form)
}
