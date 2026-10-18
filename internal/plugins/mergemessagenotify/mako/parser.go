package mako

import "strings"

type templateParser struct {
	tokens []templateToken
	at     int
	loops  int
}

func controlName(s string) string {
	if at := strings.IndexAny(s, " :\t\r\n"); at >= 0 {
		return s[:at]
	}
	return s
}
func (p *templateParser) nodes(stops map[string]bool) ([]templateNode, *templateToken, error) {
	out := []templateNode{}
	for p.at < len(p.tokens) {
		t := p.tokens[p.at]
		p.at++
		if t.kind == "close" {
			if stops["/"+t.text] {
				return out, &t, nil
			}
			return nil, nil, fault("SyntaxError")
		}
		if t.kind == "control" && stops[controlName(t.text)] {
			return out, &t, nil
		}
		switch t.kind {
		case "text":
			out = append(out, textNode(t.text))
		case "expression":
			n, err := parseExprNode(t.text)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, n)
		case "code", "module":
			if err := validateCodeContext(t.text, p.loops, t.kind == "module"); err != nil {
				return nil, nil, err
			}
			out = append(out, codeNode{t.text, t.kind == "module"})
		case "control":
			name := controlName(t.text)
			switch name {
			case "if":
				x, err := parseExpression(controlBody(t.text, "if"))
				if err != nil {
					return nil, nil, err
				}
				n := ifNode{}
				for {
					body, end, err := p.nodes(map[string]bool{"elif": true, "else": true, "endif": true})
					if err != nil {
						return nil, nil, err
					}
					if end == nil {
						return nil, nil, fault("SyntaxError")
					}
					n.branches = append(n.branches, ifBranch{x, body})
					if controlName(end.text) == "endif" {
						break
					}
					if controlName(end.text) == "else" {
						n.otherwise, end, err = p.nodes(map[string]bool{"endif": true})
						if err != nil {
							return nil, nil, err
						}
						if end == nil {
							return nil, nil, fault("SyntaxError")
						}
						break
					}
					x, err = parseExpression(controlBody(end.text, "elif"))
					if err != nil {
						return nil, nil, err
					}
				}
				out = append(out, n)
			case "for":
				header := controlBody(t.text, "for")
				at := strings.Index(header, " in ")
				if at < 1 {
					return nil, nil, fault("SyntaxError")
				}
				names, err := parseTargets(header[:at])
				if err != nil {
					return nil, nil, err
				}
				seq, err := parseExpression(header[at+4:])
				if err != nil {
					return nil, nil, err
				}
				n := forNode{names: names, sequence: seq}
				p.loops++
				body, tend, er := p.nodes(map[string]bool{"else": true, "endfor": true})
				p.loops--
				n.body = body
				if er != nil {
					return nil, nil, er
				}
				if tend == nil {
					return nil, nil, fault("SyntaxError")
				}
				if controlName(tend.text) == "else" {
					n.otherwise, tend, er = p.nodes(map[string]bool{"endfor": true})
					if er != nil {
						return nil, nil, er
					}
					if tend == nil {
						return nil, nil, fault("SyntaxError")
					}
				}
				out = append(out, n)
			case "while":
				x, err := parseExpression(controlBody(t.text, "while"))
				if err != nil {
					return nil, nil, err
				}
				n := whileNode{condition: x}
				p.loops++
				body, tend, er := p.nodes(map[string]bool{"endwhile": true})
				p.loops--
				n.body = body
				if er != nil {
					return nil, nil, er
				}
				if tend == nil {
					return nil, nil, fault("SyntaxError")
				}
				out = append(out, n)
			case "try":
				n := tryNode{}
				body, end, err := p.nodes(map[string]bool{"except": true, "else": true, "finally": true, "endtry": true})
				if err != nil {
					return nil, nil, err
				}
				n.body = body
				for end != nil && controlName(end.text) == "except" {
					signature := controlBody(end.text, "except")
					branch := exceptBranch{}
					if at := strings.Index(signature, " as "); at >= 0 {
						branch.name = strings.TrimSpace(signature[at+4:])
						signature = strings.TrimSpace(signature[:at])
					}
					if signature != "" {
						signature = strings.TrimSuffix(strings.TrimPrefix(signature, "("), ")")
						for _, kind := range strings.Split(signature, ",") {
							kind = strings.TrimSpace(kind)
							if kind != "" {
								branch.kinds = append(branch.kinds, kind)
							}
						}
					}
					branch.body, end, err = p.nodes(map[string]bool{"except": true, "else": true, "finally": true, "endtry": true})
					if err != nil {
						return nil, nil, err
					}
					n.handlers = append(n.handlers, branch)
				}
				if end != nil && controlName(end.text) == "else" {
					// Mako 1.4.1 rejects this Python-valid ternary itself.
					return nil, nil, fault("SyntaxException")
				}
				if end != nil && controlName(end.text) == "finally" {
					return nil, nil, fault("CompileException")
				}
				if end == nil || len(n.handlers) == 0 && len(n.finally) == 0 {
					return nil, nil, fault("SyntaxError")
				}
				out = append(out, n)
			case "break", "continue", "return", "pass":
				return nil, nil, fault("CompileException")
			case "except", "finally", "with", "match", "case":
				return nil, nil, ErrUnsupported
			default:
				return nil, nil, fault("SyntaxError")
			}
		case "tag":
			switch t.text {
			case "def", "block":
				signature := t.attrs["name"]
				if signature == "" {
					return nil, nil, ErrUnsupported
				}
				name, params, err := parseSignature(signature, t.text == "block")
				if err != nil {
					return nil, nil, err
				}
				priorLoops := p.loops
				p.loops = 0
				body, end, err := p.nodes(map[string]bool{"/" + t.text: true})
				p.loops = priorLoops
				if err != nil {
					return nil, nil, err
				}
				if end == nil {
					return nil, nil, fault("SyntaxError")
				}
				n := defNode{name: name, params: params, body: body, buffered: t.attrs["buffered"] == "True", block: t.text == "block"}
				if f := t.attrs["filter"]; f != "" {
					n.filters, err = parseFilters(f)
					if err != nil {
						return nil, nil, err
					}
				}
				for _, key := range []string{"cached", "cache_key", "decorator"} {
					if _, ok := t.attrs[key]; ok {
						return nil, nil, ErrUnsupported
					}
				}
				out = append(out, n)
			case "page":
				if args := t.attrs["args"]; args != "" {
					_, params, err := parseSignature("page("+args+")", false)
					if err != nil {
						return nil, nil, err
					}
					for _, param := range params {
						out = append(out, pageArgNode{param})
					}
				}
				if t.attrs["expression_filter"] != "" || t.attrs["enable_loop"] != "" {
					return nil, nil, ErrUnsupported
				}
			case "include", "inherit":
				out = append(out, failNode{})
			case "namespace":
				if t.attrs["file"] != "" {
					if t.attrs["import"] != "" {
						out = append(out, failNode{})
					} else if t.attrs["name"] != "" {
						out = append(out, namespaceNode{name: t.attrs["name"]})
					} else {
						return nil, nil, fault("CompileException")
					}
				} else {
					return nil, nil, ErrUnsupported
				}
			case "call":
				return nil, nil, ErrUnsupported
			default:
				return nil, nil, fault("SyntaxError")
			}
		default:
			return nil, nil, fault("SyntaxError")
		}
	}
	return out, nil, nil
}
func controlBody(s, name string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(s, name)), ":"))
}
func parseTargets(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimSuffix(s, ")")
	ts, err := lex(s)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for i, t := range ts {
		if t.kind == "EOF" {
			break
		}
		if i%2 == 0 {
			if t.kind != "name" {
				return nil, ErrUnsupported
			}
			names = append(names, t.text)
		} else if t.kind != "," {
			return nil, fault("SyntaxError")
		}
	}
	if len(names) == 0 {
		return nil, fault("SyntaxError")
	}
	return names, nil
}
func parseSignature(s string, block bool) (string, []parameter, error) {
	if block {
		if strings.Contains(s, "(") {
			return "", nil, fault("SyntaxError")
		}
		return s, nil, nil
	}
	at := strings.IndexByte(s, '(')
	if at < 1 || !strings.HasSuffix(s, ")") {
		return "", nil, fault("SyntaxError")
	}
	name := strings.TrimSpace(s[:at])
	ts, err := lex(s[at+1 : len(s)-1])
	if err != nil {
		return "", nil, err
	}
	p := &parser{tokens: ts}
	params := []parameter{}
	for p.peek().kind != "EOF" && p.err == nil {
		t := p.pop()
		if t.kind != "name" {
			return "", nil, ErrUnsupported
		}
		param := parameter{name: t.text}
		if p.take("=") {
			param.defaultValue = p.expr(0)
		}
		params = append(params, param)
		if !p.take(",") {
			break
		}
	}
	if p.err != nil {
		return "", nil, p.err
	}
	if p.peek().kind != "EOF" {
		return "", nil, fault("SyntaxError")
	}
	return name, params, nil
}

type pageArgNode struct{ param parameter }

func (n pageArgNode) render(e *environment) (flow, error) {
	if _, ok := e.values[n.param.name]; ok {
		return normal, nil
	}
	if n.param.defaultValue == nil {
		return normal, fault("TypeError")
	}
	v, err := n.param.defaultValue.eval(e.module)
	if err != nil {
		return normal, err
	}
	e.values[n.param.name] = v
	return normal, nil
}
func splitTop(s string, separator byte) []string {
	out := []string{}
	start, depth := 0, 0
	quote := byte(0)
	triple, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				if !triple {
					quote = 0
				} else if i+2 < len(s) && s[i+1] == quote && s[i+2] == quote {
					quote = 0
					i += 2
				}
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			triple = i+2 < len(s) && s[i+1] == c && s[i+2] == c
			if triple {
				i += 2
			}
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
		}
		if c == ')' || c == ']' || c == '}' {
			depth--
		}
		if depth == 0 && c == separator {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
func parseFilters(s string) ([]expression, error) {
	out := []expression{}
	for _, f := range splitTop(s, ',') {
		f = strings.TrimSpace(f)
		switch f {
		case "h", "x", "u", "trim", "entity", "unicode", "str":
			f = "__filter_" + f
		}
		x, err := parseExpression(f)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}
func parseExprNode(s string) (exprNode, error) {
	parts := splitTop(s, '|')
	x, err := parseExpression(parts[0])
	if err != nil {
		return exprNode{}, err
	}
	n := exprNode{x: x}
	if len(parts) > 2 {
		return n, fault("SyntaxError")
	}
	if len(parts) > 1 {
		filters := []string{}
		for _, f := range splitTop(parts[1], ',') {
			if strings.TrimSpace(f) == "n" {
				n.noDefault = true
			} else {
				filters = append(filters, f)
			}
		}
		if len(filters) > 0 {
			n.filters, err = parseFilters(strings.Join(filters, ","))
		}
	}
	return n, err
}

func executeCode(e *environment, text string) (flow, error) {
	// Code blocks are dedented by Mako before compilation. Top-level multiline
	// assignments are evaluated in their original order, not as interpolation.
	lines := splitTop(strings.TrimSpace(text), '\n')
	for _, line := range lines {
		for _, statement := range splitTop(line, ';') {
			s := strings.TrimSpace(statement)
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			if err := e.check(); err != nil {
				return normal, err
			}
			if s == "pass" {
				continue
			}
			if s == "break" {
				return flowBreak, nil
			}
			if s == "continue" {
				return flowContinue, nil
			}
			if s == "return" {
				return flowReturn, nil
			}
			if strings.HasPrefix(s, "return ") {
				x, err := parseExpression(strings.TrimSpace(s[7:]))
				if err != nil {
					return normal, err
				}
				_, err = x.eval(e)
				return flowReturn, err
			}
			if strings.HasPrefix(s, "import ") || strings.HasPrefix(s, "from ") || strings.HasPrefix(s, "def ") || strings.HasPrefix(s, "class ") || strings.HasPrefix(s, "with ") || strings.HasSuffix(s, ":") {
				return normal, ErrUnsupported
			}
			if strings.HasPrefix(s, "raise ") {
				return normal, fault("RuntimeError")
			}
			if strings.HasPrefix(s, "assert ") {
				parts := splitTop(s[7:], ',')
				x, err := parseExpression(parts[0])
				if err != nil {
					return normal, err
				}
				v, err := x.eval(e)
				if err != nil {
					return normal, err
				}
				if !truth(v) {
					return normal, fault("AssertionError")
				}
				continue
			}
			tokens, err := lex(s)
			if err != nil {
				return normal, err
			}
			depth, assignment := 0, -1
			for i, t := range tokens {
				if t.kind == "(" || t.kind == "[" || t.kind == "{" {
					depth++
				}
				if t.kind == ")" || t.kind == "]" || t.kind == "}" {
					depth--
				}
				if depth == 0 && t.kind == "=" {
					assignment = i
					break
				}
			}
			if assignment >= 0 {
				// Reconstruct from lexer tokens only for the target; retain exact right
				// expression strings (including quoted equals) via top-level splitting.
				parts := splitTop(s, '=')
				if len(parts) != 2 {
					return normal, ErrUnsupported
				}
				target := strings.TrimSpace(parts[0])
				op := ""
				for _, candidate := range []string{"**", "//", "<<", ">>", "+", "-", "*", "/", "%", "&", "|", "^"} {
					if strings.HasSuffix(target, candidate) {
						op = candidate
						target = strings.TrimSpace(strings.TrimSuffix(target, candidate))
						break
					}
				}
				x, err := parseExpression(strings.TrimSpace(parts[1]))
				if err != nil {
					return normal, err
				}
				v, err := x.eval(e)
				if err != nil {
					return normal, err
				}
				names, err := parseTargets(target)
				if err != nil {
					return normal, err
				}
				if op != "" {
					if len(names) != 1 {
						return normal, fault("SyntaxError")
					}
					v, err = binary(op, e.get(names[0]), v)
					if err != nil {
						return normal, err
					}
				}
				if err = assignTargets(e, names, v); err != nil {
					return normal, err
				}
				continue
			}
			x, err := parseExpression(s)
			if err != nil {
				return normal, err
			}
			if _, err = x.eval(e); err != nil {
				return normal, err
			}
		}
	}
	return normal, nil
}
func validateCodeContext(text string, loops int, module bool) error {
	for _, line := range splitTop(text, '\n') {
		for _, stmt := range splitTop(line, ';') {
			stmt = strings.TrimSpace(splitTop(stmt, '#')[0])
			if loops == 0 && (stmt == "break" || stmt == "continue") {
				return fault("SyntaxError")
			}
			if module && (stmt == "return" || strings.HasPrefix(stmt, "return ")) {
				return fault("SyntaxError")
			}
		}
	}
	return nil
}
