package mako

import "strings"

// The tag exposes its namespace to each definition invoked while evaluating
// the call expression. A definition consumes the pending frame and restores
// it on return, so definitions used as arguments see the same caller.
type callerStack struct {
	frames  []*callerNamespace
	pending *callerNamespace
}

func (s *callerStack) current() *callerNamespace {
	return s.frames[len(s.frames)-1]
}

func (s *callerStack) push() *callerNamespace {
	frame := s.pending
	s.frames = append(s.frames, frame)
	s.pending = nil
	return frame
}

func (s *callerStack) pop() {
	s.pending = s.current()
	s.frames = s.frames[:len(s.frames)-1]
}

type callerNamespace struct {
	members map[string]namedCallable
	context renderContext
}

func (n *callerNamespace) attribute(name string) (any, error) {
	if n != nil {
		switch name {
		case "name":
			return "caller", nil
		case "context":
			return n.context, nil
		case "inherits", "module", "template", "filename", "uri":
			return nil, nil
		}
		if fn, ok := n.members[name]; ok {
			return fn, nil
		}
	}
	return nil, fault("AttributeError")
}

type callNode struct {
	expression expression
	params     []callbackParameter
	body       []templateNode
	exports    []*callExportVisit
}

func (p *templateParser) parseCall(t templateToken) (callNode, error) {
	var n callNode
	var err error
	if t.text == "call" {
		raw, exists := t.attrs["expr"]
		if !exists || strings.Contains(raw, "${") {
			return n, fault("CompileException")
		}
		for key := range t.attrs {
			if key != "expr" && key != "args" {
				return n, fault("CompileException")
			}
		}
		n.expression, err = parseExpression(raw)
	} else {
		n.expression, err = namespaceCallExpression(t)
	}
	if err != nil {
		return n, err
	}
	n.params, err = parseCallbackParameters(t.attrs["args"])
	if err != nil || t.extra == "self" {
		return n, err
	}
	loops := p.loops
	p.loops = 0
	p.calls++
	start := p.at
	body, end, err := p.nodes(map[string]bool{"/" + t.text: true})
	p.calls--
	p.loops = loops
	if err != nil {
		return n, err
	}
	if end == nil {
		return n, fault("SyntaxError")
	}
	n.body = body
	n.exports = callerExportTree(p.tokens, start, p.at-1, p.callDefs)
	return n, nil
}

func namespaceCallExpression(t templateToken) (expression, error) {
	namespace, member, _ := strings.Cut(t.text, ":")
	fn, err := parseExpression(namespace + "." + member)
	if err != nil {
		return nil, err
	}
	x := callExpr{function: fn}
	for _, name := range t.attrOrder {
		if name == "args" {
			continue
		}
		arg, err := namespaceAttributeExpression(t.attrs[name])
		if err != nil {
			return nil, err
		}
		x.args = append(x.args, argument{key: name, value: arg})
	}
	return x, nil
}

// Mako constructs a Python concatenation for the shorthand attributes. A
// single interpolation retains its value type rather than coercing to text.
func namespaceAttributeExpression(text string) (expression, error) {
	var result expression
	appendPart := func(part expression) {
		if result == nil {
			result = part
		} else {
			result = binaryExpr{op: "+", a: result, b: part}
		}
	}
	for {
		before, tail, found := strings.Cut(text, "${")
		if !found {
			if before != "" || result == nil {
				appendPart(literal{before})
			}
			return result, nil
		}
		if before != "" {
			appendPart(literal{before})
		}
		code, next, err := scanEmbedded(tail, 0, '}')
		if err != nil {
			return nil, err
		}
		value, err := parseExpression(code)
		if err != nil {
			return nil, err
		}
		appendPart(value)
		text = tail[next:]
	}
}

func (n callNode) render(e *environment) (flow, error) {
	lexical := e.child()
	// The body closes over the current definition's original caller. It
	// writes into the buffer active when the callee actually invokes it.
	lexical.values["caller"] = nil
	if e.callerFrame != nil {
		lexical.values["caller"] = e.callerFrame
	}
	ns := &callerNamespace{members: map[string]namedCallable{}, context: renderContext{e.module.contextData}}
	if err := visitCallerExports(lexical, ns, n.exports); err != nil {
		return normal, err
	}
	defaults := map[string]any{}
	for _, param := range n.params {
		if param.defaultValue != nil {
			value, err := param.defaultValue.eval(lexical)
			if err != nil {
				return normal, err
			}
			defaults[param.name] = value
		}
	}
	ns.members["body"] = namedCallable{"body", func(active *environment, args []any, kwargs map[string]any) (any, error) {
		scope := lexical.child()
		scope.ctx, scope.output = active.ctx, active.output
		if err := bindCallbackArguments(scope, n.params, defaults, args, kwargs, active.keywordOrder); err != nil {
			return nil, err
		}
		scope.parameters = make(map[string]bool, len(n.params))
		for _, param := range n.params {
			scope.parameters[param.name] = true
		}
		if err := installForwardingDefinitions(scope, n.body, ""); err != nil {
			return nil, err
		}
		_, err := renderNodes(scope, n.body)
		return "", err
	}}
	e.callers.pending = ns
	defer func() { e.callers.pending = nil }()
	return (exprNode{x: n.expression}).render(e)
}

type callExportVisit struct {
	children   []*callExportVisit
	definition defNode
	stop       bool
}

// Mako's lexer associates each node with its containing tag and its active
// control line. DefVisitor follows both sets of edges, including duplicates
// that reevaluate declaration defaults, and stops at definitions and blocks.
func callerExportTree(tokens []templateToken, start, end int, defs map[int]defNode) []*callExportVisit {
	if len(defs) == 0 {
		return nil
	}
	type controlVisit struct {
		node, ternary *callExportVisit
	}
	root := &callExportVisit{}
	tags := []*callExportVisit{root}
	var controls []controlVisit
	for i := start; i < end; i++ {
		t := tokens[i]
		switch t.kind {
		case "close":
			if len(tags) > 1 {
				tags = tags[:len(tags)-1]
			}
			continue
		case "tag", "control":
		default:
			continue
		}
		control := ""
		if t.kind == "control" {
			control = controlName(t.text)
			if strings.HasPrefix(control, "end") {
				controls = controls[:len(controls)-1]
				continue
			}
		}
		node := &callExportVisit{definition: defs[i], stop: t.kind == "tag" && (t.text == "def" || t.text == "block")}
		tag := tags[len(tags)-1]
		tag.children = append(tag.children, node)
		ternary := control == "elif" || control == "else" || control == "except" || control == "finally"
		if len(controls) > 0 {
			active := &controls[len(controls)-1]
			active.node.children = append(active.node.children, node)
			if ternary {
				active.ternary = node
			} else if active.ternary != nil {
				active.ternary.children = append(active.ternary.children, node)
			}
		}
		switch control {
		case "if", "for", "while", "try":
			controls = append(controls, controlVisit{node: node})
		}
		if t.kind == "tag" && t.extra != "self" {
			tags = append(tags, node)
		}
	}
	return root.children
}

func visitCallerExports(scope *environment, ns *callerNamespace, nodes []*callExportVisit) error {
	for _, node := range nodes {
		if err := scope.check(); err != nil {
			return err
		}
		if def := node.definition; def.name != "" {
			fn, err := def.bindWithDefaults(scope, scope)
			if err != nil {
				return err
			}
			bound := namedCallable{def.boundName(), fn}
			scope.values[def.name] = bound
			ns.members[bound.name] = bound
		} else if !node.stop {
			if err := visitCallerExports(scope, ns, node.children); err != nil {
				return err
			}
		}
	}
	return nil
}

type callbackParameter struct {
	parameter
	kind           string
	forwardKeyword bool
}

func parseCallbackParameters(raw string) ([]callbackParameter, error) {
	return parsePythonParameters(raw, false)
}

func parsePythonParameters(raw string, preserveKeywordOnly bool) ([]callbackParameter, error) {
	tokens, err := lex(raw)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	var params []callbackParameter
	names := map[string]bool{}
	star, keywordOnly, positionalDefault := false, false, false
	for !p.is("EOF") && p.err == nil {
		kind := "positional"
		switch {
		case p.take("/"):
			// Mako's FunctionArgs omits Python positional-only parameters.
			// The defaults association after that omission needs separate proof.
			return nil, ErrUnsupported
		case p.take("**"):
			kind = "kwargs"
		case p.take("*"):
			if star {
				return nil, fault("SyntaxError")
			}
			star = true
			if p.take(",") {
				// A bare * is lost when Mako reconstructs the declaration.
				if p.is("EOF") || p.is("**") {
					return nil, fault("SyntaxError")
				}
				keywordOnly = preserveKeywordOnly
				continue
			}
			kind = "varargs"
			keywordOnly = true
		case keywordOnly:
			kind = "keyword"
		}
		name := p.pop()
		if name.kind != "name" || names[name.text] {
			return nil, fault("SyntaxError")
		}
		names[name.text] = true
		param := callbackParameter{parameter: parameter{name: name.text}, kind: kind}
		if p.take(":") {
			// Mako reconstructs arguments without their annotations.
			p.expr(0)
		}
		if p.take("=") {
			if kind == "varargs" || kind == "kwargs" {
				return nil, fault("SyntaxError")
			}
			param.defaultValue = p.expr(0)
		}
		if kind == "positional" {
			if positionalDefault && param.defaultValue == nil {
				return nil, fault("SyntaxError")
			}
			positionalDefault = positionalDefault || param.defaultValue != nil
		}
		params = append(params, param)
		if !p.take(",") || kind == "kwargs" {
			break
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	if !p.is("EOF") {
		return nil, fault("SyntaxError")
	}
	return params, nil
}

func bindCallbackArguments(e *environment, params []callbackParameter, defaults map[string]any, args []any, kwargs map[string]any, keywordOrder []string) error {
	used := map[string]bool{}
	var rest string
	for _, param := range params {
		key := param.name
		switch param.kind {
		case "varargs":
			e.values[key] = tuple(args)
			args = nil
		case "kwargs":
			rest = key
		default:
			value, supplied := kwargs[key]
			if param.kind == "positional" && len(args) != 0 {
				if supplied {
					return fault("TypeError")
				}
				value, supplied = args[0], true
				args = args[1:]
			} else if supplied {
				used[key] = true
			} else {
				value, supplied = defaults[key]
			}
			if !supplied {
				return fault("TypeError")
			}
			e.values[key] = value
		}
	}
	if len(args) != 0 || rest == "" && len(used) != len(kwargs) {
		return fault("TypeError")
	}
	if rest != "" {
		extras := dictionary{}
		for _, key := range keywordOrder {
			if value, supplied := kwargs[key]; supplied && !used[key] {
				extras = append(extras, entry{key, value})
			}
		}
		e.values[rest] = extras
	}
	return nil
}
