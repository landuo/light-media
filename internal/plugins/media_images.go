package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
)

const MediaImagesContract = "light-media.media-images/v1"

// MediaImagesSnapshot carries one concrete live MediaInfo input across the Go
// process boundary. Value is still the ordinary module return; the source
// caller separately observes mutations of Input, even when the callback fails.
type MediaImagesSnapshot struct {
	Contract    string          `json:"media_images_contract"`
	Input       json.RawMessage `json:"input"`
	Value       json.RawMessage `json:"value"`
	Failed      bool            `json:"handler_failed"`
	RateLimited bool            `json:"rate_limited"`
}

func HandleMediaImages(raw json.RawMessage, handle func(*ModuleMediaInfo) (json.RawMessage, error)) (json.RawMessage, error) {
	var wire struct {
		Args   []json.RawMessage `json:"args"`
		Kwargs struct {
			MediaInfo json.RawMessage `json:"mediainfo"`
		} `json:"kwargs"`
		Contract string `json:"media_images_contract"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Args == nil || len(wire.Args) != 0 || wire.Contract != MediaImagesContract || handle == nil {
		return nil, errors.New("invalid media images input")
	}
	model := &ModuleMediaInfo{}
	if json.Unmarshal(wire.Kwargs.MediaInfo, &model.Fields) != nil || model.Fields == nil {
		return nil, errors.New("media images requires one MediaInfo model")
	}
	value, callbackErr := handle(model)
	if len(value) == 0 {
		value = json.RawMessage(`null`)
	}
	if !json.Valid(value) {
		return nil, errors.New("invalid media images ordinary return")
	}
	input, err := json.Marshal(model.Fields)
	if err != nil || bytes.Equal(input, []byte(`null`)) {
		return nil, errors.New("invalid mutated MediaInfo fields")
	}
	return json.Marshal(MediaImagesSnapshot{Contract: MediaImagesContract, Input: input, Value: value, Failed: callbackErr != nil, RateLimited: errors.Is(callbackErr, ErrModuleRateLimited)})
}
