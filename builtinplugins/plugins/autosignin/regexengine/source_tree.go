package regexp2

import "light-media/builtinplugins/plugins/autosignin/regexengine/syntax"

// CompileSourceTree connects the private, already source-validated Python tree
// to the existing code writer/executor. No user pattern is parsed as .NET text.
func CompileSourceTree(pattern string, tree *syntax.RegexTree) (*Regexp, error) {
	code, err := syntax.Write(tree)
	if err != nil {
		return nil, err
	}
	code.PrepareCharSetASCIIBitmaps()
	config := newCompileConfig(nil)
	re := &Regexp{pattern: pattern, options: RegexOptions(tree.Options), caps: code.Caps, capnames: tree.Capnames, capslist: tree.Caplist, capsize: code.Capsize, code: code, quickCode: makeQuickCode(code), MatchTimeout: DefaultMatchTimeout, optimizations: config.optimizations}
	re.stringPrefixFilter = newStringPrefixFilter(code)
	re.initCaptureNames()
	re.initCaches()
	return re, nil
}
