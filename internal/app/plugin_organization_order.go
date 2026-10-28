package app

import (
	"bytes"
	"encoding/json"
	"sort"
)

func originalFolderInputKeys(raw json.RawMessage) []string {
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	keys := []string{}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil
		}
		key, ok := token.(string)
		if !ok {
			return nil
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil
		}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys
}

func originalFolderKeys(org PluginOrganization) []string {
	keys := []string{}
	seen := map[string]bool{}
	for _, key := range org.folderKeys {
		if _, exists := org.Folders[key]; exists && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	missing := []string{}
	for key := range org.Folders {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	// Old rows and programmatic Go maps have no original insertion metadata.
	// Use their stable encoding order; new HTTP writes preserve exact input order.
	sort.Strings(missing)
	return append(keys, missing...)
}

func originalFolderJSON(org PluginOrganization) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range originalFolderKeys(org) {
		if i > 0 {
			out.WriteByte(',')
		}
		quoted, _ := json.Marshal(key)
		out.Write(quoted)
		out.WriteByte(':')
		out.Write(org.Folders[key])
	}
	out.WriteByte('}')
	return out.Bytes()
}

func originalOrganizationHTTPValue(org PluginOrganization, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	order := org.Order
	if len(bytes.TrimSpace(order)) == 0 || bytes.TrimSpace(order)[0] != '[' {
		order = json.RawMessage(`[]`)
	}
	return map[string]any{"revision": org.Revision, "folders": originalFolderJSON(org), "order": order}, nil
}
