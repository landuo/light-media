// Package controlStructures provides built-in control structure implementations.
package controlStructures

import (
	"light-media/internal/jinja/exec"
	"light-media/internal/jinja/parser"
)

var All = exec.NewControlStructureSet(map[string]parser.ControlStructureParser{
	"autoescape": autoescapeParser,
	"block":      blockParser,
	"extends":    extendsParser,
	"filter":     filterParser,
	"for":        forParser,
	"from":       fromParser,
	"if":         ifParser,
	"import":     importParser,
	"include":    includeParser,
	"macro":      macroParser,
	"raw":        rawParser,
	"set":        setParser,
	"with":       withParser,
	"do":         doParser,
	"break":      breakParser,
	"continue":   continueParser,
	"trans":      transParser,
	"call":       callParser,
})
