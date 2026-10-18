package iyuuautoseed

import (
	_ "embed"
	"encoding/json"
	"errors"
)

// form.json is the actual installed get_form execution with empty inventory;
// only the three inventory-driven items lists are replaced at request time.
//
//go:embed form.json
var formTemplate []byte

type SiteOption struct {
	ID   any `json:"id"`
	Name any `json:"name"`
}
type FormInventory struct {
	Sites       []SiteOption
	CustomSites []SiteOption
	Downloaders []string
}

func Form(inventory FormInventory) ([]any, map[string]any, error) {
	v, e := decodeJSON(formTemplate)
	if e != nil {
		return nil, nil, e
	}
	m := v.(map[string]any)
	form := m["form"].([]any)
	defaults := m["defaults"].(map[string]any)
	sites := []any{}
	for _, s := range append(append([]SiteOption{}, inventory.Sites...), inventory.CustomSites...) {
		sites = append(sites, map[string]any{"title": s.Name, "value": s.ID})
	}
	downloaders := []any{}
	for _, name := range inventory.Downloaders {
		downloaders = append(downloaders, map[string]any{"title": name, "value": name})
	}
	var visit func(any) error
	count := 0
	visit = func(value any) error {
		switch x := value.(type) {
		case map[string]any:
			if p, ok := x["props"].(map[string]any); ok {
				switch p["model"] {
				case "sites":
					p["items"] = sites
					count++
				case "downloaders", "auto_downloader":
					p["items"] = downloaders
					count++
				}
			}
			for _, v := range x {
				if e := visit(v); e != nil {
					return e
				}
			}
		case []any:
			for _, v := range x {
				if e := visit(v); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := visit(form); e != nil {
		return nil, nil, e
	}
	if count != 3 {
		return nil, nil, errors.New("form inventory contract changed")
	}
	// Return an independent tree even when the same mutable inventory was used.
	raw, e := json.Marshal(map[string]any{"form": form, "defaults": defaults})
	if e != nil {
		return nil, nil, e
	}
	copy, e := decodeJSON(raw)
	if e != nil {
		return nil, nil, e
	}
	out := copy.(map[string]any)
	return out["form"].([]any), out["defaults"].(map[string]any), nil
}
func Commands() any { return nil }
func Routes() any   { return nil }
func Page() any     { return nil }
