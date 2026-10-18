package iyuuautoseed

import (
	"errors"
	"light-media/internal/plugins/cleaninvalidseed"
)

// ProjectIndexer is the consumed installed SitesHelper inner get_indexer
// operation after the host has authorized and resolved one domain. The host
// owns both the definition and selected DB row; neither accepts worker-issued
// credentials. This function does not contain or load the CPython extension.
// A missing/empty definition is returned as-is and cannot become an invented
// generic indexer solely because a DB row exists.
func ProjectIndexer(definition map[string]any, row map[string]any, rowPresent bool, defaultUA string) (map[string]any, error) {
	if definition == nil {
		return nil, nil
	}
	out, e := cloneMap(definition)
	if e != nil {
		return nil, e
	}
	if len(out) == 0 || !rowPresent {
		return out, nil
	}
	for key, value := range row {
		out[key] = value
	}
	if _, exists := row["domain"]; exists {
		value := row["url"]
		scheme, netloc := "", ""
		if truth(value) {
			s, ok := value.(string)
			if !ok {
				return nil, sourceFault("TypeError", "site URL")
			}
			parts, err := cleaninvalidseed.URLNetloc(s)
			if err != nil {
				return nil, sourceFault("ValueError", "site URL")
			}
			scheme, netloc = parts[0], parts[1]
		}
		out["domain"] = scheme + "://" + netloc + "/"
	}
	if !truth(out["ua"]) {
		out["ua"] = defaultUA
	}
	// Deep-copy again: a caller cannot modify either the original template or
	// the host's DB snapshot by changing a nested extension on the returned map.
	result, e := cloneMap(out)
	if e != nil {
		return nil, errors.New("invalid site projection")
	}
	return result, nil
}
