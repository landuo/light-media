package sourcezhconv

import "strings"

type conversion struct {
	asis *string
	bid  map[string]string
	unid map[string][][2]string
}
type rule struct {
	action  byte
	output  byte // normal, hidden, variant name, description
	variant string
	conv    conversion
}

var variants = []string{"zh", "zh-hant", "zh-hans", "zh-tw", "zh-hk", "zh-mo", "zh-my", "zh-sg", "zh-cn"}
var names = map[string]string{"zh": "原文", "zh-hant": "繁体", "zh-hans": "简体", "zh-tw": "臺灣", "zh-hk": "香港", "zh-mo": "澳門", "zh-my": "大马", "zh-sg": "新加坡", "zh-cn": "大陆"}

func variant(s string) (string, bool) { s = strings.ToLower(s); _, ok := names[s]; return s, ok }
func parseRule(s string) (rule, bool) {
	flags, body := "", s
	if a, b, ok := strings.Cut(s, "|"); ok {
		flags, body = a, b
	}
	r := rule{output: 'S'}
	title := false
	for _, flag := range flags {
		switch flag {
		case '+', 'A':
			r.action = '+'
		case '-':
			r.action = '-'
			r.output = 0
		case 'R':
			return rule{output: 'S', conv: conversion{asis: &body}}, true
		case 'N':
			v, ok := variant(body)
			if !ok {
				return rule{}, false
			}
			r.output = 'N'
			r.variant = v
		case 'D':
			r.output = 'D'
		case 'H':
			r.action = '+'
			r.output = 0
		case 'S':
		case 'T':
			title = true
			r.output = 0
		default:
			return rule{}, false
		}
	}
	if r.output != 'N' {
		var ok bool
		r.conv, ok = parseConversion(body)
		if !ok {
			return rule{}, false
		}
	}
	if title && len(r.conv.bid) == 0 {
		return rule{}, false
	}
	return r, true
}
func parseConversion(text string) (conversion, bool) {
	s := strings.TrimSpace(text)
	c := conversion{bid: map[string]string{}, unid: map[string][][2]string{}}
	parse := func(part string) bool {
		if strings.TrimSpace(part) == "" {
			return true
		}
		from, right, uni := strings.Cut(part, "=>")
		if !uni {
			right = part
		} else if from == "" {
			panic(invalidRule{})
		}
		v, to, ok := strings.Cut(right, ":")
		if !ok {
			return false
		}
		v, ok = variant(strings.TrimSpace(v))
		if !ok {
			return false
		}
		if uni {
			c.unid[v] = append(c.unid[v], [2]string{from, to})
		} else {
			c.bid[v] = to
		}
		return true
	}
	start, amp := 0, -1
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case '&':
			amp = i
		case ';':
			if !(amp >= 0 && i-amp > 1) {
				if !parse(s[start:i]) {
					return conversion{}, false
				}
				start = i + 1
			}
		default:
			if amp >= 0 && !(b == '#' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9') {
				amp = -1
			}
		}
	}
	if start != len(s) && !parse(s[start:]) {
		if len(c.bid) == 0 && len(c.unid) == 0 {
			return conversion{asis: &s}, true
		}
		return conversion{}, false
	}
	return c, true
}
func (r rule) pairs() [][2]string {
	if r.conv.asis != nil {
		return [][2]string{{*r.conv.asis, *r.conv.asis}}
	}
	// Upstream's bidirectional globals only apply to regional targets. For
	// zh-hans only explicit unidirectional rules apply, without fallback.
	return r.conv.unid["zh-hans"]
}
func (r rule) render() string {
	if r.output == 0 {
		return ""
	}
	if r.output == 'N' {
		return names[r.variant]
	}
	if r.conv.asis != nil {
		return *r.conv.asis
	}
	if r.output == 'D' {
		// Upstream's Rust HashMap has no stable iteration order. A fixed order
		// retains every description clause without randomized title text.
		var out strings.Builder
		for _, v := range variants {
			if t, ok := r.conv.bid[v]; ok {
				out.WriteString(names[v] + "：" + t + "；")
			}
		}
		for _, v := range variants {
			for _, p := range r.conv.unid[v] {
				out.WriteString(p[0] + "⇒" + v + ": " + p[1])
			}
		}
		return out.String()
	}
	for _, v := range []string{"zh-hans", "zh-cn", "zh-sg", "zh-my"} {
		if t, ok := r.conv.bid[v]; ok {
			return t
		}
	}
	return ""
}
