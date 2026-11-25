package mako

import "strings"

func parameterDefaults(e *environment, params []parameter) (map[string]any, error) {
	defaults := map[string]any{}
	for _, param := range params {
		if param.defaultValue != nil {
			value, err := param.defaultValue.eval(e)
			if err != nil {
				return nil, err
			}
			defaults[param.name] = value
		}
	}
	return defaults, nil
}

func bindDefArguments(e *environment, params []parameter, defaults map[string]any, args []any, kwargs map[string]any) error {
	if len(args) > len(params) {
		return fault("TypeError")
	}
	matched := 0
	for i, param := range params {
		value, ok := kwargs[param.name]
		if ok {
			matched++
		}
		switch {
		case i < len(args):
			if ok {
				return fault("TypeError")
			}
			value = args[i]
		case !ok:
			value, ok = defaults[param.name]
			if !ok {
				return fault("TypeError")
			}
		}
		e.values[param.name] = value
	}
	if matched != len(kwargs) {
		return fault("TypeError")
	}
	return nil
}

func installRootDefinitions(e *environment, nodes []templateNode) error {
	var roots []defNode
	indices := map[string]int{}
	var visit func([]templateNode) error
	visit = func(nodes []templateNode) error {
		for _, node := range nodes {
			if n, ok := node.(defNode); ok && n.root {
				if i, duplicate := indices[n.name]; duplicate {
					if n.block || roots[i].block {
						return fault("CompileException")
					}
					roots[i] = n
				} else {
					indices[n.name] = len(roots)
					roots = append(roots, n)
				}
			}
			if n, ok := node.(defNode); ok && n.block {
				if err := visit(n.body); err != nil {
					return err
				}
			}
			for _, body := range definitionControlBodies(node) {
				if err := visit(body); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(nodes); err != nil {
		return err
	}
	e.module.rootDefs = roots
	e.module.rootCalls = make(map[string]callable, len(roots))
	for _, n := range roots {
		fn, err := n.bind(e)
		if err != nil {
			return err
		}
		e.module.rootCalls[n.name] = fn
	}
	return nil
}

// Mako emits a forwarding declaration before a scope renders, only for
// referenced root defs. The declaration evaluates its own argument defaults
// in that scope; self.f bypasses it and uses the module render function.
func installForwardingDefinitions(e *environment, nodes []templateNode, ownerBlock string) error {
	if len(e.module.rootDefs) == 0 && !e.module.hasPageArguments && len(e.module.externalNamespaces) == 0 {
		return nil
	}
	refs := scopeReferences{
		reads: map[string]bool{}, declared: map[string]bool{}, assigned: map[string]bool{},
		namedBlocks: map[string]bool{}, blockArguments: map[string]bool{}, pageArguments: map[string]bool{},
		controlTargets: map[string]bool{},
	}
	refs.nodes(nodes)
	if ownerBlock != "" {
		refs.namedBlocks[ownerBlock] = true
	}
	body := e.parent == e.module
	for name := range refs.pageArguments {
		if e.parameters[name] || name == "context" {
			continue
		}
		// An earlier declaration suppresses the generated Context lookup.
		// render_body also reads every declaration when building __M_locals.
		value := e.module.get(name)
		if _, missing := value.(undefined); missing || refs.assigned[name] || refs.controlTargets[name] {
			if body {
				return fault("NameError")
			}
			value = unboundPageArgument{}
		}
		e.values[name] = value
	}
	if body {
		for name := range refs.blockArguments {
			if !e.parameters[name] {
				// render_body initializes its local context before executing
				// any template statement, even when a block is never reached.
				return fault("NameError")
			}
		}
		e.contextLocals = refs.assigned
		for name := range e.parameters {
			if name == "pageargs" && e.variadicPageargs || refs.pageArguments[name] || refs.blockArguments[name] {
				e.contextLocals[name] = true
				continue
			}
			// A page tag inside a definition can change the body signature
			// without declaring the argument in the body's identifier pass.
			// In that case a direct read still generates a Context lookup.
			_, moduleName := e.module.values[name]
			if !refs.declared[name] && refs.reads[name] && !moduleName {
				e.values[name] = e.module.contextData.get(name)
			}
		}
		for name := range refs.pageArguments {
			e.contextLocals[name] = true
		}
	}
	for _, ns := range e.module.externalNamespaces {
		_, moduleName := e.module.values[ns.name]
		_, rootDefinition := e.module.rootCalls[ns.name]
		referenced := refs.reads[ns.name] && !refs.declared[ns.name] && !e.parameters[ns.name] && !moduleName && !rootDefinition
		if ns.imports || referenced {
			return e.generateExternalNamespaces()
		}
	}
	for _, n := range e.module.rootDefs {
		isBlockDeclaration := refs.namedBlocks[n.name]
		if (!refs.reads[n.name] && !isBlockDeclaration) || refs.declared[n.name] || e.parameters[n.name] {
			continue
		}
		// Mako declares a block visited in this scope even when the module
		// has a value with that name. An ordinary read keeps the module value.
		if _, moduleName := e.module.values[n.name]; moduleName && !isBlockDeclaration {
			continue
		}
		if !e.independentDef && hasLexicalDeclaration(e.parent, n.name) {
			continue
		}
		defaults, err := parameterDefaults(e, n.params)
		if err != nil {
			return err
		}
		root := e.module.rootCalls[n.name]
		if e.forwarding == nil {
			e.forwarding = map[string]bool{}
		}
		e.forwarding[n.name] = true
		e.values[n.name] = namedCallable{n.name, func(active *environment, args []any, kwargs map[string]any) (any, error) {
			scope := e.child()
			if err := n.bindArguments(scope, defaults, args, kwargs, active.keywordOrder, false); err != nil {
				return nil, err
			}
			contextScope := e.definitionContext
			if len(e.contextLocals) != 0 {
				contextScope = contextScope.child()
				for name := range e.contextLocals {
					if v, ok := e.values[name]; ok {
						contextScope.values[name] = v
					}
				}
				contextScope.definitionContext = contextScope
			}
			if n.blockParams != nil {
				args, kwargs, order, err := n.forwardBlockArguments(scope, false)
				if err != nil {
					return nil, err
				}
				call := *active
				call.keywordOrder = order
				return callWithDefinitionContext(&call, contextScope, root, args, kwargs)
			}
			values := make([]any, len(n.params))
			for i, param := range n.params {
				values[i] = scope.values[param.name]
			}
			return callWithDefinitionContext(active, contextScope, root, values, nil)
		}}
	}
	return nil
}

func callWithDefinitionContext(active, definitionContext *environment, fn callable, args []any, kwargs map[string]any) (any, error) {
	call := *active
	call.definitionContext = definitionContext
	return fn(&call, args, kwargs)
}

func hasLexicalDeclaration(e *environment, name string) bool {
	for ; e != nil; e = e.parent {
		if e.parameters[name] || e.forwarding[name] {
			return true
		}
		if e.independentDef {
			break
		}
	}
	return false
}

// These references describe the parsed template subset, not an independent
// Python parser. The compiler collects references across control branches,
// but a definition body and a call body have their own generated scopes.
type scopeReferences struct {
	reads, declared, assigned, namedBlocks, blockArguments map[string]bool
	pageArguments, controlTargets                          map[string]bool
}

func (r scopeReferences) nodes(nodes []templateNode) {
	for _, node := range nodes {
		switch n := node.(type) {
		case exprNode:
			r.expressions(n.x)
			r.expressions(n.filters...)
		case ifNode:
			for _, branch := range n.branches {
				r.expressions(branch.condition)
			}
		case forNode:
			r.expressions(n.sequence)
			for _, name := range n.names {
				r.declared[name] = true
				r.controlTargets[name] = true
			}
		case whileNode:
			r.expressions(n.condition)
		case tryNode:
			for _, branch := range n.handlers {
				r.declared[branch.name] = true
				r.controlTargets[branch.name] = true
			}
		case codeNode:
			if !n.module {
				r.code(n.text)
			}
		case defNode:
			if !n.root && n.name != "" {
				r.declared[n.name] = true
			}
			if n.root && n.block {
				r.namedBlocks[n.name] = true
				for _, param := range n.params {
					r.blockArguments[param.name] = true
				}
			}
			if n.block {
				r.nodes(n.body)
			}
			for _, p := range n.params {
				r.expressions(p.defaultValue)
			}
			r.expressions(n.decorator)
		case callNode:
			r.expressions(n.expression)
			for _, p := range n.params {
				r.expressions(p.defaultValue)
			}
		case includeNode:
			r.expressions(n.invocation)
		case pageArgNode:
			r.declared[n.name] = true
			r.pageArguments[n.name] = true
		}
		for _, body := range definitionControlBodies(node) {
			r.nodes(body)
		}
	}
}

func (r scopeReferences) expressions(expressions ...expression) {
	for _, expr := range expressions {
		switch x := expr.(type) {
		case nameExpr:
			r.reads[string(x)] = true
		case unaryExpr:
			r.expressions(x.x)
		case binaryExpr:
			r.expressions(x.a, x.b)
		case conditionalExpr:
			r.expressions(x.condition, x.yes, x.no)
		case comparison:
			r.expressions(x.first)
			r.expressions(x.rest...)
		case sequenceExpr:
			r.expressions(x.items...)
		case dictExpr:
			r.expressions(x.keys...)
			r.expressions(x.values...)
		case attribute:
			r.expressions(x.base)
		case subscript:
			r.expressions(x.base, x.index, x.stop, x.step)
		case callExpr:
			r.expressions(x.function)
			for _, arg := range x.args {
				r.expressions(arg.value)
			}
		case lambdaExpr:
			r.nestedExpressions(x.parameters, x.body)
		case comprehension:
			r.expressions(x.sequence)
			r.nestedExpressions(x.target, x.item, x.key, x.condition)
		}
	}
}

func (r scopeReferences) nestedExpressions(parameters []string, expressions ...expression) {
	nested := scopeReferences{reads: map[string]bool{}}
	nested.expressions(expressions...)
	for _, parameter := range parameters {
		delete(nested.reads, parameter)
	}
	for name := range nested.reads {
		r.reads[name] = true
	}
}

func (r scopeReferences) code(text string) {
	lines := splitTop(text, '\n')
	for at := 0; at < len(lines); at++ {
		line := strings.TrimSpace(lines[at])
		if strings.HasPrefix(line, "def ") {
			fn, next, err := readPythonFunction(lines, at)
			if err != nil {
				continue
			}
			r.declared[fn.name] = true
			if r.assigned != nil {
				r.assigned[fn.name] = true
			}
			nested := scopeReferences{reads: map[string]bool{}, declared: map[string]bool{}}
			for _, param := range fn.params {
				r.expressions(param.defaultValue)
				nested.declared[param.name] = true
			}
			nested.code(fn.body)
			for name := range nested.reads {
				if !nested.declared[name] {
					r.reads[name] = true
				}
			}
			at = next - 1
			continue
		}
		for _, stmt := range splitTop(line, ';') {
			stmt = strings.TrimSpace(splitTop(stmt, '#')[0])
			if strings.HasPrefix(stmt, "global ") {
				for _, name := range strings.Split(stmt[len("global "):], ",") {
					r.declared[strings.TrimSpace(name)] = true
				}
				continue
			}
			parts := splitTop(stmt, '=')
			if len(parts) == 2 {
				target := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(parts[0]), "+-*/%<>&|^"))
				if names, err := parseTargets(target); err == nil {
					for _, name := range names {
						r.declared[name] = true
						if r.assigned != nil {
							r.assigned[name] = true
						}
					}
					stmt = parts[1]
				}
			}
			stmt = strings.TrimPrefix(strings.TrimPrefix(stmt, "return "), "assert ")
			if expr, err := parseExpression(stmt); err == nil {
				r.expressions(expr)
			}
		}
	}
}
