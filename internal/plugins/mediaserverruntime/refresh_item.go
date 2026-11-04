package mediaserverruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"math/big"
)

// RefreshMediaType is the identity of the original plain Enum, not its string
// .value. In particular Movie is NOT equal to the ordinary string "电影".
type RefreshMediaType uint8

const (
	RefreshMediaTypeNone RefreshMediaType = iota
	RefreshMediaTypeMovie
	RefreshMediaTypeTV
	RefreshMediaTypeCollection
	RefreshMediaTypeUnknown
)

// ValidatedRefreshItem carries the FIVE fields of an already validated original
// RefreshMediaItem model. This is not a JSON/SDK/Pydantic admission endpoint.
// Empty RawMessage denotes None. Title/Category/TargetPath are optional source
// strings; TargetPath is the lexical PosixPath spelling. Year is an optional
// string or arbitrary-precision integer. No filesystem operation is permitted.
// Future callers must perform original model admission before creating it.
type ValidatedRefreshItem struct {
	Title, Year, Category, TargetPath json.RawMessage
	Type                              RefreshMediaType
}

// OwnedCopy checks the normalized carrier contract and detaches caller slices.
// An invalid carrier is a platform integration error, not a source exception.
func (v ValidatedRefreshItem) OwnedCopy() (ValidatedRefreshItem, error) {
	if v.Type > RefreshMediaTypeUnknown {
		return ValidatedRefreshItem{}, errors.New("invalid validated refresh enum")
	}
	out := v
	fields := []*json.RawMessage{&out.Title, &out.Year, &out.Category, &out.TargetPath}
	for index, field := range fields {
		raw := bytes.TrimSpace(*field)
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			*field = json.RawMessage("null")
			continue
		}
		if !json.Valid(raw) {
			return ValidatedRefreshItem{}, errors.New("invalid validated refresh JSON")
		}
		if raw[0] == '"' {
			if _, err := plugins.DecodeSystemConfigJSONString(raw); err != nil {
				return ValidatedRefreshItem{}, errors.New("invalid validated refresh text")
			}
		} else if index == 1 && !bytes.ContainsAny(raw, ".eE") {
			if _, ok := new(big.Int).SetString(string(raw), 10); !ok {
				return ValidatedRefreshItem{}, errors.New("invalid validated refresh year")
			}
		} else {
			return ValidatedRefreshItem{}, errors.New("invalid validated refresh field type")
		}
		*field = append(json.RawMessage(nil), raw...)
	}
	return out, nil
}
