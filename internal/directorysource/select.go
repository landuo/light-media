package directorysource

import (
	"context"
	"encoding/json"
	"math/big"
	"regexp"
	"sort"
	"strings"
)

type Media struct {
	Type          string
	Category      *string
	CategoryKnown bool
}
type Query struct {
	Media                                        *Media
	IncludeUnsorted                              bool
	Storage, SourcePath, TargetStorage, DestPath *string
}
type SameDisk func(context.Context, string, string) (bool, error)

func matchMedia(r Rule, m *Media) (bool, error) {
	if m == nil || m.Type == "" || !truth(r.Values["media_type"]) {
		return true, nil
	}
	if text(r.Values["media_type"]) != m.Type {
		return false, nil
	}
	if !truth(r.Values["media_category"]) {
		return true, nil
	}
	if !m.CategoryKnown {
		return false, ErrDependency
	}
	return m.Category != nil && text(r.Values["media_category"]) == *m.Category, nil
}

// GetDir keeps original configuration order. Priority sorting belongs to the
// separate download/library list methods, never to this selection method.
func GetDir(ctx context.Context, known bool, rules []Rule, q Query, same SameDisk) (*Rule, error) {
	if !known {
		return nil, ErrDependency
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	considered := rules
	var src *sourcePath
	if q.SourcePath != nil {
		p, _ := parsePath(*q.SourcePath)
		src = &p
		matching := []Rule{}
		for _, r := range rules {
			root, e := parsePath(r.Values["download_path"])
			if e != nil {
				return nil, e
			}
			if p.under(root) {
				matching = append(matching, r)
			}
		}
		if len(matching) > 0 {
			considered = matching
		}
	}
	matches := []Rule{}
	for _, r := range considered {
		if !truth(r.Values["monitor_type"]) && !q.IncludeUnsorted {
			continue
		}
		if q.Storage != nil && *q.Storage != "" && text(r.Values["storage"]) != *q.Storage {
			continue
		}
		if q.TargetStorage != nil && *q.TargetStorage != "" && text(r.Values["library_storage"]) != *q.TargetStorage {
			continue
		}
		if q.DestPath != nil {
			want, _ := parsePath(*q.DestPath)
			path, e := parsePath(r.Values["library_path"])
			if e != nil {
				return nil, e
			}
			if !want.equal(path) {
				continue
			}
		}
		ok, e := matchMedia(r, q.Media)
		if e != nil {
			return nil, e
		}
		if ok {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if src != nil {
		storage := "local"
		if q.Storage != nil && *q.Storage != "" {
			storage = *q.Storage
		}
		for _, r := range matches {
			target, e := parsePath(r.Values["download_path"])
			if e != nil {
				return nil, e
			}
			targetStorage := text(r.Values["library_storage"])
			yes := targetStorage == storage
			if targetStorage == "local" && storage == "local" {
				if same == nil {
					return nil, ErrDependency
				}
				yes, e = same(ctx, src.String(), target.String())
				if e != nil {
					return nil, e
				}
			}
			if yes {
				v := r
				return &v, nil
			}
		}
	}
	return &matches[0], nil
}
func List(known bool, rules []Rule, library, local bool) ([]Rule, error) {
	if !known {
		return nil, ErrDependency
	}
	key, storage := "download_path", "storage"
	if library {
		key, storage = "library_path", "library_storage"
	}
	out := []Rule{}
	for _, r := range rules {
		if truth(r.Values[key]) {
			out = append(out, r)
		}
	}
	if len(out) > 1 {
		for _, r := range out {
			n, ok := r.Values["priority"].(json.Number)
			if !ok {
				return nil, sourceType("priority None or invalid original integer")
			}
			if _, ok = new(big.Int).SetString(n.String(), 10); !ok {
				return nil, sourceType("priority")
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := new(big.Int).SetString(out[i].Values["priority"].(json.Number).String(), 10)
		b, _ := new(big.Int).SetString(out[j].Values["priority"].(json.Number).String(), 10)
		return a.Cmp(b) < 0
	})
	if local {
		filtered := []Rule{}
		for _, r := range out {
			if text(r.Values[storage]) == "local" {
				filtered = append(filtered, r)
			}
		}
		out = filtered
	}
	return out, nil
}

var jinja = regexp.MustCompile(`(?s)\{\{.*?}}`)

func MediaRoot(format *string, rawPath string) (*string, error) {
	if format == nil || *format == "" {
		return nil, nil
	}
	p, _ := parsePath(rawPath)
	parts := strings.Split(*format, "/")
	level := len(parts) - 1
	for n := 1; n < len(parts); n++ {
		name := parts[len(parts)-1-n]
		title, season := false, false
		for _, m := range jinja.FindAllString(name, -1) {
			title = title || strings.Contains(m, "title")
			season = season || strings.Contains(m, "season")
		}
		if title && !season {
			level = n
			goto found
		}
	}
found:
	parents := p.parents()
	if level > len(parents) {
		return nil, nil
	}
	if level <= 0 {
		level = 1
	}
	if level > len(parents) {
		return nil, &SourceError{Kind: "IndexError", Message: "tuple index out of range"}
	}
	v := parents[level-1].String()
	return &v, nil
}
