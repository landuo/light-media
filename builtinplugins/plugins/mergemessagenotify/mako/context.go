package mako

import "strings"

type renderContext struct{ scope *environment }
type selfNamespace struct{ scope *environment }

func (c renderContext) attribute(name string) (any, error) {
	switch name {
	case "write":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 1, 1); err != nil {
				return nil, err
			}
			s, ok := a[0].(string)
			if !ok {
				return nil, fault("TypeError")
			}
			e.output.WriteString(s)
			return nil, nil
		}), nil
	case "get":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 1, 2); err != nil {
				return nil, err
			}
			key, ok := a[0].(string)
			if !ok {
				return nil, fault("TypeError")
			}
			if key == "caller" {
				return e.callers, nil
			}
			v := c.scope.get(key)
			if _, ok := v.(undefined); ok {
				if len(a) > 1 {
					return a[1], nil
				}
				return nil, nil
			}
			return v, nil
		}), nil
	case "keys":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 0, 0); err != nil {
				return nil, err
			}
			return nil, ErrUnsupported
		}), nil
	case "kwargs", "lookup", "namespaces", "pop_caller", "push_caller":
		return nil, ErrUnsupported
	case "caller_stack":
		return c.scope.callers, nil
	}
	return nil, fault("AttributeError")
}
func (s selfNamespace) attribute(name string) (any, error) {
	if fn, ok := s.scope.module.rootCalls[name]; ok {
		// TemplateNamespace exposes a functools.partial, without __name__.
		return namedCallable{call: func(active *environment, args []any, kwargs map[string]any) (any, error) {
			return callWithDefinitionContext(active, s.scope.definitionContext, fn, args, kwargs)
		}}, nil
	}
	v := s.scope.get(name)
	if _, ok := v.(undefined); ok {
		return nil, fault("AttributeError")
	}
	return v, nil
}
func captureFunction(e *environment, a []any, k map[string]any) (any, error) {
	if len(a) == 0 {
		return nil, fault("TypeError")
	}
	fn, ok := asCallable(a[0])
	if !ok {
		return nil, fault("TypeError")
	}
	var b strings.Builder
	scope := e.child()
	scope.output = &b
	_, err := fn(scope, a[1:], k)
	if err != nil {
		return nil, err
	}
	return b.String(), nil
}
