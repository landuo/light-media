package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type fileBinding struct {
	ItemGUID     string `json:"item_guid"`
	RelativePath string `json:"relative_path"`
	ActualPath   string `json:"actual_path"`
}
type fileEvidence struct {
	ExperimentID string        `json:"experiment_id"`
	OwnedGUID    string        `json:"owned_guid"`
	Root         string        `json:"root"`
	Bindings     []fileBinding `json:"bindings"`
}

var canonicalFiles = map[string]string{
	"夜王 (2026)/夜王 (2026).mkv":         "Movie",
	"生活大爆炸/Season 7/生活大爆炸 S07E17.mkv": "Episode",
	"诛仙/Season 4/诛仙 S04E05.mkv":       "Episode",
}
var decimalSourceID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var opaqueSourceID = regexp.MustCompile(`^(tm|tt|te)[1-9][0-9]{0,18}$`)

// Keep observed ID spelling/types. A prefix is never converted to a TMDB ID.
func observedSourceFields(detail map[string]json.RawMessage) map[string]any {
	out := map[string]any{}
	if raw, exists := detail["source"]; exists {
		switch s := stringField(detail, "source"); s {
		case "tmdb", "themoviedb", "tmdb_id", "trim_id":
			out["source"] = s
		default:
			out["source_state"] = rawKind(raw)
		}
	}
	for _, field := range []string{"tmdb_id", "source_id"} {
		raw, exists := detail[field]
		if !exists {
			continue
		}
		var n int64
		if json.Unmarshal(raw, &n) == nil && n > 0 {
			out[field] = n
			continue
		}
		value := stringField(detail, field)
		if decimalSourceID.MatchString(value) {
			if _, err := strconv.ParseInt(value, 10, 64); err == nil {
				out[field] = value
				continue
			}
		} else if field == "source_id" && opaqueSourceID.MatchString(value) {
			out[field] = value
			continue
		}
		out[field+"_state"] = rawKind(raw)
	}
	return out
}

func rawKind(raw json.RawMessage) string {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return "invalid"
	}
	switch b[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	default:
		return "number"
	}
}

var filePathFields = map[string]bool{"path": true, "file_path": true, "filepath": true, "full_path": true, "actual_path": true}
var fileContainers = map[string]bool{"files": true, "file_list": true, "media_files": true, "media_list": true, "media": true, "media_stream": true}
var fileShapeFields = map[string]bool{"guid": true, "item_guid": true, "ancestor_guid": true, "parent_guid": true, "mdb_guid": true, "type": true, "media_guid": true, "file_guid": true, "filename": true, "file_name": true, "name": true}

// The installed client exposes GET /media/itemfile/:guid and its caller reads
// an array's file_path values for an item GUID, not a library or playback GUID.
func (a *api) linkedFiles(ctx context.Context, r receipt, n observedNode) (map[string]any, []string, error) {
	var rows []map[string]json.RawMessage
	if err := a.call(ctx, "GET", "/media/itemfile/"+n.guid, nil, &rows); err != nil {
		return nil, nil, err
	}
	shape := map[string]any{"kind": "array", "count": len(rows)}
	if rows == nil || len(rows) > 4 {
		return shape, nil, errors.New("FILE_DETAIL_SHAPE_LIMIT")
	}
	entries := []map[string]any{}
	paths := []string{}
	allPathsPresent := true
	for _, row := range rows {
		for _, key := range []string{"parent_guid", "item_type"} {
			if _, exists := row[key]; !exists {
				continue
			}
			want := n.parent
			if n.depth == 0 {
				want = ""
			}
			if key == "item_type" {
				want = n.kind
			}
			if stringField(row, key) != want {
				return shape, nil, errors.New("FILE_DETAIL_ITEM_SCOPE_MISMATCH")
			}
		}
		entry, rowPaths, err := inspectFileShape(row, r, n.guid, 0)
		if err != nil {
			return shape, nil, err
		}
		entries, paths = append(entries, entry), append(paths, rowPaths...)
		if stringField(row, "file_path") == "" {
			allPathsPresent = false
		}
	}
	shape["entries"] = entries
	if len(rows) != 1 || !allPathsPresent {
		return shape, nil, nil // Multiple attachments cannot identify one file.
	}
	return shape, paths, nil
}

// Inspect only bounded known shape/path categories. Unknown fields and all
// scalar values other than accepted paths/IDs are never returned in diagnostics.
func inspectFileShape(m map[string]json.RawMessage, r receipt, itemGUID string, depth int) (map[string]any, []string, error) {
	if depth > 3 || len(m) > 80 {
		return nil, nil, errors.New("FILE_DETAIL_SHAPE_LIMIT")
	}
	shape := map[string]any{}
	paths := []string{}
	for _, key := range []string{"ancestor_guid", "mdb_guid", "item_guid"} {
		if _, exists := m[key]; exists {
			want := r.OwnedGUID
			if key == "item_guid" {
				want = itemGUID
			}
			if stringField(m, key) != want {
				return nil, nil, errors.New("FILE_DETAIL_ITEM_SCOPE_MISMATCH")
			}
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw := m[key]
		if !filePathFields[key] && !fileContainers[key] && !fileShapeFields[key] {
			continue
		}
		entry := map[string]any{"kind": rawKind(raw)}
		shape[key] = entry
		if filePathFields[key] {
			var value string
			if json.Unmarshal(raw, &value) != nil || value == "" {
				continue
			}
			if !validAbsolutePath(value) || path.Clean(value) != value || !strings.HasPrefix(value, r.Root+"/") {
				return shape, nil, errors.New("FILE_PATH_SCOPE_MISMATCH")
			}
			relative := strings.TrimPrefix(value, r.Root+"/")
			if canonicalFiles[relative] == "" || value != r.Root+"/"+relative {
				return shape, nil, errors.New("FILE_PATH_SCOPE_MISMATCH")
			}
			paths = append(paths, value) // Returned backend string, not reconstructed.
			entry["owned_canonical_path"] = true
		}
		if fileContainers[key] {
			var child map[string]json.RawMessage
			if json.Unmarshal(raw, &child) == nil && child != nil {
				childShape, childPaths, err := inspectFileShape(child, r, itemGUID, depth+1)
				if err != nil {
					return shape, nil, err
				}
				entry["fields"], paths = childShape, append(paths, childPaths...)
				continue
			}
			var list []map[string]json.RawMessage
			if json.Unmarshal(raw, &list) == nil && list != nil {
				if len(list) > 4 {
					return shape, nil, errors.New("FILE_DETAIL_SHAPE_LIMIT")
				}
				entry["count"] = len(list)
				children := []map[string]any{}
				for _, item := range list {
					childShape, childPaths, err := inspectFileShape(item, r, itemGUID, depth+1)
					if err != nil {
						return shape, nil, err
					}
					children, paths = append(children, childShape), append(paths, childPaths...)
				}
				entry["entries"] = children
			}
		}
	}
	return shape, paths, nil
}

func (a *api) inspectFiles(ctx context.Context, r receipt) ([]map[string]any, *fileEvidence, error) {
	if !uuidPattern.MatchString(r.ExperimentID) || !guidPattern.MatchString(r.OwnedGUID) || r.Root != path.Join(parentRoot, prefix+r.ExperimentID) {
		return nil, nil, errors.New("INVALID_FILE_INSPECTION_SCOPE")
	}
	items, err := a.observe(ctx, r)
	if err != nil {
		return items, nil, err // No file-detail read before the entire tree is scoped.
	}
	evidence := &fileEvidence{ExperimentID: r.ExperimentID, OwnedGUID: r.OwnedGUID, Root: r.Root, Bindings: []fileBinding{}}
	targets := 0
	for _, item := range items {
		if item["type"] == "Movie" || item["type"] == "Episode" {
			targets++
		}
	}
	if targets > 3 {
		return items, nil, errors.New("FILE_INSPECT_ITEM_LIMIT")
	}
	seenPaths := map[string]bool{}
	for _, item := range items {
		kind, _ := item["type"].(string)
		if kind != "Movie" && kind != "Episode" {
			continue
		}
		guid, _ := item["guid"].(string)
		var detail map[string]json.RawMessage
		if err = a.call(ctx, "POST", "/item/getEditDetail", map[string]string{"item_guid": guid}, &detail); err != nil {
			return items, nil, err
		}
		for _, key := range []string{"guid", "item_guid", "ancestor_guid", "mdb_guid", "parent_guid", "type"} {
			if _, exists := detail[key]; !exists {
				continue
			}
			want := item[key]
			if key == "item_guid" {
				want = guid
			} else if key == "mdb_guid" {
				want = r.OwnedGUID
			}
			if stringField(detail, key) != want {
				return items, nil, errors.New("FILE_DETAIL_ITEM_SCOPE_MISMATCH")
			}
		}
		shape, paths, err := inspectFileShape(detail, r, guid, 0)
		item["file_detail_shape"] = shape
		item["file_binding_status"] = "unverified"
		if err != nil {
			return items, nil, err
		}
		unique := map[string]bool{}
		for _, value := range paths {
			unique[value] = true
		}
		if len(unique) != 1 {
			// getEditDetail may expose metadata editing fields only. Re-read
			// the already proven detail route, never guess another endpoint.
			depth, depthOK := item["tree_depth"].(int)
			parent, parentOK := item["parent_guid"].(string)
			if !depthOK || !parentOK || item["scope_verified"] != true {
				return items, nil, errors.New("INVALID_FILE_INSPECTION_SCOPE")
			}
			if depth == 0 {
				parent = r.OwnedGUID
			}
			n := observedNode{guid: guid, parent: parent, kind: kind, depth: depth}
			var current map[string]json.RawMessage
			if err = a.call(ctx, "GET", "/item/"+guid, nil, &current); err != nil {
				return items, nil, err
			}
			if err = verifyItemScope(current, r, n); err != nil {
				item["file_fallback_scope"] = scopeDiagnostic(current, n)
				return items, nil, err
			}
			item["file_fallback_scope_verified"] = true
			stream := map[string]json.RawMessage{}
			if raw, exists := current["media_stream"]; exists {
				stream["media_stream"] = raw
			}
			fallbackShape, fallbackPaths, err := inspectFileShape(stream, r, guid, 0)
			item["fallback_item_file_shape"] = fallbackShape
			if err != nil {
				return items, nil, err
			}
			unique = map[string]bool{}
			for _, value := range fallbackPaths {
				unique[value] = true
			}
			if len(unique) != 1 {
				lastShape, lastPaths, err := a.linkedFiles(ctx, r, n)
				item["media_itemfile_shape"] = lastShape
				if err != nil {
					return items, nil, err
				}
				unique = map[string]bool{}
				for _, value := range lastPaths {
					unique[value] = true
				}
				if len(unique) != 1 {
					continue // No backend proof; never invent an actual file path.
				}
			}
		}
		for value := range unique {
			relative := strings.TrimPrefix(value, r.Root+"/")
			if canonicalFiles[relative] != kind || seenPaths[value] {
				return items, nil, errors.New("FILE_BINDING_CONFLICT")
			}
			seenPaths[value] = true
			evidence.Bindings = append(evidence.Bindings, fileBinding{guid, relative, value})
			item["file_binding_status"] = "backend_path_observed"
		}
	}
	return items, evidence, nil
}

func completeFileEvidence(e *fileEvidence) bool {
	if e == nil || len(e.Bindings) != 3 {
		return false
	}
	seen := map[string]bool{}
	for _, binding := range e.Bindings {
		if canonicalFiles[binding.RelativePath] == "" || seen[binding.RelativePath] {
			return false
		}
		seen[binding.RelativePath] = true
	}
	return true
}
