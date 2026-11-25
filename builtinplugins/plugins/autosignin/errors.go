package autosignin

import (
	"errors"
	legacyanime "light-media/builtinplugins/plugins/autosignin/pyvalue"
)

var ErrSiteTargetDenied = errors.New("site transport target is not authorized")
var ErrSiteEncodingNotReady = errors.New("site response encoding unavailable")

func sourcePythonSpace(r rune) bool { return legacyanime.Strip(string(r)) == "" }

type boundaryFailure struct{ cause error }

func (e *boundaryFailure) Error() string { return e.cause.Error() }
func (e *boundaryFailure) Unwrap() error { return e.cause }

type sourceException struct{ cause error }

func (e *sourceException) Error() string { return e.cause.Error() }
func (e *sourceException) Unwrap() error { return e.cause }
