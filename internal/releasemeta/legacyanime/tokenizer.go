package legacyanime

import "strings"

func (s *state) addToken(category tokenCategory, content string, enclosed bool) {
	s.tokens = append(s.tokens, &token{category, content, enclosed})
}
func (s *state) tokenize(filename string) bool {
	text := []rune(filename)
	open := false
	matching := rune(0)
	brackets := map[rune]rune{'(': ')', '[': ']', '{': '}', '「': '」', '『': '』', '【': '】', '（': '）'}
	for len(text) > 0 {
		at := -1
		if !open {
			for i, r := range text {
				if close, ok := brackets[r]; ok {
					at = i
					matching = close
					break
				}
			}
		} else {
			for i, r := range text {
				if r == matching {
					at = i
					break
				}
			}
		}
		if at != 0 {
			last := at
			if last == -1 {
				last = len(text)
			}
			s.tokenizePreidentified(string(text[:last]), open)
		}
		if at != -1 {
			s.addToken(tokenBracket, string(text[at]), true)
			open = !open
			text = text[at+1:]
		} else {
			text = nil
		}
	}
	return len(s.tokens) > 0
}
func (s *state) tokenizePreidentified(text string, enclosed bool) {
	intervals := s.peek(text)
	last := 0
	for _, span := range intervals {
		if last != span.first {
			s.tokenizeDelimiters(runeSlice(text, last, span.first), enclosed)
		}
		s.addToken(tokenIdentifier, runeSlice(text, span.first, span.last), enclosed)
		last = span.last
	}
	if last != runeLen(text) {
		s.tokenizeDelimiters(runeSlice(text, last, runeLen(text)), enclosed)
	}
}
func pySpace(r rune) bool {
	return r >= 9 && r <= 13 || r >= 0x1c && r <= 0x20 || r == 0x85 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
}
func delimiterMatcher(delimiters string) func(rune) bool {
	if delimiters == "" {
		sourcePanic("error")
	}
	var checks []func(rune) bool
	for _, r := range delimiters {
		literal := r
		switch r {
		case 'a':
			literal = '\a'
		case 'b':
			literal = '\b'
		case 'f':
			literal = '\f'
		case 'n':
			literal = '\n'
		case 'r':
			literal = '\r'
		case 't':
			literal = '\t'
		case 'v':
			literal = '\v'
		case 's':
			checks = append(checks, pySpace)
			continue
		case 'S':
			checks = append(checks, func(v rune) bool { return !pySpace(v) })
			continue
		case 'w':
			checks = append(checks, func(v rune) bool { return v == '_' || inRange(v, pyAlnumRanges) })
			continue
		case 'W':
			checks = append(checks, func(v rune) bool { return !(v == '_' || inRange(v, pyAlnumRanges)) })
			continue
		case 'd':
			checks = append(checks, func(v rune) bool { _, ok := pyDecimal[v]; return ok })
			continue
		case 'D':
			checks = append(checks, func(v rune) bool { _, ok := pyDecimal[v]; return !ok })
			continue
		default:
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '8' || r == '9' {
				sourcePanic("error")
			}
			if r >= '0' && r <= '7' {
				literal = r - '0'
			}
		}
		checks = append(checks, func(v rune) bool { return v == literal })
	}
	return func(r rune) bool {
		for _, check := range checks {
			if check(r) {
				return true
			}
		}
		return false
	}
}
func (s *state) tokenizeDelimiters(text string, enclosed bool) {
	match := delimiterMatcher(s.delimiters())
	runes := []rune(text)
	first := 0
	add := func(value string) {
		if value != "" {
			category := tokenUnknown
			if strings.Contains(s.delimiters(), value) {
				category = tokenDelimiter
			}
			s.addToken(category, value, enclosed)
		}
	}
	for i, r := range runes {
		if match(r) {
			add(string(runes[first:i]))
			add(string(r))
			first = i + 1
		}
	}
	add(string(runes[first:]))
	s.validateDelimiterTokens()
}
func (s *state) validateDelimiterTokens() {
	prev := func(t *token) *token { return s.previous(t, flagValid) }
	next := func(t *token) *token { return s.next(t, flagValid) }
	isDelimiter := func(t *token) bool { return t != nil && t.category == tokenDelimiter }
	isUnknown := func(t *token) bool { return t != nil && t.category == tokenUnknown }
	single := func(t *token) bool { return isUnknown(t) && runeLen(t.content) == 1 && t.content != "-" }
	appendTo := func(t, to *token) {
		if to == nil {
			sourcePanic("AttributeError")
		}
		to.content += t.content
		t.category = tokenInvalid
	}
	for _, t := range s.list(0, nil, nil) {
		if t.category != tokenDelimiter {
			continue
		}
		delimiter := t.content
		previous, following := prev(t), next(t)
		if delimiter != " " && delimiter != "_" {
			if single(previous) {
				appendTo(t, previous)
				for isUnknown(following) {
					appendTo(following, previous)
					following = next(following)
					if isDelimiter(following) && following.content == delimiter {
						appendTo(following, previous)
						following = next(following)
					}
				}
				continue
			}
			if single(following) {
				appendTo(t, previous)
				appendTo(following, previous)
				continue
			}
		}
		if isUnknown(previous) && isDelimiter(following) {
			nextDelimiter := following.content
			if delimiter != nextDelimiter && delimiter != "," && (nextDelimiter == " " || nextDelimiter == "_") {
				appendTo(t, previous)
			}
		} else if isDelimiter(previous) && isDelimiter(following) {
			if previous.content == following.content && previous.content != delimiter {
				t.category = tokenUnknown
			}
		}
		if delimiter == "&" || delimiter == "+" {
			if isUnknown(previous) && isUnknown(following) && pyDigit(previous.content) && pyDigit(following.content) {
				appendTo(t, previous)
				appendTo(following, previous)
			}
		}
	}
	var filtered []*token
	for _, t := range s.tokens {
		if t.category != tokenInvalid {
			filtered = append(filtered, t)
		}
	}
	s.tokens = filtered
}
