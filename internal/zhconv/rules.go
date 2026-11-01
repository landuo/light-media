// Algorithm adapted from zhconv-rs 0.4.2 rule.rs/variant.rs/pagerules.rs.
// MIT OR Apache-2.0; see licenses/.
package zhconv

import (
	"errors"
	"strings"
)

type variant string

const hans variant = "zh-hans"

var variantNames = map[variant]string{
	"zh": "原文", "zh-hant": "繁体", "zh-hans": "简体",
	"zh-tw": "臺灣", "zh-hk": "香港", "zh-mo": "澳門",
	"zh-my": "大马", "zh-sg": "新加坡", "zh-cn": "大陆",
}

var errRule = errors.New("zhconv: invalid conversion rule")

func parseVariant(s string) (variant, error) {
	// strum's ascii_case_insensitive is not Unicode case folding.
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	v := variant(b)
	if _, ok := variantNames[v]; !ok {
		return "", errRule
	}
	return v, nil
}

// Rust str::trim uses Unicode White_Space, not Python's broader str.isspace.
func trimRust(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r >= 0x09 && r <= 0x0d || r == 0x20 || r == 0x85 || r == 0xa0 ||
			r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 ||
			r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
	})
}

type pair struct{ from, to string }
type conv struct {
	asis *string
	bid  map[variant]string
	unid map[variant][]pair
}

func asisConv(s string) *conv { return &conv{asis: &s} }

func parseConv(text string) (*conv, error) {
	s := trimRust(text)
	c := &conv{bid: make(map[variant]string), unid: make(map[variant][]pair)}
	parseSingle := func(part string) error {
		if trimRust(part) == "" {
			return nil
		}
		left, right, hasLeft := "", part, false
		if i := strings.Index(part, "=>"); i >= 0 {
			if i == 0 {
				return &SourcePanicError{}
			}
			left, right, hasLeft = part[:i], part[i+2:], true
		}
		i := strings.IndexByte(right, ':')
		if i < 0 {
			return errRule
		}
		v, err := parseVariant(trimRust(right[:i]))
		if err != nil {
			return err
		}
		to := right[i+1:]
		if hasLeft {
			c.unid[v] = append(c.unid[v], pair{left, to})
		} else {
			c.bid[v] = to
		}
		return nil
	}
	i, amp := 0, -1
	for j := 0; j < len(s); j++ {
		switch b := s[j]; b {
		case '&':
			amp = j
		case ';':
			if !(amp >= 0 && j-amp > 1) {
				if err := parseSingle(s[i:j]); err != nil {
					return nil, err
				}
				i = j + 1
			}
		default:
			if amp >= 0 && !(b == '#' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z') {
				amp = -1
			}
		}
	}
	if i != len(s) {
		if err := parseSingle(s[i:]); err != nil {
			if _, fatal := err.(*SourcePanicError); fatal {
				return nil, err
			}
			if len(c.bid) == 0 && len(c.unid) == 0 {
				return asisConv(s), nil
			}
			return nil, err
		}
	}
	return c, nil
}

func (c *conv) textHans() string {
	if c == nil {
		return ""
	}
	if c.asis != nil {
		return *c.asis
	}
	for _, v := range []variant{hans, "zh-cn", "zh-sg", "zh-my"} {
		if s, ok := c.bid[v]; ok {
			return s
		}
	}
	return ""
}

func (c *conv) pairsHans() []pair {
	if c == nil {
		return nil
	}
	if c.asis != nil {
		return []pair{{*c.asis, *c.asis}}
	}
	// Original bidirectional get_conv_pairs emits NO pairs for ZhHans.
	return c.unid[hans]
}

func (c *conv) describe() string {
	if c == nil {
		return ""
	}
	if c.asis != nil {
		return *c.asis
	}
	var out strings.Builder
	// Upstream HashMap iteration is unspecified. Do not sort and pretend
	// byte-identical ordering; this branch's comparison contract is pending.
	for v, text := range c.bid {
		out.WriteString(variantNames[v])
		out.WriteString("：")
		out.WriteString(text)
		out.WriteString("；")
	}
	for v, pairs := range c.unid {
		for _, p := range pairs {
			out.WriteString(p.from)
			out.WriteString("⇒")
			out.WriteString(string(v))
			out.WriteString(": ")
			out.WriteString(p.to)
		}
	}
	return out.String()
}

type action uint8

const (
	noAction action = iota
	addAction
	removeAction
)

type outputMode uint8

const (
	normalOutput outputMode = iota
	noOutput
	variantOutput
	descriptionOutput
)

type rule struct {
	action  action
	output  outputMode
	conv    *conv
	variant variant
	title   bool
}

func parseRule(s string) (rule, error) {
	flags, body := "", s
	if i := strings.IndexByte(s, '|'); i >= 0 {
		flags, body = s[:i], s[i+1:]
	}
	r := rule{output: normalOutput}
	for _, f := range flags {
		switch f {
		case '+':
			r.action = addAction
		case '-':
			r.action, r.output = removeAction, noOutput
		case 'R':
			return rule{output: normalOutput, conv: asisConv(body)}, nil
		case 'N':
			v, err := parseVariant(body)
			if err != nil {
				return rule{}, err
			}
			r.output, r.variant = variantOutput, v
		case 'D':
			r.output = descriptionOutput
		case 'H':
			r.action, r.output = addAction, noOutput
		case 'S': // Original is a no-op even following H/T/-.
		case 'A':
			r.action = addAction
		case 'T':
			r.title, r.output = true, noOutput
		default:
			return rule{}, errRule
		}
	}
	if r.output != variantOutput {
		c, err := parseConv(body)
		if err != nil {
			return rule{}, err
		}
		r.conv = c
	}
	if r.title && (r.conv == nil || len(r.conv.bid) == 0) {
		return rule{}, errRule
	}
	return r, nil
}

func parseRuleInfallible(s string) (rule, error) {
	r, err := parseRule(s)
	if err == nil {
		return r, nil
	}
	if _, fatal := err.(*SourcePanicError); fatal {
		return rule{}, err
	}
	return rule{output: normalOutput, conv: asisConv(s), title: true}, nil
}

func (r rule) render() string {
	switch r.output {
	case noOutput:
		return ""
	case variantOutput:
		return variantNames[r.variant]
	case descriptionOutput:
		return r.conv.describe()
	default:
		return r.conv.textHans()
	}
}
