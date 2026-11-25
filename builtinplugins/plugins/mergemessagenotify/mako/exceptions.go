package mako

import (
	"context"
	"errors"
)

type exceptBranch struct {
	kinds []string
	name  string
	body  []templateNode
}
type tryNode struct {
	body, otherwise, finally []templateNode
	handlers                 []exceptBranch
}
type caughtException struct{ err error }

func (n tryNode) render(e *environment) (flow, error) {
	f, err := renderNodes(e, n.body)
	if err == nil && f == normal {
		f, err = renderNodes(e, n.otherwise)
	} else if err != nil {
		if errors.Is(err, ErrUnsupported) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return f, err
		}
		for _, handler := range n.handlers {
			matches := len(handler.kinds) == 0
			for _, kind := range handler.kinds {
				if exceptionMatches(err, kind) {
					matches = true
					break
				}
			}
			if matches {
				if handler.name != "" {
					e.values[handler.name] = caughtException{err}
				}
				f, err = renderNodes(e, handler.body)
				if handler.name != "" {
					delete(e.values, handler.name)
				}
				break
			}
		}
	}
	if len(n.finally) > 0 {
		ff, fe := renderNodes(e, n.finally)
		if ff != normal || fe != nil {
			return ff, fe
		}
	}
	return f, err
}
func exceptionMatches(err error, kind string) bool {
	if kind == "BaseException" || kind == "Exception" {
		return true
	}
	name := err.Error()
	if kind == name {
		return true
	}
	switch kind {
	case "ArithmeticError":
		return name == "ZeroDivisionError" || name == "OverflowError" || name == "FloatingPointError"
	case "LookupError":
		return name == "KeyError" || name == "IndexError"
	case "RuntimeError":
		return name == "RecursionError" || name == "NotImplementedError"
	}
	return false
}
