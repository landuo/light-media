// Package subscriptionstats ports the installed MoviePilotServerHelper's
// subscription statistics child call. The App owns asynchronous admission,
// sealed source identity, configured sharing preference and durable receipts.
package subscriptionstats

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var ErrDependency = errors.New("subscription_stats_native_dependency")

// requests' InvalidJSONError is a caught RequestException in the child thread.
var ErrInvalidJSON = errors.New("source_requests_invalid_json")

func CaughtJSONResult(err error) (Result, bool) {
	if errors.Is(err, ErrInvalidJSON) {
		return Result{SourceReturned: true, Outcome: "not_requested"}, true
	}
	return Result{}, false
}

// BuildCompletion requires trusted source-shaped Subscribe.to_dict and full
// MediaInfo attributes. Missing attributes are provenance errors, not invented
// None values. json.Number retains source JSON integer precision and spelling.
func BuildCompletion(subscribe, mediaInfo json.RawMessage) ([]byte, error) {
	sub, err := attributes(subscribe)
	if err != nil {
		return nil, err
	}
	media, err := attributes(mediaInfo)
	if err != nil {
		return nil, err
	}
	fields := []sourceField{}
	for _, f := range []struct{ target, source string }{
		{"tmdbid", "tmdb_id"}, {"doubanid", "douban_id"},
		{"bangumiid", "bangumi_id"}, {"anilistid", "anilist_id"},
	} {
		value, found := media[f.source]
		if !found {
			return nil, ErrDependency
		}
		fields = append(fields, sourceField{name: f.target, raw: value})
	}
	for _, key := range []string{"media_source", "media_id", "season"} {
		value, found := sub[key]
		if !found {
			return nil, ErrDependency
		}
		fields = append(fields, sourceField{name: key, raw: value})
	}
	return marshalSourceFields(fields)
}

// BuildRegistration accepts already evaluated original add() locals and image
// getters. It must not substitute a catalog Media projection for these values.
func BuildRegistration(sourceFields json.RawMessage) ([]byte, error) {
	values, err := attributes(sourceFields)
	if err != nil {
		return nil, err
	}
	fields := []sourceField{}
	for _, name := range []string{"name", "year", "type", "tmdbid", "imdbid", "tvdbid", "doubanid", "bangumiid", "anilistid", "media_source", "media_id", "season", "poster", "backdrop", "vote", "description"} {
		value, found := values[name]
		if !found {
			return nil, ErrDependency
		}
		fields = append(fields, sourceField{name: name, raw: value})
	}
	return marshalSourceFields(fields)
}

func attributes(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, ErrDependency
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value map[string]json.RawMessage
	if decoder.Decode(&value) != nil || value == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, ErrDependency
	}
	return value, nil
}
