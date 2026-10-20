package rsscontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/releasemeta"
	"time"
)

// Frame records GLOBAL defaults, not a precomputed effective local list. Revision
// distinguishes cache creation from later selection without reinterpreting either.
type Frame struct {
	Revision string                          `json:"revision"`
	Global   releasemeta.MetaInfoEnvironment `json:"global"`
}

func cloneFrame(f Frame) Frame {
	out := f
	copyList := func(in []string) []string {
		if in == nil {
			return nil
		}
		return append(make([]string, 0, len(in)), in...)
	}
	out.Global.Options.CustomWords = copyList(f.Global.Options.CustomWords)
	out.Global.Options.ReleaseGroups = copyList(f.Global.Options.ReleaseGroups)
	out.Global.Options.Customization = copyList(f.Global.Options.Customization)
	out.Global.Options.MediaExts = copyList(f.Global.Options.MediaExts)
	if f.Global.StreamingPlatforms != nil {
		out.Global.StreamingPlatforms = map[string]string{}
		for k, v := range f.Global.StreamingPlatforms {
			out.Global.StreamingPlatforms[k] = v
		}
	}
	return out
}

type LocalFrame struct {
	Present      bool           `json:"present"`
	Words        []string       `json:"words"`
	Decision     Frame          `json:"decision_frame"`
	Subscription map[string]any `json:"subscription"`
	Sites        []any          `json:"sites"`
	Domains      []string       `json:"domains"`
}

func localFrame(words *[]string, frame Frame) LocalFrame {
	f := LocalFrame{Decision: cloneFrame(frame)}
	if words != nil {
		f.Present = true
		if *words != nil {
			f.Words = append(make([]string, 0, len(*words)), (*words)...)
		}
	}
	return f
}

var contextDefaults = map[string]any{"media_recognize_fail_count": 0, "resource_source": "unknown", "match_source": "unknown", "candidate_recognized": false, "media_info_is_target": false, "allowed_episodes": nil, "confirmed_full_coverage": false}

// Context models the actual ten source fields. Field membership is separate from
// the dataclass defaults to retain the installed old-cache compatibility behavior.
type Context struct {
	Meta      *releasemeta.MetaObject
	Media     *MediaInfo
	Torrent   *TorrentInfo
	Fields    map[string]any
	MetaFrame Frame
	Selection *LocalFrame
}

func NewContext(meta *releasemeta.MetaObject, media *MediaInfo, torrent *TorrentInfo, frame Frame) *Context {
	return &Context{Meta: meta, Media: media, Torrent: torrent, Fields: deep(contextDefaults).(map[string]any), MetaFrame: cloneFrame(frame)}
}

// NewContextWithFields supplies the seven scalar/set constructor arguments in
// addition to the three complete native source objects. Unknown constructor
// keywords fail; explicit nil and the allowed-episode empty set stay present.
func NewContextWithFields(meta *releasemeta.MetaObject, media *MediaInfo, torrent *TorrentInfo, fields map[string]any, frame Frame) (*Context, error) {
	c := NewContext(meta, media, torrent, frame)
	for key, value := range fields {
		if _, ok := contextDefaults[key]; !ok {
			return nil, errors.New("TypeError")
		}
		c.Set(key, value)
	}
	return c, nil
}
func (c *Context) Get(k string) any {
	if v, ok := c.Fields[k]; ok {
		return v
	}
	return contextDefaults[k]
}
func (c *Context) Set(k string, v any) { c.Fields[k] = v }
func (c *Context) ShallowCopy() *Context {
	if c == nil {
		return nil
	}
	out := *c
	out.Fields = map[string]any{}
	for k, v := range c.Fields {
		out.Fields[k] = v
	}
	return &out
}
func (c *Context) DeepCopy() *Context {
	if c == nil {
		return nil
	}
	out := c.ShallowCopy()
	out.Meta = c.Meta.DeepCopy()
	out.Media = c.Media.DeepCopy()
	out.Torrent = c.Torrent.DeepCopy()
	out.Fields = deep(c.Fields).(map[string]any)
	out.MetaFrame = cloneFrame(c.MetaFrame)
	if c.Selection != nil {
		s := localFrame(c.Selection.WordsPointer(), c.Selection.Decision)
		if c.Selection.Subscription != nil {
			s.Subscription = deep(c.Selection.Subscription).(map[string]any)
		}
		if c.Selection.Sites != nil {
			s.Sites = deep(c.Selection.Sites).([]any)
		}
		if c.Selection.Domains != nil {
			s.Domains = append(make([]string, 0, len(c.Selection.Domains)), c.Selection.Domains...)
		}
		out.Selection = &s
	}
	return out
}
func (f LocalFrame) WordsPointer() *[]string {
	if !f.Present {
		return nil
	}
	words := f.Words
	return &words
}
func (c *Context) ToDict() (map[string]any, error) {
	return c.ToDictAt(time.Now())
}

// ToDictAt freezes the wall-clock input used by the source TorrentInfo property;
// it does not replace or reduce the native MetaInfo/MediaInfo/TorrentInfo objects.
func (c *Context) ToDictAt(now time.Time) (map[string]any, error) {
	var meta map[string]any
	var e error
	if c.Meta != nil {
		meta, e = c.Meta.ToDict()
		if e != nil {
			return nil, e
		}
	}
	torrent, e := c.Torrent.ToDictAt(now)
	if e != nil {
		return nil, e
	}
	media, e := c.Media.ToDict()
	if e != nil {
		return nil, e
	}
	out := map[string]any{"meta_info": meta, "media_info": media, "torrent_info": torrent}
	for k := range contextDefaults {
		out[k] = c.Get(k)
	}
	if v := out["allowed_episodes"]; v != nil {
		normalized, e := sortedEpisodeSet(v)
		if e != nil {
			return nil, e
		}
		out["allowed_episodes"] = normalized
	}
	return out, nil
}
func (c *Context) EnsureCompatibility(stype string) {
	if _, ok := c.Fields["media_recognize_fail_count"]; !ok {
		n := 0
		id := ResolveIdentity(c.Media)
		if c.Media == nil || id.Source == "" || id.ID == "" {
			n = 1
		}
		c.Set("media_recognize_fail_count", n)
	}
	if _, ok := c.Fields["resource_source"]; !ok {
		source := "rss"
		if stype == "spider" {
			source = "spider"
		}
		c.Set("resource_source", source)
	}
	if _, ok := c.Fields["candidate_recognized"]; !ok {
		id := ResolveIdentity(c.Media)
		c.Set("candidate_recognized", c.Media != nil && id.Source != "" && id.ID != "")
	}
	if _, ok := c.Fields["match_source"]; !ok {
		source := "unknown"
		if truth(c.Get("candidate_recognized")) {
			source = MediaMatchSource(c.Media)
		}
		c.Set("match_source", source)
	}
	if _, ok := c.Fields["media_info_is_target"]; !ok {
		c.Set("media_info_is_target", false)
	}
}

type envelope struct {
	Version         int             `json:"version"`
	Meta            json.RawMessage `json:"meta"`
	Media, Torrent  map[string]any
	MediaTypeIsEnum bool `json:"media_type_is_enum"`
	Fields          map[string]any
	MetaFrame       Frame       `json:"meta_frame"`
	Selection       *LocalFrame `json:"selection"`
}

func (c *Context) EncodeState() ([]byte, error) {
	if c == nil {
		return nil, errors.New("context_nil")
	}
	var meta []byte
	var e error
	if c.Meta != nil {
		meta, e = c.Meta.EncodeState()
		if e != nil {
			return nil, e
		}
	}
	var media, torrent map[string]any
	if c.Media != nil {
		media = c.Media.Fields
	}
	if c.Torrent != nil {
		torrent = c.Torrent.Fields
	}
	_, isEnum := media["type"].(MediaType)
	return json.Marshal(envelope{Version: 1, Meta: meta, Media: media, Torrent: torrent, MediaTypeIsEnum: isEnum, Fields: c.Fields, MetaFrame: c.MetaFrame, Selection: c.Selection})
}
func DecodeState(data []byte) (*Context, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	var x envelope
	if e := d.Decode(&x); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, errors.New("context_state_trailing")
	}
	if x.Version != 1 || x.Fields == nil {
		return nil, errors.New("context_state_invalid")
	}
	c := &Context{Fields: x.Fields, MetaFrame: cloneFrame(x.MetaFrame), Selection: x.Selection}
	if len(x.Meta) > 0 && string(x.Meta) != "null" {
		m, e := releasemeta.DecodeMetaObjectState(x.Meta)
		if e != nil {
			return nil, e
		}
		c.Meta = m
	}
	if x.Media != nil {
		if x.MediaTypeIsEnum {
			if text, ok := x.Media["type"].(string); ok {
				x.Media["type"] = MediaType(text)
			} else {
				return nil, errors.New("context_state_media_enum_invalid")
			}
		}
		c.Media = &MediaInfo{Fields: x.Media}
	}
	if x.Torrent != nil {
		c.Torrent = &TorrentInfo{Fields: x.Torrent}
	}
	return c, nil
}
