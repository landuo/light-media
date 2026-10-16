package torrentfilter

import "strings"

// stdlib re's word and whitespace classes differ from the third-party regex
// module used by recognition words. Keep this translation local to additional
// filters, including Python's four extra Unicode whitespace control codes.
func standardClasses(pattern string) string {
	ascii := strings.HasPrefix(pattern, "(?a)")
	if ascii {
		pattern = strings.TrimPrefix(pattern, "(?a)")
	}
	word := `\p{L}\p{Nd}\p{Nl}\p{No}_`
	if ascii {
		word = "A-Za-z0-9_"
	}
	wordAtom := `(?-i:[` + word + `])`
	alias := func(c byte) string {
		switch c {
		case 'w':
			return wordAtom
		case 'W':
			return `(?-i:[^` + word + `])`
		case 'd':
			if ascii {
				return `[0-9]`
			}
			return `\d`
		case 'D':
			if ascii {
				return `[^0-9]`
			}
			return `\D`
		case 's':
			if ascii {
				return `[ \t\n\r\f\v]`
			}
			return `[\s\x1c-\x1f]`
		case 'S':
			if ascii {
				return `[^ \t\n\r\f\v]`
			}
			return `[^\s\x1c-\x1f]`
		}
		return ""
	}
	class := func(raw string) string {
		negative := strings.HasPrefix(raw, "^")
		if negative {
			raw = raw[1:]
		}
		var ordinary strings.Builder
		parts := []string{}
		for i := 0; i < len(raw); i++ {
			if raw[i] == '\\' && i+1 < len(raw) {
				if value := alias(raw[i+1]); value != "" {
					parts = append(parts, value)
					i++
					continue
				}
				ordinary.WriteByte(raw[i])
				i++
			}
			ordinary.WriteByte(raw[i])
		}
		if len(parts) == 0 {
			if negative {
				return "[^" + raw + "]"
			}
			return "[" + raw + "]"
		}
		if ordinary.Len() > 0 {
			parts = append(parts, "["+ordinary.String()+"]")
		}
		union := "(?:" + strings.Join(parts, "|") + ")"
		if negative {
			return "(?:(?!" + union + ")[\\s\\S])"
		}
		return union
	}
	var out strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '[' {
			start := i + 1
			j := start
			if j < len(pattern) && pattern[j] == '^' {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) {
				if pattern[j] == '\\' && j+1 < len(pattern) {
					j += 2
					continue
				}
				if pattern[j] == ']' {
					break
				}
				j++
			}
			if j < len(pattern) {
				out.WriteString(class(pattern[start:j]))
				i = j
				continue
			}
		}
		if c == '\\' && i+1 < len(pattern) {
			i++
			value := pattern[i]
			if replacement := alias(value); replacement != "" {
				out.WriteString(replacement)
				continue
			}
			if value == 'b' {
				out.WriteString(`(?:(?<=` + wordAtom + `)(?!` + wordAtom + `)|(?<!` + wordAtom + `)(?=` + wordAtom + `))`)
				continue
			}
			if value == 'B' {
				out.WriteString(`(?:(?!\A\z)(?:(?<=` + wordAtom + `)(?=` + wordAtom + `)|(?<!` + wordAtom + `)(?!` + wordAtom + `)))`)
				continue
			}
			out.WriteByte('\\')
			out.WriteByte(value)
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}
