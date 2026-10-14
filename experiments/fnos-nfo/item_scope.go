package main

import (
	"bytes"
	"encoding/json"
	"errors"
)

// A list-filter is not ownership evidence. Require the detail endpoint to assert
// the actual ancestor and immediate parent before reading persons/images/children.
// Live root TV diagnostics returned an explicit empty parent_guid with the
// library in ancestor_guid; public UI navigation uses media parents only for
// descendants. Root Movie/TV/Video therefore require an explicit empty parent,
// while Season/Episode require the exact nonempty media parent. Never substitute
// traversal-inferred values for backend fields.
func verifyItemScope(d map[string]json.RawMessage, r receipt, n observedNode) error {
	if stringField(d, "guid") != n.guid {
		return errors.New("ITEM_ID_MISMATCH")
	}
	kind := stringField(d, "type")
	if n.depth == 0 {
		if kind != "Movie" && kind != "TV" && kind != "Video" {
			return errors.New("ITEM_TYPE_MISMATCH")
		}
	} else if (n.depth == 1 && kind != "Season") || (n.depth == 2 && kind != "Episode") || n.depth > 2 {
		return errors.New("ITEM_TYPE_MISMATCH")
	}
	if n.kind != "" && kind != n.kind {
		return errors.New("ITEM_TYPE_MISMATCH")
	}
	if stringField(d, "ancestor_guid") != r.OwnedGUID {
		return errors.New("ITEM_ANCESTOR_MISMATCH")
	}
	state, parent := guidFieldState(d, "parent_guid")
	if n.depth == 0 {
		if state != "empty_string" {
			return errors.New("ITEM_PARENT_MISMATCH")
		}
	} else if state != "valid_guid" || parent != n.parent || !guidPattern.MatchString(n.parent) {
		return errors.New("ITEM_PARENT_MISMATCH")
	}
	return nil
}
func scopeDiagnostic(d map[string]json.RawMessage, n observedNode) map[string]any {
	expected := n.parent
	if n.depth == 0 {
		expected = ""
	}
	m := map[string]any{"scope_verified": false, "requested_guid": n.guid, "expected_parent_guid": expected}
	for _, k := range []string{"guid", "ancestor_guid", "parent_guid"} {
		state, value := guidFieldState(d, k)
		m[k+"_state"] = state
		if state == "valid_guid" {
			m[k] = value
		}
	}
	switch s := stringField(d, "type"); s {
	case "Movie", "TV", "Video", "Season", "Episode":
		m["type"] = s
	default:
		m["type_state"] = "missing_or_invalid"
	}
	return m
}

// Return only an accepted GUID value; malformed strings never enter output.
func guidFieldState(d map[string]json.RawMessage, k string) (state, value string) {
	raw, ok := d[k]
	if !ok {
		return "absent", ""
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null", ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "nonstring", ""
	}
	if s == "" {
		return "empty_string", ""
	}
	if !guidPattern.MatchString(s) {
		return "invalid_string", ""
	}
	return "valid_guid", s
}
