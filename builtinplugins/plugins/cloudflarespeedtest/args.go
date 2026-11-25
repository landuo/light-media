package cloudflarespeedtest

import (
	"errors"
	"strings"
)

var ErrShellSyntax = errors.New("CloudflareSpeedTest additional_args contains unsupported shell syntax")

// SplitArgs supports the source UI's ordinary quoted command arguments without
// running a shell. Shell expansion, redirection and command substitution are
// deliberately rejected: additional_args cannot authorize another program.
func SplitArgs(s string) ([]string, error) {
	var out []string
	var word strings.Builder
	quote := byte(0)
	active := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0 || c == '\r' || c == '\n' || c == '`' || c == '$' && i+1 < len(s) && s[i+1] == '(' {
			return nil, ErrShellSyntax
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			active = true
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
				continue
			}
			// No environment expansion: a quoted $ is literal to exec, unlike
			// the old shell. Refuse it instead of silently changing a command.
			if c == '$' {
				return nil, ErrShellSyntax
			}
			if c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				i++
				c = s[i]
			}
			word.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			active = true
		case '\\':
			if i+1 == len(s) {
				return nil, ErrShellSyntax
			}
			i++
			word.WriteByte(s[i])
			active = true
		case ' ', '\t':
			if active {
				out = append(out, word.String())
				word.Reset()
				active = false
			}
		case ';', '&', '|', '<', '>', '$':
			return nil, ErrShellSyntax
		default:
			word.WriteByte(c)
			active = true
		}
	}
	if quote != 0 {
		return nil, ErrShellSyntax
	}
	if active {
		out = append(out, word.String())
	}
	return out, nil
}
