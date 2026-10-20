package noticetemplate

import (
	"strings"

	"light-media/internal/jinja/exec"
)

// Values accepted by ast.literal_eval but rejected by json.dumps stay distinct
// from syntax/ValueError: auto parsing catches those errors differently.
type literalBytes struct{ value string }
type literalSet struct{ values []any }

func (p *literalParser) bytesStart() bool {
	i := p.pos
	for i < len(p.s) && p.s[i] != '\'' && p.s[i] != '"' {
		i++
	}
	return strings.ContainsAny(p.s[p.pos:i], "bB")
}
func (p *literalParser) braces() (any, error) {
	p.pos++
	out := exec.NewDict()
	if p.take("}") {
		return out, nil
	}
	key, err := p.value()
	if err != nil {
		return nil, err
	}
	if !p.take(":") {
		values := []any{key}
		if !literalHashable(key) {
			return nil, SourceError{"TypeError"}
		}
		for {
			if p.take("}") {
				return literalSet{values}, nil
			}
			if !p.take(",") {
				return nil, literalError()
			}
			if p.take("}") {
				return literalSet{values}, nil
			}
			v, e := p.value()
			if e != nil {
				return nil, e
			}
			if !literalHashable(v) {
				return nil, SourceError{"TypeError"}
			}
			values = append(values, v)
		}
	}
	for {
		value, e := p.value()
		if e != nil {
			return nil, e
		}
		if !literalHashable(key) {
			return nil, SourceError{"TypeError"}
		}
		found := false
		for _, pair := range out.Pairs {
			if literalKeyEqual(pair.Key.Interface(), key) {
				pair.Value = exec.AsValue(value)
				found = true
				break
			}
		}
		if !found {
			out.Pairs = append(out.Pairs, &exec.Pair{Key: exec.AsValue(key), Value: exec.AsValue(value)})
		}
		if p.take("}") {
			return out, nil
		}
		if !p.take(",") {
			return nil, literalError()
		}
		if p.take("}") {
			return out, nil
		}
		key, e = p.value()
		if e != nil {
			return nil, e
		}
		if !p.take(":") {
			return nil, literalError()
		}
	}
}
