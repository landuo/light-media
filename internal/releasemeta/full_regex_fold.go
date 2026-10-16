package releasemeta

import "strings"

// All case-sensitive literals in these fixed source expressions are ASCII/CJK.
// Expand their source casefold explicitly instead of using the Go engine's table.
func sourceASCIIFold(c byte, mode string) string {
	if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
		upper := c
		if upper >= 'a' {
			upper -= 32
		}
		s := string([]byte{upper, upper + 32})
		switch upper {
		case 'K':
			s += "K"
		case 'S':
			s += "ſ"
		case 'I':
			if mode == "legacy" {
				s += "İı"
			} else if mode == "legacy_metabase" {
				if c == 'I' {
					s += "ı"
				} else {
					s += "İ"
				}
			}
		}
		return s
	}
	return string(c)
}
func sourceFoldASCII(pattern, mode string) string {
	var out strings.Builder
	for i := 0; i < len(pattern); {
		if pattern[i] == '\\' {
			start := i
			i += 2
			if i > len(pattern) {
				i = len(pattern)
			} else if pattern[start+1] == 'u' && i+4 <= len(pattern) {
				i += 4
			} else if pattern[start+1] == 'x' && i < len(pattern) && pattern[i] == '{' {
				if at := strings.IndexByte(pattern[i:], '}'); at >= 0 {
					i += at + 1
				}
			}
			out.WriteString(pattern[start:i])
			continue
		}
		if strings.HasPrefix(pattern[i:], "(?P<") || strings.HasPrefix(pattern[i:], "(?<") && i+3 < len(pattern) && pattern[i+3] != '=' && pattern[i+3] != '!' {
			at := strings.IndexByte(pattern[i:], '>')
			if at >= 0 {
				out.WriteString(pattern[i : i+at+1])
				i += at + 1
				continue
			}
		}
		if pattern[i] == '[' {
			start := i
			i++
			var members strings.Builder
			for i < len(pattern) && pattern[i] != ']' {
				if pattern[i] == '\\' {
					startEscape := i
					i += 2
					if i > len(pattern) {
						i = len(pattern)
					} else if pattern[startEscape+1] == 'u' && i+4 <= len(pattern) {
						i += 4
					} else if pattern[startEscape+1] == 'x' && i < len(pattern) && pattern[i] == '{' {
						if at := strings.IndexByte(pattern[i:], '}'); at >= 0 {
							i += at + 1
						}
					}
					continue
				}
				c := pattern[i]
				if i+2 < len(pattern) && pattern[i+1] == '-' && pattern[i+2] != ']' {
					for n := int(c); n <= int(pattern[i+2]); n++ {
						if n >= 65 && n <= 90 || n >= 97 && n <= 122 {
							members.WriteString(sourceASCIIFold(byte(n), mode))
						}
					}
					i += 3
				} else {
					if c >= 65 && c <= 90 || c >= 97 && c <= 122 {
						members.WriteString(sourceASCIIFold(c, mode))
					}
					i++
				}
			}
			if i < len(pattern) {
				out.WriteString(pattern[start:i])
				out.WriteString(members.String())
				out.WriteByte(']')
				i++
			} else {
				out.WriteString(pattern[start:])
			}
			continue
		}
		c := pattern[i]
		if c < 128 && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			out.WriteByte('[')
			out.WriteString(sourceASCIIFold(c, mode))
			out.WriteByte(']')
		} else {
			out.WriteByte(c)
		}
		i++
	}
	return out.String()
}
