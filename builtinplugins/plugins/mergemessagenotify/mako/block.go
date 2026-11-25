package mako

import "strings"

func parseMakoParameters(raw string) ([]callbackParameter, error) {
	// Mako's FunctionArgs discards positional-only names and keeps the
	// remaining defaults aligned with the end of its generated signature.
	parts := splitTop(raw, ',')
	omitted := 0
	for i, part := range parts {
		if strings.TrimSpace(part) != "/" {
			continue
		}
		if i == 0 || omitted != 0 {
			return nil, fault("SyntaxError")
		}
		omitted = i
	}
	if omitted != 0 {
		raw = strings.Join(append(parts[:omitted], parts[omitted+1:]...), ",")
	}
	params, err := parsePythonParameters(raw, true)
	if err != nil {
		return nil, err
	}
	if omitted > len(params) {
		return nil, fault("SyntaxError")
	}
	for _, param := range params[:omitted] {
		if param.kind != "positional" {
			return nil, fault("SyntaxError")
		}
	}
	return params[omitted:], nil
}

func parseNamedBlockParameters(raw string) ([]callbackParameter, error) {
	params, err := parseMakoParameters(raw)
	if err != nil {
		return nil, err
	}
	var varargs, positionalDefault bool
	for i := range params {
		param := &params[i]
		if param.name == "context" || param.name == "pageargs" || param.kind == "kwargs" {
			// render_block adds context and **pageargs unconditionally.
			return nil, fault("SyntaxError")
		}
		if param.kind == "varargs" {
			varargs = true
		}
		if param.kind == "keyword" {
			param.forwardKeyword = true
			if !varargs {
				// Mako omits a bare star from the generated declaration,
				// but still forwards the corresponding values by keyword.
				param.kind = "positional"
			}
		}
		if param.kind == "positional" {
			if positionalDefault && param.defaultValue == nil {
				return nil, fault("SyntaxError")
			}
			positionalDefault = positionalDefault || param.defaultValue != nil
		}
	}
	return params, nil
}

func (n defNode) bindArguments(scope *environment, defaults map[string]any, args []any, kwargs map[string]any, keywordOrder []string, original bool) error {
	if n.blockParams == nil {
		return bindDefArguments(scope, n.params, defaults, args, kwargs)
	}
	params := n.blockParams
	if !original {
		params = params[:len(params)-1]
	}
	return bindCallbackArguments(scope, params, defaults, args, kwargs, keywordOrder)
}

func (n defNode) forwardBlockArguments(scope *environment, page bool) ([]any, map[string]any, []string, error) {
	var args []any
	var keywordOrder []string
	kwargs := map[string]any{}
	for _, param := range n.blockParams[:len(n.blockParams)-1] {
		value, exists := scope.values[param.name]
		if !exists {
			return nil, nil, nil, fault("NameError")
		}
		switch {
		case param.kind == "varargs":
			items, err := iterable(value)
			if err != nil {
				return nil, nil, nil, err
			}
			args = append(args, items...)
		case param.forwardKeyword:
			kwargs[param.name] = value
			keywordOrder = append(keywordOrder, param.name)
		default:
			args = append(args, value)
		}
	}
	if page {
		pageargs, ok := scope.values["pageargs"].(dictionary)
		if !ok {
			return nil, nil, nil, fault("NameError")
		}
		for _, pair := range pageargs {
			key, ok := pair.key.(string)
			if _, exists := kwargs[key]; !ok || exists {
				return nil, nil, nil, fault("TypeError")
			}
			kwargs[key] = pair.value
			keywordOrder = append(keywordOrder, key)
		}
	}
	return args, kwargs, keywordOrder, nil
}

func (n defNode) renderNamedBlock(e *environment) error {
	args, kwargs, order, err := n.forwardBlockArguments(e, true)
	if err != nil {
		return err
	}
	self, ok := e.module.contextData.get("self").(selfNamespace)
	if !ok {
		return fault("TypeError")
	}
	value, err := self.attribute(n.name)
	if err != nil {
		return err
	}
	call, ok := asCallable(value)
	if !ok {
		return fault("TypeError")
	}
	active := *e
	active.keywordOrder = order
	_, err = call(&active, args, kwargs)
	return err
}
