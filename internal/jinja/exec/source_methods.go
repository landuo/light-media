package exec

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// Offline public CPython 3.12 builtin attribute inventory; never executes Python.
//
//go:embed source-methods.json
var sourceMethodsJSON []byte
var sourceMethods = func() map[string][]string {
	out := map[string][]string{}
	if err := json.Unmarshal(sourceMethodsJSON, &out); err != nil {
		panic(err)
	}
	return out
}()

func sourceMethodExists(v *Value, name string) (exists, known bool) {
	kind := ""
	switch {
	case v.IsNil():
		kind = "None"
	case v.IsString():
		kind = "str"
	case v.IsBool():
		kind = "bool"
	case v.IsInteger():
		kind = "int"
	case v.IsFloat():
		kind = "float"
	case v.IsDict():
		kind = "dict"
	case v.IsList():
		kind = "list"
	}
	if _, ok := v.Interface().(Tuple); ok {
		kind = "tuple"
	}
	if _, ok := v.Interface().(SourceRange); ok {
		kind = "range"
	}
	if kind == "" {
		return false, false
	}
	return slices.Contains(sourceMethods[kind], name), true
}
