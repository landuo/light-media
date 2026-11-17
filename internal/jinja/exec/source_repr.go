package exec

import (
	"strings"

	"light-media/internal/pyrepr"
)

func PythonRepr(v *Value) string {
	if v.IsUndefined() {
		return "Undefined"
	}
	if !v.IsString() {
		return v.String()
	}
	return pyrepr.String(v.String())
}

type Tuple []any

func (t Tuple) String() string {
	items := make([]string, len(t))
	for i, v := range t {
		items[i] = PythonRepr(ToValue(v))
	}
	s := strings.Join(items, ", ")
	if len(t) == 1 {
		s += ","
	}
	return "(" + s + ")"
}
