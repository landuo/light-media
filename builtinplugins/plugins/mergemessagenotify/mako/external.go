package mako

import (
	"slices"
	"strings"
)

type includeNode struct{ invocation callExpr }

func (n includeNode) render(e *environment) (flow, error) {
	_, err := n.invocation.eval(e)
	return normal, err
}

func parseInclude(t templateToken) (includeNode, error) {
	file, err := externalFile(t, "args", "import")
	if err != nil {
		return includeNode{}, err
	}
	if raw, ok := t.attrs["import"]; ok {
		if _, err := namespaceAttributeExpression(raw); err != nil {
			return includeNode{}, err
		}
	}
	parsed, err := parseExpression("__include(" + t.attrs["args"] + ")")
	if err != nil {
		return includeNode{}, err
	}
	invocation, ok := parsed.(callExpr)
	if !ok {
		return includeNode{}, fault("SyntaxError")
	}
	invocation.function = literal{callable(includeWithoutLookup)}
	invocation.args = append([]argument{{value: file}}, invocation.args...)
	return includeNode{invocation}, nil
}

func includeWithoutLookup(_ *environment, args []any, kwargs map[string]any) (any, error) {
	if len(args) != 1 {
		return nil, fault("TypeError")
	}
	for _, name := range []string{"context", "uri", "calling_uri"} {
		if _, ok := kwargs[name]; ok {
			return nil, fault("TypeError")
		}
	}
	return nil, fault("TemplateLookupException")
}

func externalFile(t templateToken, optional ...string) (expression, error) {
	raw, ok := t.attrs["file"]
	if !ok {
		return nil, fault("CompileException")
	}
	for name := range t.attrs {
		if name != "file" && !slices.Contains(optional, name) {
			return nil, fault("CompileException")
		}
	}
	return namespaceAttributeExpression(raw)
}

type externalNamespace struct {
	name        string
	file        expression
	imports     bool
	tag         templateToken
	definitions []defNode
}

func (p *templateParser) addExternalNamespace(t templateToken) error {
	if _, file := t.attrs["file"]; !file {
		return ErrUnsupported
	}
	file, err := externalFile(t, "name", "import", "inheritable")
	if err != nil {
		return err
	}
	name, named := t.attrs["name"]
	_, imports := t.attrs["import"]
	if !named && !imports {
		return fault("CompileException")
	}
	for _, attribute := range []string{"name", "import", "inheritable"} {
		if strings.Contains(t.attrs[attribute], "${") {
			return fault("CompileException")
		}
	}
	ns := externalNamespace{name: name, file: file, imports: imports, tag: t}
	if t.extra != "self" {
		// The compiler's top-level pass stops at a NamespaceTag. Parse its
		// body without applying nested page/namespace/inherit declarations
		// to the containing template.
		child := templateParser{tokens: p.tokens, at: p.at, defDepth: 1, compile: p.compile}
		nodes, end, err := child.nodes(map[string]bool{"/namespace": true})
		if err != nil {
			return err
		}
		if end == nil {
			return fault("SyntaxError")
		}
		p.at = child.at
		if err := collectNamespaceDefinitions(nodes, &ns.definitions); err != nil {
			return err
		}
	}
	if named {
		for i, prior := range p.externalNamespaces {
			if prior.name == name {
				p.externalNamespaces[i] = ns
				return nil
			}
		}
	}
	p.externalNamespaces = append(p.externalNamespaces, ns)
	return nil
}

func collectNamespaceDefinitions(nodes []templateNode, definitions *[]defNode) error {
	for _, node := range nodes {
		if definition, ok := node.(defNode); ok {
			if definition.name == "" {
				return fault("CompileException")
			}
			definition.root = false
			*definitions = append(*definitions, definition)
			continue
		}
		bodies := definitionControlBodies(node)
		if call, ok := node.(callNode); ok {
			bodies = append(bodies, call.body)
		}
		for _, body := range bodies {
			if err := collectNamespaceDefinitions(body, definitions); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *environment) externalScope() *environment {
	// Mako emits inheritance and namespace construction as module functions
	// with Context, without the render body's function arguments.
	scope := e.module.child()
	scope.callers = e.callers
	scope.values["context"] = e.get("context")
	return scope
}

func (e *environment) inheritWithoutLookup(file expression) error {
	if err := e.generateExternalNamespaces(); err != nil {
		return err
	}
	uri, err := file.eval(e.externalScope())
	if err != nil || uri == nil {
		return err
	}
	return fault("TemplateLookupException")
}

func (e *environment) generateExternalNamespaces() error {
	if len(e.module.externalNamespaces) == 0 {
		return nil
	}
	// The plugin constructs an in-memory Template without TemplateLookup.
	// The first external namespace therefore prevents generation.
	ns := e.module.externalNamespaces[0]
	scope := e.externalScope()
	uri, err := ns.file.eval(scope)
	if err != nil {
		return err
	}
	// Generated inline callables evaluate their declaration defaults before
	// the TemplateNamespace constructor tries the external lookup.
	for _, definition := range ns.definitions {
		fn, err := definition.bind(scope)
		if err != nil {
			return err
		}
		scope.values[definition.boundName()] = namedCallable{definition.originalName(), fn}
	}
	if uri == nil {
		return fault("TypeError")
	}
	return fault("TemplateLookupException")
}
