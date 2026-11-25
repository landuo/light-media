package pyregex

import (
	"fmt"
	"light-media/builtinplugins/plugins/mpserverstatus/encodingdetector"
	"strings"
)

const asciiLetters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const digits = "0123456789"
const octalDigits = "01234567"
const hexDigits = "0123456789abcdefABCDEF"
const whitespace = " \t\n\r\v\f"

var simpleEscapes = map[string]rune{`\a`: '\a', `\b`: '\b', `\f`: '\f', `\n`: '\n', `\r`: '\r', `\t`: '\t', `\v`: '\v', `\\`: '\\'}
var categories = map[string]string{`\d`: "CATEGORY_DIGIT", `\D`: "CATEGORY_NOT_DIGIT", `\s`: "CATEGORY_SPACE", `\S`: "CATEGORY_NOT_SPACE", `\w`: "CATEGORY_WORD", `\W`: "CATEGORY_NOT_WORD"}
var anchors = map[string]string{`\A`: "AT_BEGINNING_STRING", `\b`: "AT_BOUNDARY", `\B`: "AT_NON_BOUNDARY", `\Z`: "AT_END_STRING"}

func singleIn(s, charset string) bool {
	return len(sourceRunes(s)) == 1 && strings.Contains(charset, s)
}
func literal(r rune) Node    { return Node{Op: Literal, Rune: r} }
func category(s string) Node { return Node{Op: In, Set: []ClassItem{{Op: Category, Category: s}}} }
func classItem(n Node) ClassItem {
	if n.Op == In {
		return n.Set[0]
	}
	return ClassItem{Op: n.Op, Rune: n.Rune}
}
func escape(t *tokenizer, escaped string, state *State, inClass bool) Node {
	if !inClass {
		if value, ok := anchors[escaped]; ok {
			return Node{Op: At, Anchor: value}
		}
		if value, ok := categories[escaped]; ok {
			return category(value)
		}
	}
	if value, ok := simpleEscapes[escaped]; ok {
		return literal(value)
	}
	if inClass {
		if value, ok := categories[escaped]; ok {
			return category(value)
		}
	}
	chars := sourceRunes(escaped)
	symbol := chars[1]
	if symbol == 'x' || symbol == 'u' || symbol == 'U' {
		n := 2
		if symbol == 'u' {
			n = 4
		}
		if symbol == 'U' {
			n = 8
		}
		escaped += t.while(n, hexDigits)
		if len(sourceRunes(escaped)) != n+2 {
			t.fail("incomplete escape "+escaped, len(sourceRunes(escaped)))
		}
		value := parseUint(escaped[2:], 16)
		if value > 0x7fffffff {
			raise("OverflowError", "Python int too large to convert to C int", state.Pattern, -1)
		}
		if value > 0x10ffff {
			t.fail("bad escape "+escaped, len(sourceRunes(escaped)))
		}
		return literal(rune(value))
	}
	if symbol == 'N' {
		if !t.match("{") {
			t.fail("missing {", 0)
		}
		name := t.until("}", "character name")
		text, err := encodingdetector.Decode([]byte(`\N{`+name+`}`), "unicode_escape", false)
		r := sourceRunes(text)
		if err != nil || len(r) != 1 {
			t.fail("undefined character name "+repr(name), len(sourceRunes(name))+4)
		}
		return literal(r[0])
	}
	if inClass && singleIn(string(symbol), octalDigits) || !inClass && symbol == '0' {
		escaped += t.while(2, octalDigits)
		value := parseUint(escaped[1:], 8)
		if value > 0o377 {
			t.fail("octal escape value "+escaped+" outside of range 0-0o377", len(sourceRunes(escaped)))
		}
		return literal(rune(value))
	}
	if !inClass && singleIn(string(symbol), digits) {
		if singleIn(t.next, digits) {
			escaped += t.get()
			c := sourceRunes(escaped)
			if singleIn(string(c[1]), octalDigits) && singleIn(string(c[2]), octalDigits) && singleIn(t.next, octalDigits) {
				escaped += t.get()
				value := parseUint(escaped[1:], 8)
				if value > 0o377 {
					t.fail("octal escape value "+escaped+" outside of range 0-0o377", len(sourceRunes(escaped)))
				}
				return literal(rune(value))
			}
		}
		group := int(parseUint(escaped[1:], 10))
		if group < len(state.GroupWidths) {
			if !state.checkGroup(group) {
				t.fail("cannot refer to an open group", len(sourceRunes(escaped)))
			}
			state.checkLookbehind(group, t)
			return Node{Op: GroupRef, Group: group}
		}
		t.fail(fmt.Sprintf("invalid group reference %d", group), len(sourceRunes(escaped))-1)
	}
	if len(chars) == 2 && !singleIn(string(symbol), asciiLetters) && !singleIn(string(symbol), digits) {
		return literal(symbol)
	}
	t.fail("bad escape "+escaped, len(sourceRunes(escaped)))
	panic("unreachable")
}
