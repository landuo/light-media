// Package pyregex implements the Python3.12 str-pattern subset used by AutoSignIn.
package pyregex

import "light-media/builtinplugins/plugins/autosignin/regexengine"

func Compile(pattern string, ignoreCase bool) (*regexp2.Regexp, error) {
	return CompileAt(pattern, ignoreCase, SourceOrigin{})
}
func CompileAt(pattern string, ignoreCase bool, origin SourceOrigin) (*regexp2.Regexp, error) {
	flags := 0
	if ignoreCase {
		flags = IgnoreCase
	}
	return sourceCompiles.compile(pattern, flags, origin)
}
