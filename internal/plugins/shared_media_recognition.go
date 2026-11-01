package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

const MediaRecognizeCapability = "media.recognize"

type SharedMediaRecognition struct{ Host HostCaller }

// RecognizeMediaRequest names exactly ChainBase.recognize_media's arguments.
// Nil RawMessage means omitted, not explicit JSON null. Neither numeric values
// nor source defaults pass through a float64 intermediate in this SDK.
// This first vertical slice admits only omitted/null Meta and ShareMeta. A
// future non-null Meta extension needs an explicit mutation-state roundtrip.
type RecognizeMediaRequest struct {
	Meta         json.RawMessage `json:"meta,omitempty"`
	MType        json.RawMessage `json:"mtype,omitempty"`
	Source       json.RawMessage `json:"source,omitempty"`
	MediaID      json.RawMessage `json:"mediaid,omitempty"`
	TMDBID       json.RawMessage `json:"tmdbid,omitempty"`
	DoubanID     json.RawMessage `json:"doubanid,omitempty"`
	BangumiID    json.RawMessage `json:"bangumiid,omitempty"`
	AniListID    json.RawMessage `json:"anilistid,omitempty"`
	EpisodeGroup json.RawMessage `json:"episode_group,omitempty"`
	Cache        json.RawMessage `json:"cache,omitempty"`
	ShareMeta    json.RawMessage `json:"share_meta,omitempty"`
}

// RecognizeMedia makes one current-invocation callback, without retry. A nil
// result and nil error are source None; an object preserves all returned raw
// MediaInfo fields. The host, not this wire adapter, implements source ID
// precedence, module order, actual fresh/cache behavior and full model mapping.
func (s SharedMediaRecognition) RecognizeMedia(ctx context.Context, input RecognizeMediaRequest) (*ModuleMediaInfo, error) {
	if ctx == nil || s.Host == nil {
		return nil, errors.New("media recognition requires an active context and host caller")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, raw := range []json.RawMessage{
		input.Meta, input.MType, input.Source, input.MediaID, input.TMDBID,
		input.DoubanID, input.BangumiID, input.AniListID, input.EpisodeGroup,
		input.Cache, input.ShareMeta,
	} {
		if raw == nil {
			continue
		}
		// Reuse the existing ordinary JSON validator, including duplicate-key
		// detection. Do not decode numbers or execute user marshalers.
		if len(raw) > 128<<10 || downloaderCommandJSON(raw) != nil {
			return nil, errors.New("invalid media recognition argument JSON")
		}
	}
	for _, raw := range []json.RawMessage{input.Meta, input.ShareMeta} {
		if raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, errors.New("media recognition currently requires null metadata")
		}
	}
	request, err := json.Marshal(struct {
		Args RecognizeMediaRequest `json:"args"`
	}{input})
	if err != nil {
		return nil, err
	}
	if len(request) > 128<<10 {
		return nil, errors.New("media recognition input exceeds host limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := s.Host(ctx, MediaRecognizeCapability, request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fields, err := downloaderCommandObject(raw, "value")
	if err != nil {
		return nil, errors.New("invalid media recognition result envelope")
	}
	value := bytes.TrimSpace(fields["value"])
	if bytes.Equal(value, []byte("null")) {
		return nil, nil
	}
	if len(value) == 0 || value[0] != '{' {
		return nil, errors.New("media recognition expected MediaInfo or null")
	}
	// Validate only the keys we materialize as Go strings. Raw field values,
	// including escaped source code points and exact numbers, stay untouched.
	// The enclosing ordinary JSON validator already rejected nested duplicates.
	d := json.NewDecoder(bytes.NewReader(value))
	_, _ = d.Token() // object opening already validated
	model := &ModuleMediaInfo{Fields: make(map[string]json.RawMessage)}
	for d.More() {
		start := d.InputOffset()
		key, err := d.Token()
		if err != nil {
			return nil, errors.New("invalid MediaInfo field name")
		}
		name, ok := key.(string)
		keyRaw := bytes.TrimLeft(value[start:d.InputOffset()], ", \t\r\n")
		if !ok || ValidateSystemConfigUTF8JSON(keyRaw) != nil {
			return nil, errors.New("MediaInfo field name is not lossless Unicode")
		}
		var field json.RawMessage
		if err := d.Decode(&field); err != nil {
			return nil, errors.New("invalid MediaInfo field value")
		}
		model.Fields[name] = field
	}
	return model, nil
}
