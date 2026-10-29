package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// JSON may encode one UTF-8 byte using six bytes (for example < as \u003c,
// or a control character as \u0001). The ordinary 128 KiB resource envelope
// would therefore reject valid policies below the unchanged 64 KiB content
// limit. Only this single-field policy resource admits the worst-case JSON
// expansion plus a small, fixed envelope allowance. This is not an increase
// to the generic resource, stdio, file, parser or decoded-content limits.
const maxCategoryApplyJSONBytes = 6*maxCategoryPolicyBytes + 64

type categoryEditorApplyInput struct {
	Content json.RawMessage `json:"content"`
}

func categoryEditorApplyDecode(raw json.RawMessage, output *categoryEditorApplyInput) error {
	if len(raw) == 0 || len(raw) > maxCategoryApplyJSONBytes || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return bad("INVALID_PLUGIN_VALUE", "插件资源参数必须为有界对象")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil {
		return bad("INVALID_PLUGIN_VALUE", "插件资源参数无效")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return bad("INVALID_PLUGIN_VALUE", "插件资源参数无效")
	}
	return nil
}
