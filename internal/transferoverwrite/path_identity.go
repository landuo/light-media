package transferoverwrite

import (
	"light-media/internal/releasemeta"
	"path/filepath"
)

// PathIdentity projects only the three attributes consumed by the installed
// latest-version cleanup. It does not claim to replace a complete MediaInfo.
func PathIdentity(path string, options releasemeta.Options) (Identity, error) {
	parse, err := PathIdentityParser(options)
	if err != nil {
		return Identity{}, err
	}
	return parse(path)
}

// Reuse one frozen configuration for a directory scan. Parsing each sibling
// must not reload host settings or rebuild the parser environment.
func PathIdentityParser(options releasemeta.Options) (func(string) (Identity, error), error) {
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	return func(path string) (Identity, error) {
		file, err := parser.ParseTitle(filepath.Base(path), nil, nil)
		if err != nil {
			return Identity{}, err
		}
		for _, p := range []string{filepath.Base(filepath.Dir(path)), filepath.Base(filepath.Dir(filepath.Dir(path)))} {
			parent, e := parser.ParseTitle(p, nil, nil)
			if e != nil {
				return Identity{}, e
			}
			if file.Text("type") == "电视剧" || parent.Text("type") != "电视剧" {
				if options.SourceMode == "rust" {
					file.MergeRust(parent)
				} else {
					file.MergeLegacy(parent)
				}
			}
		}
		var part *string
		if s, ok := file.Get("part").(string); ok {
			part = &s
		}
		return Identity{Season: file.Season(), Episode: file.Episode(), Part: part}, nil
	}, nil
}
