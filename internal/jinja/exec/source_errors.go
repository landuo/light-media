package exec

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupported marks native work that is still required. It must never take
// MoviePilot's source-exception fallback path and look like a delivered notice.
var ErrUnsupported = errors.New("Jinja construct requires native implementation")

type SourceError struct{ Class, Message string }

func (e *SourceError) Error() string {
	if e.Message == "" {
		return e.Class
	}
	return e.Class + ": " + e.Message
}
func SourceFailuref(format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	for _, class := range []string{"TypeError", "ValueError", "UndefinedError", "KeyError", "FilterArgumentError", "ZeroDivisionError", "TemplateSyntaxError", "TemplateAssertionError"} {
		if message == class {
			return &SourceError{Class: class}
		}
		if strings.HasPrefix(message, class+":") {
			return &SourceError{Class: class, Message: strings.TrimSpace(strings.TrimPrefix(message, class+":"))}
		}
	}
	return fmt.Errorf("%w: %s", ErrUnsupported, message)
}
func (v *Value) Unwrap() error {
	if v.IsError() {
		return v.Interface().(error)
	}
	return nil
}
