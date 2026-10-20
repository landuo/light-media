// Package methods provides built-in method implementations for template types.
package methods

import "light-media/internal/jinja/exec"

var All = exec.Methods{
	Bool:  boolMethods,
	Str:   strMethods,
	Int:   intMethods,
	Float: floatMethods,
	Dict:  dictMethods,
	List:  listMethods,
}
