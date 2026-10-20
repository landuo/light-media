package builtins

import "light-media/internal/jinja/exec"

var GlobalVariables = exec.NewContext(map[string]any{
	"none": nil,
	"None": nil,
})
