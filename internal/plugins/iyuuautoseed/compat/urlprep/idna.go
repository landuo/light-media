package urlprep

import (
	"sort"
	"strings"
)

func idnaError(class, message string) error { return fail(class, message) }
func idnaEncode(s string, strict, uts46, std3 bool) (string, error) {
	rs, e := pythonRunes(s)
	if e != nil {
		return "", idnaError("IDNAError", "Invalid Unicode")
	}
	if len(rs) > 1024 {
		return "", idnaError("IDNAError", "Domain too long")
	}
	if uts46 {
		rs, e = utsRemap(rs, std3)
		if e != nil {
			return "", e
		}
	}
	if len(rs) > 254 {
		return "", idnaError("IDNAError", "Domain too long")
	}
	labels := [][]rune{}
	start := 0
	for i, r := range rs {
		if r == '.' || !strict && (r == 0x3002 || r == 0xFF0E || r == 0xFF61) {
			labels = append(labels, rs[start:i])
			start = i + 1
		}
	}
	labels = append(labels, rs[start:])
	if len(labels) == 1 && len(labels[0]) == 0 {
		return "", idnaError("IDNAError", "Empty domain")
	}
	trailing := len(labels[len(labels)-1]) == 0
	if trailing {
		labels = labels[:len(labels)-1]
	}
	out := make([]string, 0, len(labels)+1)
	for _, label := range labels {
		a, e := alabel(label)
		if e != nil {
			return "", e
		}
		if a == "" {
			return "", idnaError("IDNAError", "Empty label")
		}
		out = append(out, a)
	}
	if trailing {
		out = append(out, "")
	}
	result := strings.Join(out, ".")
	limit := 253
	if trailing {
		limit = 254
	}
	if len(result) > limit {
		return "", idnaError("IDNAError", "Domain too long")
	}
	return result, nil
}
func utsRemap(rs []rune, std3 bool) ([]rune, error) {
	loadTables()
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		i := int(r)
		if r >= 256 {
			i = sort.Search(len(tables.uts), func(i int) bool { return tables.uts[i].Start > r }) - 1
		}
		if i < 0 || i >= len(tables.uts) {
			return nil, idnaError("InvalidCodepoint", "Invalid UTS46 code point")
		}
		row := tables.uts[i]
		if row.Status == 'V' || row.Status == 'D' || row.Status == '3' && !std3 && !row.HasReplacement {
			out = append(out, r)
		} else if row.HasReplacement && (row.Status == 'M' || row.Status == '3' && !std3) {
			out = append(out, row.Replacement...)
		} else if row.Status != 'I' {
			return nil, idnaError("InvalidCodepoint", "Disallowed UTS46 code point")
		}
	}
	return nfc(out), nil
}
func alabel(label []rune) (string, error) {
	if len(label) > 1024 {
		return "", idnaError("IDNAError", "Label too long")
	}
	isASCII := true
	for _, c := range label {
		if c >= 128 {
			isASCII = false
			break
		}
	}
	if isASCII {
		s := pythonString(label)
		if _, e := ulabel(s); e != nil {
			return "", e
		}
		if len(s) > 63 {
			return "", idnaError("IDNAError", "Label too long")
		}
		return s, nil
	}
	if e := checkLabel(label); e != nil {
		return "", e
	}
	s := "xn--" + punyEncode(label)
	if len(s) > 63 {
		return "", idnaError("IDNAError", "Label too long")
	}
	return s, nil
}
func ulabel(s string) ([]rune, error) {
	if len(s) > 1024 {
		return nil, idnaError("IDNAError", "Label too long")
	}
	s = lowerASCII(s)
	if !strings.HasPrefix(s, "xn--") {
		rs, e := pythonRunes(s)
		if e != nil {
			return nil, idnaError("IDNAError", "Invalid label")
		}
		return rs, checkLabel(rs)
	}
	s = s[4:]
	if s == "" {
		return nil, idnaError("IDNAError", "Malformed A-label")
	}
	if strings.HasSuffix(s, "-") {
		return nil, idnaError("IDNAError", "A-label must not end with a hyphen")
	}
	rs, e := punyDecode(s)
	if e != nil {
		return nil, idnaError("IDNAError", "Invalid A-label")
	}
	return rs, checkLabel(rs)
}
func checkLabel(label []rune) error {
	loadTables()
	if len(label) > 1024 {
		return idnaError("IDNAError", "Label too long")
	}
	if len(label) == 0 {
		return idnaError("IDNAError", "Empty Label")
	}
	if len(label) > 254 {
		return idnaError("IDNAError", "Label too long")
	}
	if pythonString(nfc(label)) != pythonString(label) {
		return idnaError("IDNAError", "Label must be in Normalization Form C")
	}
	if len(label) >= 4 && label[2] == '-' && label[3] == '-' {
		return idnaError("IDNAError", "Label has disallowed hyphens")
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return idnaError("IDNAError", "Label must not start or end with hyphen")
	}
	if property(label[0]).Mark {
		return idnaError("IDNAError", "Label begins with an illegal combining character")
	}
	for pos, c := range label {
		if inRanges(c, tables.classes["PVALID"]) {
			continue
		}
		if inRanges(c, tables.classes["CONTEXTJ"]) {
			ok, e := validContextJ(label, pos)
			if e != nil {
				return idnaError("IDNAError", "Unknown codepoint adjacent to joiner")
			}
			if !ok {
				return idnaError("IDNAError", "Joiner not allowed")
			}
		} else if inRanges(c, tables.classes["CONTEXTO"]) {
			if !validContextO(label, pos) {
				return idnaError("InvalidCodepointContext", "Codepoint not allowed in context")
			}
		} else {
			return idnaError("InvalidCodepoint", "Disallowed codepoint")
		}
	}
	return checkBidi(label)
}
func combiningClass(c rune) (uint8, error) {
	p := property(c)
	if p.Combining == 0 && !p.Named {
		return 0, idnaError("ValueError", "Unknown character in unicodedata")
	}
	return p.Combining, nil
}
func joiningType(c rune) string {
	for _, s := range []string{"C", "D", "L", "R", "T"} {
		if inRanges(c, tables.joining[s]) {
			return s
		}
	}
	return ""
}
func validContextJ(label []rune, pos int) (bool, error) {
	c := label[pos]
	if c != 0x200C && c != 0x200D {
		return false, nil
	}
	if pos > 0 {
		cc, e := combiningClass(label[pos-1])
		if e != nil {
			return false, e
		}
		if cc == 9 {
			return true, nil
		}
	}
	if c == 0x200D {
		return false, nil
	}
	ok := false
	for i := pos - 1; i >= 0; i-- {
		j := joiningType(label[i])
		if j == "T" {
			continue
		}
		ok = j == "L" || j == "D"
		break
	}
	if !ok {
		return false, nil
	}
	for i := pos + 1; i < len(label); i++ {
		j := joiningType(label[i])
		if j == "T" {
			continue
		}
		return j == "R" || j == "D", nil
	}
	return false, nil
}
func isScript(c rune, s string) bool { return inRanges(c, tables.scripts[s]) }
func validContextO(label []rune, pos int) bool {
	c := label[pos]
	switch c {
	case 0xB7:
		return pos > 0 && pos < len(label)-1 && label[pos-1] == 'l' && label[pos+1] == 'l'
	case 0x375:
		return pos < len(label)-1 && len(label) > 1 && isScript(label[pos+1], "Greek")
	case 0x5F3, 0x5F4:
		return pos > 0 && isScript(label[pos-1], "Hebrew")
	case 0x30FB:
		for _, r := range label {
			if r != 0x30FB && (isScript(r, "Hiragana") || isScript(r, "Katakana") || isScript(r, "Han")) {
				return true
			}
		}
		return false
	}
	if c >= 0x660 && c <= 0x669 {
		for _, r := range label {
			if r >= 0x6F0 && r <= 0x6F9 {
				return false
			}
		}
		return true
	}
	if c >= 0x6F0 && c <= 0x6F9 {
		for _, r := range label {
			if r >= 0x660 && r <= 0x669 {
				return false
			}
		}
		return true
	}
	return false
}
func containsDirection(set, d string) bool { return strings.Contains("|"+set+"|", "|"+d+"|") }
func checkBidi(label []rune) error {
	bidi := false
	for _, c := range label {
		d := property(c).Bidi
		if d == "" {
			return idnaError("IDNABidiError", "Unknown directionality")
		}
		if containsDirection("R|AL|AN", d) {
			bidi = true
		}
	}
	if !bidi {
		return nil
	}
	d := property(label[0]).Bidi
	rtl := containsDirection("R|AL", d)
	if !rtl && d != "L" {
		return idnaError("IDNABidiError", "First codepoint direction must be L, R or AL")
	}
	ending := false
	number := ""
	for _, c := range label {
		d = property(c).Bidi
		if rtl {
			if !containsDirection("R|AL|AN|EN|ES|CS|ET|ON|BN|NSM", d) {
				return idnaError("IDNABidiError", "Invalid RTL direction")
			}
			if containsDirection("R|AL|EN|AN", d) {
				ending = true
			} else if d != "NSM" {
				ending = false
			}
			if containsDirection("AN|EN", d) {
				if number == "" {
					number = d
				} else if number != d {
					return idnaError("IDNABidiError", "Mixed numeral types")
				}
			}
		} else {
			if !containsDirection("L|EN|ES|CS|ET|ON|BN|NSM", d) {
				return idnaError("IDNABidiError", "Invalid LTR direction")
			}
			if containsDirection("L|EN", d) {
				ending = true
			} else if d != "NSM" {
				ending = false
			}
		}
	}
	if !ending {
		return idnaError("IDNABidiError", "Illegal ending direction")
	}
	return nil
}
