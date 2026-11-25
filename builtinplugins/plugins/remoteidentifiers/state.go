// Package remoteidentifiers ports the installed 2.4.1 class state. Form defaults
// are deliberately separate from runtime and from the persisted configuration.
package remoteidentifiers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"
)

const Cron = "30 4 * * *"

var ErrConfiguration = errors.New("remote_identifiers_source_configuration")

type State struct {
	Enable   any `json:"enable"`
	OnlyOnce any `json:"onlyonce"`
	Flitter  any `json:"flitter"`
	Cron     any `json:"cron"`
	FileURLs any `json:"file_urls"`
}

func Cold() State { return State{false, false, true, Cron, ""} }
func Decode(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrConfiguration
	}
	return v, nil
}
func Truth(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			n, _ := strconv.ParseFloat(string(v), 64)
			return n != 0
		}
		return strings.TrimLeft(strings.TrimPrefix(string(v), "-"), "0") != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

// Init does not clear class fields for None, {} or any other falsey input.
// stop_service precedes this method in the host, even for those inputs.
func (s *State) Init(raw json.RawMessage) error {
	v, err := Decode(raw)
	if err != nil || !Truth(v) {
		return err
	}
	c, ok := v.(map[string]any)
	if !ok {
		return ErrConfiguration
	}
	get := func(k string, def any) any {
		if x := c[k]; x != nil {
			return x
		}
		return def
	}
	s.Enable = get("enable", false)
	s.OnlyOnce = get("onlyonce", false)
	s.Flitter = get("flitter", false)
	s.Cron = c["cron"]
	if !Truth(s.Cron) {
		s.Cron = Cron
	}
	s.FileURLs = c["file_urls"]
	if !Truth(s.FileURLs) {
		s.FileURLs = ""
	}
	return nil
}
func (s State) Task() (string, bool, error) {
	v, ok := s.FileURLs.(string)
	if !ok {
		return "", false, ErrConfiguration
	}
	return v, Truth(s.Flitter), nil
}

// Python str.strip additionally includes the four information separators.
func Strip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}
