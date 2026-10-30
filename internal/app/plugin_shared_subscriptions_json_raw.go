package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

// S05 JSON columns are Python JSON values, whose strings may contain isolated
// surrogate code points. They cannot be represented faithfully by Go strings.
// Keep decoded code points until producing escaped JSON, never RuneError.
// Object keys use the same code-point identity and Python's last-value-wins.
type sharedSubscriptionJSONValue struct {
	kind    byte
	text    string
	points  []rune
	items   []*sharedSubscriptionJSONValue
	members []sharedSubscriptionJSONMember
	index   map[string]int
}
type sharedSubscriptionJSONMember struct {
	key   []rune
	value *sharedSubscriptionJSONValue
}
type sharedSubscriptionJSONParser struct {
	raw []byte
	pos int
}

func parseSharedSubscriptionJSON(raw json.RawMessage) (*sharedSubscriptionJSONValue, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid source JSON")
	}
	p := sharedSubscriptionJSONParser{raw: raw}
	return p.value(), nil
}
func (p *sharedSubscriptionJSONParser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}
func (p *sharedSubscriptionJSONParser) value() *sharedSubscriptionJSONValue {
	p.space()
	kind := p.raw[p.pos]
	v := &sharedSubscriptionJSONValue{kind: kind}
	switch kind {
	case '"':
		v.points = p.stringValue()
	case '[':
		p.pos++
		p.space()
		for p.raw[p.pos] != ']' {
			v.items = append(v.items, p.value())
			p.space()
			if p.raw[p.pos] == ',' {
				p.pos++
				p.space()
			}
		}
		p.pos++
	case '{':
		v.index = map[string]int{}
		p.pos++
		p.space()
		for p.raw[p.pos] != '}' {
			key := p.stringValue()
			p.space()
			p.pos++
			child := p.value()
			identity := string(appendSharedSubscriptionJSONString(nil, key))
			if index, ok := v.index[identity]; ok {
				v.members[index].value = child
			} else {
				v.index[identity] = len(v.members)
				v.members = append(v.members, sharedSubscriptionJSONMember{key, child})
			}
			p.space()
			if p.raw[p.pos] == ',' {
				p.pos++
				p.space()
			}
		}
		p.pos++
	default:
		begin := p.pos
		for p.pos < len(p.raw) {
			c := p.raw[p.pos]
			if c == ',' || c == ']' || c == '}' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			p.pos++
		}
		v.text = string(p.raw[begin:p.pos])
		if kind != 't' && kind != 'f' && kind != 'n' {
			v.kind = '0'
		}
	}
	return v
}
func (p *sharedSubscriptionJSONParser) hex() rune {
	n, _ := strconv.ParseUint(string(p.raw[p.pos:p.pos+4]), 16, 16)
	p.pos += 4
	return rune(n)
}
func (p *sharedSubscriptionJSONParser) stringValue() []rune {
	p.pos++
	out := []rune{}
	for p.raw[p.pos] != '"' {
		if p.raw[p.pos] != '\\' {
			r, n := utf8.DecodeRune(p.raw[p.pos:])
			out = append(out, r)
			p.pos += n
			continue
		}
		p.pos++
		code := p.raw[p.pos]
		p.pos++
		switch code {
		case '"', '\\', '/':
			out = append(out, rune(code))
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r := p.hex()
			if r >= 0xd800 && r <= 0xdbff && p.pos+6 <= len(p.raw) && p.raw[p.pos] == '\\' && p.raw[p.pos+1] == 'u' {
				next, _ := strconv.ParseUint(string(p.raw[p.pos+2:p.pos+6]), 16, 16)
				if next >= 0xdc00 && next <= 0xdfff {
					r = 0x10000 + (r-0xd800)*0x400 + rune(next) - 0xdc00
					p.pos += 6
				}
			}
			out = append(out, r)
		}
	}
	p.pos++
	return out
}
func appendSharedSubscriptionJSONString(out []byte, points []rune) []byte {
	const digits = "0123456789abcdef"
	escaped := func(r rune) {
		out = append(out, '\\', 'u', digits[(r>>12)&15], digits[(r>>8)&15], digits[(r>>4)&15], digits[r&15])
	}
	out = append(out, '"')
	for _, r := range points {
		switch {
		case r == '"' || r == '\\':
			out = append(out, '\\', byte(r))
		case r >= 0x20 && r < 0x7f:
			out = append(out, byte(r))
		case r <= 0xffff:
			escaped(r)
		default:
			r -= 0x10000
			escaped(0xd800 + (r >> 10))
			escaped(0xdc00 + (r & 1023))
		}
	}
	return append(out, '"')
}
func (v *sharedSubscriptionJSONValue) appendJSON(out []byte) []byte {
	switch v.kind {
	case '"':
		return appendSharedSubscriptionJSONString(out, v.points)
	case '[':
		out = append(out, '[')
		for i, item := range v.items {
			if i > 0 {
				out = append(out, ',')
			}
			out = item.appendJSON(out)
		}
		return append(out, ']')
	case '{':
		out = append(out, '{')
		for i, member := range v.members {
			if i > 0 {
				out = append(out, ',')
			}
			out = appendSharedSubscriptionJSONString(out, member.key)
			out = append(out, ':')
			out = member.value.appendJSON(out)
		}
		return append(out, '}')
	default:
		return append(out, v.text...)
	}
}
func (v *sharedSubscriptionJSONValue) hasUnpaired() bool {
	for _, r := range v.points {
		if r >= 0xd800 && r <= 0xdfff {
			return true
		}
	}
	for _, item := range v.items {
		if item.hasUnpaired() {
			return true
		}
	}
	for _, member := range v.members {
		for _, r := range member.key {
			if r >= 0xd800 && r <= 0xdfff {
				return true
			}
		}
		if member.value.hasUnpaired() {
			return true
		}
	}
	return false
}
func (v *sharedSubscriptionJSONValue) equal(other *sharedSubscriptionJSONValue) bool {
	if v.kind != other.kind || v.text != other.text || len(v.points) != len(other.points) || len(v.items) != len(other.items) || len(v.members) != len(other.members) {
		return false
	}
	for i, r := range v.points {
		if r != other.points[i] {
			return false
		}
	}
	for i, item := range v.items {
		if !item.equal(other.items[i]) {
			return false
		}
	}
	for _, member := range v.members {
		index, ok := other.index[string(appendSharedSubscriptionJSONString(nil, member.key))]
		if !ok || !member.value.equal(other.members[index].value) {
			return false
		}
	}
	return true
}
