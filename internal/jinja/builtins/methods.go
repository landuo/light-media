package builtins

import (
	methods "light-media/internal/jinja/builtins/methods"
	"light-media/internal/jinja/exec"
)

// Methods exports all builtins methods.
var Methods exec.Methods = methods.All
