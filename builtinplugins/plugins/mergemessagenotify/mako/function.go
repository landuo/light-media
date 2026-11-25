package mako

import "strings"

type pythonFunction struct {
	name   string
	params []callbackParameter
	body   string
}

func readPythonFunction(lines []string, at int) (pythonFunction, int, error) {
	var f pythonFunction
	header := strings.TrimSpace(lines[at])[len("def "):]
	parts := splitTop(header, ':')
	if len(parts) < 2 {
		return f, at, fault("SyntaxError")
	}
	signature := strings.TrimSpace(parts[0])
	open := strings.IndexByte(signature, '(')
	if open < 1 || !strings.HasSuffix(signature, ")") {
		return f, at, ErrUnsupported
	}
	f.name = strings.TrimSpace(signature[:open])
	tokens, err := lex(f.name)
	if err != nil || len(tokens) != 2 || tokens[0].kind != "name" {
		return f, at, fault("SyntaxError")
	}
	f.params, err = parsePythonParameters(signature[open+1:len(signature)-1], true)
	if err != nil {
		return f, at, err
	}
	if inline := strings.TrimSpace(strings.Join(parts[1:], ":")); inline != "" && !strings.HasPrefix(inline, "#") {
		f.body = inline
		return f, at + 1, nil
	}
	base, next := pythonIndent(lines[at]), at+1
	for next < len(lines) {
		line := strings.TrimSpace(lines[next])
		if line != "" && !strings.HasPrefix(line, "#") && pythonIndent(lines[next]) <= base {
			break
		}
		next++
	}
	f.body = strings.Join(lines[at+1:next], "\n")
	if strings.TrimSpace(f.body) == "" {
		return f, at, fault("IndentationError")
	}
	return f, next, nil
}

func pythonIndent(line string) int {
	var column int
	for _, r := range line {
		switch r {
		case ' ':
			column++
		case '\t':
			column = (column/8 + 1) * 8
		default:
			return column
		}
	}
	return column
}

func (f pythonFunction) bind(lexical *environment) (callable, error) {
	defaults := map[string]any{}
	for _, param := range f.params {
		if param.defaultValue != nil {
			value, err := param.defaultValue.eval(lexical)
			if err != nil {
				return nil, err
			}
			defaults[param.name] = value
		}
	}
	return func(active *environment, args []any, kw map[string]any) (any, error) {
		scope := lexical.child()
		scope.ctx, scope.output = active.ctx, active.output
		if err := bindCallbackArguments(scope, f.params, defaults, args, kw, active.keywordOrder); err != nil {
			return nil, err
		}
		var value any
		_, err := executeCodeResult(scope, f.body, &value)
		return value, err
	}, nil
}
