// Package directorysource implements the original DirectoryHelper's pure
// selection/default semantics. Filesystem observations and host authorization
// remain explicit caller responsibilities.
package directorysource

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

type Object map[string]any
type Rule struct{ Values Object }
type SourceError struct{ Kind, Message string }

func (e *SourceError) Error() string { return e.Kind + ": " + e.Message }

var ErrDependency = errors.New("original directory configuration or observation is unknown")

//go:embed defaults.json
var defaults []byte

func Decode(raw json.RawMessage) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var x any
	if e := d.Decode(&x); e != nil {
		return nil, e
	}
	var tail any
	if e := d.Decode(&tail); e != io.EOF {
		return nil, errors.New("multiple JSON values")
	}
	return x, nil
}
func validation() error {
	return &SourceError{Kind: "ValidationError", Message: "invalid original TransferDirectoryConf field"}
}

var intString = regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*(?:\.0+)?$`)

func integer(v any) (json.Number, error) {
	switch x := v.(type) {
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case json.Number:
		s := string(x)
		if !strings.ContainsAny(s, ".eE") {
			n, ok := new(big.Int).SetString(s, 10)
			if ok {
				return json.Number(n.String()), nil
			}
		}
		f, e := strconv.ParseFloat(s, 64)
		if e != nil || math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) || f >= math.Ldexp(1, 63) || f < -math.Ldexp(1, 63) {
			return "", validation()
		}
		n, _ := big.NewFloat(f).Int(nil)
		return json.Number(n.String()), nil
	case string:
		s := strings.TrimSpace(x)
		if !intString.MatchString(s) {
			return "", validation()
		}
		s = strings.ReplaceAll(strings.Split(s, ".")[0], "_", "")
		n, ok := new(big.Int).SetString(s, 10)
		if ok {
			return json.Number(n.String()), nil
		}
	}
	return "", validation()
}
func boolean(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case json.Number:
		f, e := strconv.ParseFloat(string(x), 64)
		if e == nil && (f == 0 || f == 1) {
			return f == 1, nil
		}
	case string:
		switch strings.ToLower(x) {
		case "1", "on", "t", "true", "y", "yes":
			return true, nil
		case "0", "off", "f", "false", "n", "no":
			return false, nil
		}
	}
	return false, validation()
}
func Normalize(input Object) (Rule, error) {
	base, e := Decode(defaults)
	if e != nil {
		return Rule{}, e
	}
	out := Object(base.(map[string]any))
	for key, def := range out {
		v, present := input[key]
		if !present {
			continue
		}
		if v == nil {
			out[key] = nil
			continue
		}
		if key == "priority" {
			n, e := integer(v)
			if e != nil {
				return Rule{}, e
			}
			out[key] = n
			continue
		}
		if _, ok := def.(bool); ok {
			b, e := boolean(v)
			if e != nil {
				return Rule{}, e
			}
			out[key] = b
			continue
		}
		s, ok := v.(string)
		if !ok {
			return Rule{}, validation()
		}
		out[key] = s
	}
	return Rule{out}, nil
}
func ParseRules(input any) ([]Rule, error) {
	if input == nil {
		return []Rule{}, nil
	}
	raw, ok := input.([]any)
	if !ok {
		return nil, validation()
	}
	out := make([]Rule, 0, len(raw))
	for _, v := range raw {
		o, ok := v.(map[string]any)
		if !ok {
			return nil, validation()
		}
		r, e := Normalize(o)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func text(v any) string { s, _ := v.(string); return s }
func truth(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case string:
		return x != ""
	case bool:
		return x
	case json.Number:
		return x != "0"
	}
	return true
}
func sourceType(name string) error {
	return &SourceError{Kind: "TypeError", Message: fmt.Sprintf("invalid original pathlib or ordering value: %s", name)}
}
