package pyregex

import "strings"

type tokenizer struct {
	runes   []rune
	pattern string
	index   int
	next    string
}

func newTokenizer(pattern string) *tokenizer {
	t := &tokenizer{pattern: pattern, runes: sourceRunes(pattern)}
	t.advance()
	return t
}
func (t *tokenizer) advance() {
	i := t.index
	if i >= len(t.runes) {
		t.next = ""
		return
	}
	r := t.runes[i : i+1]
	if r[0] == '\\' {
		i++
		if i >= len(t.runes) {
			raise("error", "bad escape (end of pattern)", t.pattern, len(t.runes)-1)
		}
		r = t.runes[i-1 : i+1]
	}
	t.index = i + 1
	t.next = sourceText(r)
}
func (t *tokenizer) get() string { value := t.next; t.advance(); return value }
func (t *tokenizer) match(s string) bool {
	if t.next == s {
		t.advance()
		return true
	}
	return false
}
func (t *tokenizer) tell() int  { return t.index - len(sourceRunes(t.next)) }
func (t *tokenizer) seek(i int) { t.index = i; t.advance() }
func (t *tokenizer) while(n int, chars string) string {
	var b strings.Builder
	for range n {
		if t.next == "" || len(sourceRunes(t.next)) != 1 || !strings.Contains(chars, t.next) {
			break
		}
		b.WriteString(t.get())
	}
	return b.String()
}
func (t *tokenizer) until(end, name string) string {
	var b strings.Builder
	for {
		c := t.next
		t.advance()
		if c == "" {
			if b.Len() == 0 {
				t.fail("missing "+name, 0)
			}
			t.fail("missing "+end+", unterminated name", len(sourceRunes(b.String())))
		}
		if c == end {
			if b.Len() == 0 {
				t.fail("missing "+name, 1)
			}
			return b.String()
		}
		b.WriteString(c)
	}
}
func (t *tokenizer) fail(message string, offset int) {
	raise("error", message, t.pattern, t.tell()-offset)
}
func (t *tokenizer) checkName(name string, offset int) {
	if !identifier(name) {
		t.fail("bad character in group name "+repr(name), len(sourceRunes(name))+offset)
	}
}
