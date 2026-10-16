package rsscontext

import (
	"bytes"
	"encoding/json"
	"light-media/internal/releasemeta"
)

// Freeze every list even when empty. Options' application JSON omitempty tags
// would otherwise collapse a present empty global list into null on persistence.
type frameOptions struct {
	SourceMode    string   `json:"source_mode"`
	CustomWords   []string `json:"custom_words"`
	ReleaseGroups []string `json:"release_groups"`
	Customization []string `json:"customization"`
	MediaExts     []string `json:"media_exts"`
}
type frameWire struct {
	Revision string `json:"revision"`
	Global   struct {
		Options            frameOptions      `json:"options"`
		StreamingPlatforms map[string]string `json:"streaming_platforms"`
	} `json:"global"`
}

func (f Frame) MarshalJSON() ([]byte, error) {
	var wire frameWire
	wire.Revision = f.Revision
	o := f.Global.Options
	wire.Global.Options = frameOptions{o.SourceMode, o.CustomWords, o.ReleaseGroups, o.Customization, o.MediaExts}
	wire.Global.StreamingPlatforms = f.Global.StreamingPlatforms
	return json.Marshal(wire)
}
func (f *Frame) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var wire frameWire
	if err := d.Decode(&wire); err != nil {
		return err
	}
	o := wire.Global.Options
	*f = Frame{Revision: wire.Revision, Global: releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: o.SourceMode, CustomWords: o.CustomWords, ReleaseGroups: o.ReleaseGroups, Customization: o.Customization, MediaExts: o.MediaExts}, StreamingPlatforms: wire.Global.StreamingPlatforms}}
	return nil
}
