// Package fileextensions resolves precisely the original three extension inputs.
// It does not read the caller's environment or infer an original application's
// values from a different process. The host must supply explicit observations.
package fileextensions

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	jinjaexec "light-media/internal/jinja/exec"
)

const MediaKey = "RMT_MEDIAEXT"
const SubtitleKey = "RMT_SUBEXT"
const AudioKey = "RMT_AUDIOEXT"

func Keys() []string { return []string{MediaKey, SubtitleKey, AudioKey} }

var ErrDependency = errors.New("extension configuration provenance is unknown")
var ErrSourceValidation = errors.New("original extension list failed validation")
var ErrUnsupportedMember = errors.New("original extension list contains unsupported non-string members")

//go:embed defaults.json
var defaultData []byte

type Inputs struct {
	Namespace          string `json:"namespace"`
	SourceID           string `json:"source_id"`
	EnvironmentKnown   bool   `json:"environment_known"`
	ConfigurationKnown bool   `json:"configuration_known"`
	// Presence includes an explicitly empty value; maps must not be reconstructed
	// from a different process when replaying an original captured source input.
	Environment   map[string]string          `json:"environment"`
	Configuration map[string]json.RawMessage `json:"configuration"`
}
type Field struct {
	Origin string          `json:"origin"`
	Raw    json.RawMessage `json:"raw"`
	Values []string        `json:"values"`
}
type Snapshot struct {
	Version          int              `json:"version"`
	Inputs           Inputs           `json:"inputs"`
	DefaultSourceSHA string           `json:"default_source_sha"`
	InputSHA         string           `json:"input_sha"`
	Fields           map[string]Field `json:"fields"`
}

func decode(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	var tail any
	if e := d.Decode(&tail); e != io.EOF {
		return nil, ErrSourceValidation
	}
	return v, nil
}
func Resolve(in Inputs) (Snapshot, error) {
	if in.Namespace == "" || in.SourceID == "" || !in.EnvironmentKnown || !in.ConfigurationKnown {
		return Snapshot{}, ErrDependency
	}
	var defaults struct {
		SourceSHA string                     `json:"source_sha256"`
		Values    map[string]json.RawMessage `json:"values"`
	}
	if e := json.Unmarshal(defaultData, &defaults); e != nil {
		return Snapshot{}, e
	}
	out := Snapshot{Version: 1, Inputs: Inputs{in.Namespace, in.SourceID, true, true, map[string]string{}, map[string]json.RawMessage{}}, DefaultSourceSHA: defaults.SourceSHA, Fields: map[string]Field{}}
	for _, key := range Keys() {
		raw, origin := defaults.Values[key], "source_default"
		if value, present := in.Configuration[key]; present {
			raw = append(json.RawMessage(nil), value...)
			out.Inputs.Configuration[key] = append(json.RawMessage(nil), value...)
			origin = "configuration"
		}
		if value, present := in.Environment[key]; present {
			raw = []byte(value)
			out.Inputs.Environment[key] = value
			origin = "environment"
		}
		values, e := ResolveSettingsList(raw, defaults.Values[key])
		if e != nil {
			return Snapshot{}, e
		}
		out.Fields[key] = Field{origin, append(json.RawMessage(nil), raw...), values}
	}
	evidence, _ := json.Marshal(struct {
		Inputs           Inputs
		DefaultSourceSHA string
	}{out.Inputs, out.DefaultSourceSHA})
	sum := sha256.Sum256(evidence)
	out.InputSHA = hex.EncodeToString(sum[:])
	return out, nil
}

// ResolveSettingsJSONList applies the fixed Settings list conversion with the
// caller's actual field factory default, including explicit empty lists.
func ResolveSettingsJSONList(raw, defaultRaw json.RawMessage) (json.RawMessage, error) {
	value, e := decode(raw)
	if e != nil {
		return nil, ErrSourceValidation
	}
	// The actual Settings.generic_type_validator preserves an already-list
	// value. Its generic converter supplies the field factory for other
	// non-string values, but leaves explicit None for schema rejection.
	fallback := func() any { v, _ := decode(defaultRaw); return v }
	if s, ok := value.(string); ok {
		parsed, pe := decode([]byte(strings.TrimSpace(s)))
		if pe != nil {
			value = fallback()
		} else if _, list := parsed.([]any); list && jinjaexec.PythonRepr(jinjaexec.ToValue(parsed)) == s {
			// The source only assigns the converted value when needs_update is
			// true. For a literal "[]" it leaves a string and Pydantic rejects it.
			return nil, ErrSourceValidation
		} else {
			value = parsed
		}
	} else if value != nil {
		if _, list := value.([]any); !list {
			value = fallback()
		}
	}
	list, ok := value.([]any)
	if !ok {
		return nil, ErrSourceValidation
	}
	return json.Marshal(list)
}

// ResolveSettingsList is the existing string-only filesystem classifier view.
// Source settings readers use ResolveSettingsJSONList to retain arbitrary JSON
// list members; no classifier behavior or membership normalization changes.
func ResolveSettingsList(raw, defaultRaw json.RawMessage) ([]string, error) {
	normalized, err := ResolveSettingsJSONList(raw, defaultRaw)
	if err != nil {
		return nil, err
	}
	value, err := decode(normalized)
	if err != nil {
		return nil, ErrSourceValidation
	}
	list := value.([]any)
	values := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, ErrUnsupportedMember
		}
		values = append(values, s)
	}
	return values, nil
}

func (s Snapshot) Values(key string) []string {
	f, ok := s.Fields[key]
	if !ok {
		return nil
	}
	return append([]string{}, f.Values...)
}
func (s Snapshot) Contains(key, lowercaseExtension string) bool {
	f, ok := s.Fields[key]
	if !ok {
		return false
	}
	for _, v := range f.Values {
		if v == lowercaseExtension {
			return true
		}
	}
	return false
}
func (s Snapshot) Valid() bool {
	actual, e := Resolve(s.Inputs)
	if e != nil {
		return false
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(s)
	return bytes.Equal(a, b)
}
