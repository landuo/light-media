// Package pyregex translates the Python pattern syntax used by MoviePilot.
package pyregex

import (
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
)

func Translate(pattern string) string {
	var out strings.Builder
	class := false
	for i := 0; i < len(pattern); {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			// Python regex accepts an escaped underscore as a literal; the .NET
			// parser rejects it. Current shared lists use this in language tags.
			if pattern[i+1] == '_' {
				out.WriteByte('_')
				i += 2
				continue
			}
			out.WriteString(pattern[i : i+2])
			i += 2
			continue
		}
		if pattern[i] == '[' {
			class = true
		}
		if pattern[i] == ']' {
			class = false
		}
		if !class && strings.HasPrefix(pattern[i:], "(?P<") {
			out.WriteString("(?<")
			i += 4
			continue
		}
		if !class && strings.HasPrefix(pattern[i:], "(?P=") {
			if end := strings.IndexByte(pattern[i+4:], ')'); end >= 0 {
				out.WriteString(`\k<` + pattern[i+4:i+4+end] + ">")
				i += 5 + end
				continue
			}
		}
		out.WriteByte(pattern[i])
		i++
	}
	return out.String()
}

func Compile(pattern string, ignoreCase bool) (*regexp2.Regexp, error) {
	options := []regexp2.CompileOption{regexp2.OptionMaintainCaptureOrder()}
	if ignoreCase {
		options = append(options, regexp2.IgnoreCase)
	}
	re, err := regexp2.Compile(Translate(pattern), options...)
	if err == nil {
		re.MatchTimeout = 50 * time.Millisecond
	}
	return re, err
}
