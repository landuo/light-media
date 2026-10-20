// Package subscriptiondefaults ports SubscribeChain's two original default
// methods over JSON-shaped settings. It does not fabricate global downloader,
// directory, quality or best-version defaults, or execute subscriptions.
package subscriptiondefaults

import (
	"encoding/json"
	"math/big"
)

type Object = map[string]any

type SourceError struct{ Class string }

func (e *SourceError) Error() string { return e.Class }

// Environment records attribute presence independently of its value: an
// existing None attribute hides the database value in the original method.
type Provider struct {
	Environment  Object
	ReadDatabase func(string) (any, error)
}

func truth(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		n, ok := new(big.Rat).SetString(string(v))
		return !ok || n.Sign() != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case *big.Int:
		return v != nil && v.Sign() != 0
	case []any:
		return len(v) > 0
	case []string:
		return len(v) > 0
	case Object:
		return len(v) > 0
	default:
		return true
	}
}

func (p Provider) Get(mediaType any, key string) (any, error) {
	var name string
	if mediaType == "电视剧" {
		name = "DefaultTvSubscribeConfig"
	}
	if mediaType == "电影" {
		name = "DefaultMovieSubscribeConfig"
	}
	if name == "" {
		return nil, &SourceError{Class: "TypeError"}
	}
	value, present := p.Environment[name]
	if !present && p.ReadDatabase != nil {
		var e error
		value, e = p.ReadDatabase(name)
		if e != nil {
			return nil, e
		}
	}
	if !truth(value) {
		return nil, nil
	}
	config, ok := value.(Object)
	if !ok {
		return nil, &SourceError{Class: "AttributeError"}
	}
	value = config[key]
	if !truth(value) {
		return nil, nil
	}
	return value, nil
}

// Apply preserves the original lazy evaluation order and the None-versus-False
// distinction for both best-version fields. It returns exactly the twelve
// source keys; SubscribeChain.add merges that result into its original kwargs.
func (p Provider) Apply(mediaType any, kwargs Object) (Object, error) {
	keys := []string{"quality", "resolution", "effect", "include", "exclude", "best_version", "best_version_full", "search_imdbid", "sites", "downloader", "save_path", "filter_groups"}
	out := Object{}
	for _, key := range keys {
		value := kwargs[key]
		fallback := !truth(value)
		if key == "best_version" || key == "best_version_full" {
			fallback = value == nil
		}
		if fallback {
			var e error
			value, e = p.Get(mediaType, key)
			if e != nil {
				return nil, e
			}
		}
		out[key] = value
	}
	return out, nil
}
