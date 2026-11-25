package mako

import (
	"context"
	"errors"
	"maps"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Render follows the original TemplateUtil boundary: a source syntax/runtime
// exception returns the original text. Cancellation and unimplemented valid
// constructs remain errors rather than masquerading as that source behavior.
func Render(ctx context.Context, text string, variables map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if text == "" || len(variables) == 0 {
		return text, nil
	}
	tokens, err := scanTemplate(text)
	if err != nil {
		return fallback(text, err)
	}
	p := templateParser{tokens: tokens, compile: &environment{ctx: ctx, strict: true}}
	nodes, _, err := p.nodes(nil)
	if err != nil {
		return fallback(text, err)
	}
	if p.pageTag.text != "" {
		if _, err = p.readCacheOptions(p.pageTag); err != nil {
			return fallback(text, err)
		}
	}
	pageSignature, err := pageParameters(p.pageArgs)
	if err != nil {
		return fallback(text, err)
	}
	if p.pageCached || p.usesPageCacheTimeout {
		if err = p.validateCacheTimeout(p.pageCacheTimeout); err != nil {
			return fallback(text, err)
		}
	}
	for _, ns := range p.externalNamespaces {
		if _, err = p.compileFlag(ns.tag, "inheritable"); err != nil {
			return fallback(text, err)
		}
	}
	b := &strings.Builder{}
	module := &environment{
		ctx: ctx, values: map[string]any{}, output: b, strict: true,
		pageFilter: p.pageFilter, hasPageArguments: p.hasPageArguments, externalNamespaces: p.externalNamespaces,
	}
	module.module = module
	for _, n := range nodes {
		if c, ok := n.(codeNode); ok && c.module {
			if _, err = c.render(module); err != nil {
				return fallback(text, err)
			}
		}
	}
	callers := &callerStack{frames: []*callerNamespace{nil}}
	e := &environment{ctx: ctx, values: map[string]any{}, output: b, parent: module, module: module, callers: callers}
	kwargs := make(map[string]any, len(variables))
	for k, v := range variables {
		kwargs[k] = normalize(v)
		e.values[k] = kwargs[k]
	}
	e.values["self"] = selfNamespace{e}
	e.values["local"] = selfNamespace{e}
	e.values["capture"] = callable(captureFunction)
	e.values["caller"] = callers
	module.contextData = &environment{values: maps.Clone(e.values), callers: callers}
	e.values["UNDEFINED"] = undefined{}
	e.values["STOP_RENDERING"] = ""
	e.values["context"] = renderContext{module.contextData}
	filtersEnvironment(e)
	originalContext := e.child()
	originalContext.parent, originalContext.values = module, maps.Clone(e.values)
	originalContext.definitionContext = originalContext
	e.definitionContext = originalContext
	pageArgs := make([]parameter, 0, len(pageSignature))
	for _, param := range pageSignature {
		pageArgs = append(pageArgs, param.parameter)
	}
	pageDefaults, err := parameterDefaults(module, pageArgs)
	if err == nil && p.pageCached {
		pageDefaults, err = parameterDefaults(module, pageArgs)
	}
	if err != nil {
		return fallback(text, err)
	}
	if err = installRootDefinitions(e, nodes); err != nil {
		return fallback(text, err)
	}
	e.parameters = make(map[string]bool, len(pageArgs))
	for _, param := range pageSignature {
		e.parameters[param.name] = true
		if param.name == "pageargs" && param.kind == "kwargs" {
			e.variadicPageargs = true
		}
	}
	for name := range kwargs {
		// The original render call also reserves the positional parameters
		// of Template.render, Context and _render_context.
		if reservedMakoName(name) || name == "self" || name == "buffer" || name == "tmpl" || name == "callable_" {
			return fallback(text, fault("TypeError"))
		}
	}
	if p.inheritFile != nil {
		if err = e.inheritWithoutLookup(p.inheritFile); err != nil {
			return fallback(text, err)
		}
	}
	// TemplateUtil.render uses keyword arguments exclusively. An input key
	// equal to the variadic positional name still belongs to the keyword map.
	if err = bindCallbackArguments(e, pageSignature, pageDefaults, nil, kwargs, slices.Sorted(maps.Keys(kwargs))); err != nil {
		return fallback(text, err)
	}
	if p.pageCached {
		return fallback(text, unavailableTemplateCache())
	}
	if err = installDefinitions(e, nodes); err == nil {
		_, err = renderNodes(e, nodes)
	}
	if err != nil {
		return fallback(text, err)
	}
	return b.String(), nil
}

func pageParameters(params []callbackParameter) ([]callbackParameter, error) {
	implicitKeywords := len(params) == 0 || params[len(params)-1].kind != "kwargs"
	signature := make([]callbackParameter, 0, len(params)+1)
	var varargs, positionalDefault bool
	for _, param := range params {
		if implicitKeywords && param.name == "pageargs" {
			return nil, fault("SyntaxError")
		}
		if param.kind == "varargs" {
			varargs = true
		}
		if param.kind == "keyword" && !varargs {
			// Mako does not emit a bare star when regenerating a signature.
			param.kind = "positional"
		}
		if param.kind == "positional" {
			if positionalDefault && param.defaultValue == nil {
				return nil, fault("SyntaxError")
			}
			positionalDefault = positionalDefault || param.defaultValue != nil
		}
		signature = append(signature, param)
	}
	if implicitKeywords {
		signature = append(signature, callbackParameter{parameter: parameter{name: "pageargs"}, kind: "kwargs"})
	}
	return signature, nil
}

func fallback(text string, err error) (string, error) {
	if errors.Is(err, ErrUnsupported) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", err
	}
	return text, nil
}

type templateToken struct {
	kind, text, extra string
	attrs             map[string]string
	attrOrder         []string
	line              int
}

func scanTemplate(s string) ([]templateToken, error) {
	out := []templateToken{}
	tagLine, previousTag := 1, 0
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, templateToken{kind: "text", text: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		if i == 0 || s[i-1] == '\n' {
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if strings.HasPrefix(s[j:], "##") || j < len(s) && s[j] == '%' && !strings.HasPrefix(s[j:], "%%") {
				flush()
				end := strings.IndexByte(s[j:], '\n')
				next := len(s)
				line := s[j:]
				if end >= 0 {
					line = s[j : j+end]
					next = j + end + 1
				}
				if strings.HasPrefix(line, "##") {
					i = next
					continue
				}
				line = strings.TrimSpace(line[1:])
				for strings.HasSuffix(line, "\\") && next < len(s) {
					line = strings.TrimSuffix(line, "\\")
					end = strings.IndexByte(s[next:], '\n')
					if end < 0 {
						line += s[next:]
						next = len(s)
					} else {
						line += s[next : next+end]
						next += end + 1
					}
				}
				line = strings.TrimSpace(splitTop(line, '#')[0])
				keyword := controlName(line)
				if !strings.HasPrefix(keyword, "end") {
					switch keyword {
					case "if", "elif", "else", "for", "while", "try", "except", "with":
						if !strings.HasSuffix(line, ":") {
							return nil, fault("CompileException")
						}
						if (keyword == "else" || keyword == "try") && controlBody(line, keyword) != "" {
							return nil, fault("SyntaxException")
						}
					default:
						return nil, fault("CompileException")
					}
				}
				out = append(out, templateToken{kind: "control", text: line})
				i = next
				continue
			}
			if strings.HasPrefix(s[j:], "%%") {
				text.WriteString(s[i:j])
				text.WriteByte('%')
				i = j + 2
				continue
			}
		}
		if strings.HasPrefix(s[i:], "\\\n") {
			i += 2
			continue
		}
		if strings.HasPrefix(s[i:], "\\\r\n") {
			i += 3
			continue
		}
		if strings.HasPrefix(s[i:], "${") {
			flush()
			body, next, err := scanEmbedded(s, i+2, '}')
			if err != nil {
				return nil, err
			}
			out = append(out, templateToken{kind: "expression", text: body})
			i = next
			continue
		}
		if strings.HasPrefix(s[i:], "</%") {
			flush()
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return nil, fault("SyntaxError")
			}
			out = append(out, templateToken{kind: "close", text: strings.TrimSpace(s[i+3 : i+end])})
			i += end + 1
			continue
		}
		if strings.HasPrefix(s[i:], "<%") {
			flush()
			tagStart := i
			at := i + 2
			for at < len(s) {
				r, size := utf8.DecodeRuneInString(s[at:])
				if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == ':') {
					break
				}
				at += size
			}
			tag := s[i+2 : at]
			known := tag == "text" || tag == "doc" || tag == "def" || tag == "block" || tag == "page" || tag == "include" || tag == "inherit" || tag == "namespace" || tag == "call" || strings.Contains(tag, ":")
			if !known {
				module := strings.HasPrefix(s[i+2:], "!")
				start := i + 2
				if module {
					start++
				}
				body, next, err := scanEmbedded(s, start, '%')
				if err != nil {
					return nil, err
				}
				kind := "code"
				if module {
					kind = "module"
				}
				out = append(out, templateToken{kind: kind, text: body})
				i = next
				continue
			}
			header, next, err := scanHeader(s, at)
			if err != nil {
				return nil, err
			}
			selfClosing := strings.HasSuffix(strings.TrimSpace(header), "/")
			attrs, attrOrder, err := parseAttributes(strings.TrimSuffix(strings.TrimSpace(header), "/"))
			if err != nil {
				return nil, err
			}
			i = next
			if tag == "text" || tag == "doc" {
				end := strings.Index(s[i:], "</%"+tag+">")
				if end < 0 {
					return nil, fault("SyntaxError")
				}
				if tag == "text" {
					out = append(out, templateToken{kind: "text", text: s[i : i+end]})
				}
				i += end + len(tag) + 4
				continue
			}
			tagLine += strings.Count(s[previousTag:tagStart], "\n")
			previousTag = tagStart
			out = append(out, templateToken{kind: "tag", text: tag, attrs: attrs, attrOrder: attrOrder, line: tagLine})
			if selfClosing {
				out[len(out)-1].extra = "self"
			}
			continue
		}
		text.WriteByte(s[i])
		i++
	}
	flush()
	return out, nil
}
func scanEmbedded(s string, start int, end byte) (string, int, error) {
	depth := 0
	quote := byte(0)
	triple := false
	escaped := false
	comment := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if comment {
			if c == '\n' {
				comment = false
			}
			continue
		}
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
		if c == '#' {
			comment = true
			continue
		}
		if depth == 0 && c == end {
			if end == '}' {
				return s[start:i], i + 1, nil
			}
			if end == '%' && i+1 < len(s) && s[i+1] == '>' {
				return s[start:i], i + 2, nil
			}
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
		}
		if c == ')' || c == ']' || c == '}' {
			depth--
			if depth < 0 {
				return "", 0, fault("SyntaxError")
			}
		}
	}
	return "", 0, fault("SyntaxError")
}
func scanHeader(s string, start int) (string, int, error) {
	quote := byte(0)
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '>' {
			return s[start:i], i + 1, nil
		}
	}
	return "", 0, fault("SyntaxError")
}
func parseAttributes(s string) (map[string]string, []string, error) {
	out := map[string]string{}
	var order []string
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		at := strings.IndexByte(s, '=')
		if at < 1 {
			return nil, nil, fault("SyntaxError")
		}
		name := strings.TrimSpace(s[:at])
		s = strings.TrimSpace(s[at+1:])
		if len(s) < 2 || s[0] != '\'' && s[0] != '"' {
			return nil, nil, fault("SyntaxError")
		}
		end := strings.IndexByte(s[1:], s[0])
		if end < 0 {
			return nil, nil, fault("SyntaxError")
		}
		if _, exists := out[name]; !exists {
			order = append(order, name)
		}
		out[name] = strings.ReplaceAll(s[1:1+end], "\r\n", "\n")
		s = s[end+2:]
	}
	return out, order, nil
}

type flow int

const (
	normal flow = iota
	flowBreak
	flowContinue
	flowReturn
)

type templateNode interface {
	render(*environment) (flow, error)
}
type textNode string

func (n textNode) render(e *environment) (flow, error) {
	if err := e.check(); err != nil {
		return normal, err
	}
	e.output.WriteString(string(n))
	return normal, nil
}

type exprNode struct {
	x         expression
	filters   []expression
	noDefault bool
}

func (n exprNode) render(e *environment) (flow, error) {
	v, err := n.x.eval(e)
	if err != nil {
		return normal, err
	}
	var page templateFilters
	if !n.noDefault {
		page = e.module.pageFilter
	}
	if !n.noDefault && !page.noDefault {
		v, err = pyString(v)
		if err != nil {
			return normal, err
		}
	}
	v, err = applyFilters(e, v, page.values, "NameError")
	if err != nil {
		return normal, err
	}
	v, err = applyFilters(e, v, n.filters, "NameError")
	if err != nil {
		return normal, err
	}
	s, ok := v.(string)
	if !ok {
		return normal, fault("TypeError")
	}
	e.output.WriteString(s)
	return normal, nil
}

func applyFilters(e *environment, value any, filters []expression, invalidClass string) (any, error) {
	for _, filter := range filters {
		fn, err := filter.eval(e)
		if err != nil {
			return nil, err
		}
		call, ok := asCallable(fn)
		if !ok {
			return nil, fault(invalidClass)
		}
		value, err = call(e, []any{value}, nil)
		if err != nil {
			return nil, err
		}
	}
	return value, nil
}

type ifBranch struct {
	condition expression
	body      []templateNode
}
type ifNode struct {
	branches  []ifBranch
	otherwise []templateNode
}

func (n ifNode) render(e *environment) (flow, error) {
	for _, br := range n.branches {
		v, err := br.condition.eval(e)
		if err != nil {
			return normal, err
		}
		if truth(v) {
			return renderNodes(e, br.body)
		}
	}
	return renderNodes(e, n.otherwise)
}

type forNode struct {
	names           []string
	sequence        expression
	body, otherwise []templateNode
}

func (n forNode) render(e *environment) (flow, error) {
	v, err := n.sequence.eval(e)
	if err != nil {
		return normal, err
	}
	items, err := iterable(v)
	if err != nil {
		return normal, err
	}
	parent, _ := e.get("loop").(*loopState)
	old, had := e.values["loop"]
	defer func() {
		if had {
			e.values["loop"] = old
		} else {
			delete(e.values, "loop")
		}
	}()
	loop := &loopState{length: len(items), parent: parent}
	e.values["loop"] = loop
	for i, x := range items {
		if err = e.check(); err != nil {
			return normal, err
		}
		loop.index = i
		if err = assignTargets(e, n.names, x); err != nil {
			return normal, err
		}
		f, err := renderNodes(e, n.body)
		if err != nil {
			return normal, err
		}
		if f == flowBreak {
			return normal, nil
		}
		if f == flowReturn {
			return f, nil
		}
	}
	return renderNodes(e, n.otherwise)
}

type whileNode struct {
	condition       expression
	body, otherwise []templateNode
}

func (n whileNode) render(e *environment) (flow, error) {
	for {
		if err := e.check(); err != nil {
			return normal, err
		}
		v, err := n.condition.eval(e)
		if err != nil {
			return normal, err
		}
		if !truth(v) {
			break
		}
		f, err := renderNodes(e, n.body)
		if err != nil {
			return normal, err
		}
		if f == flowBreak {
			return normal, nil
		}
		if f == flowReturn {
			return f, nil
		}
	}
	return renderNodes(e, n.otherwise)
}

type flowNode flow

func (n flowNode) render(e *environment) (flow, error) { return flow(n), nil }

type codeNode struct {
	text   string
	module bool
}

func (n codeNode) render(e *environment) (flow, error) { return executeCode(e, n.text) }

type parameter struct {
	name         string
	defaultValue expression
}
type defNode struct {
	name        string
	params      []parameter
	blockParams []callbackParameter
	body        []templateNode
	buffered    bool
	filters     []expression
	block       bool
	cached      bool
	decorator   expression
	anonymous   int
	root        bool
	line        int
}

func (n defNode) render(e *environment) (flow, error) {
	if !n.block {
		return normal, nil
	}
	if n.name == "" {
		fn, err := n.anonymousCallable(e)
		if err != nil {
			return normal, err
		}
		_, err = fn(e, nil, nil)
		return normal, err
	}
	return normal, n.renderNamedBlock(e)
}
func (n defNode) install(e *environment) error {
	fn, err := n.bind(e)
	if err != nil {
		return err
	}
	e.values[n.name] = namedCallable{n.boundName(), fn}
	return nil
}
func (n defNode) bind(e *environment) (callable, error) {
	if n.root {
		return n.bindWithDefaults(e, e.module)
	}
	return n.bindWithDefaults(e, e)
}

func (n defNode) bindWithDefaults(e, defaultsScope *environment) (callable, error) {
	topLevel := n.root
	var decorator any
	if n.decorator != nil {
		decoratorScope := e
		if topLevel {
			decoratorScope = e.module
		}
		var err error
		decorator, err = n.decorator.eval(decoratorScope)
		if err != nil {
			return nil, err
		}
	}
	defaults, err := parameterDefaults(defaultsScope, n.params)
	if err != nil {
		return nil, err
	}
	body := callable(func(caller *environment, args []any, kw map[string]any) (any, error) {
		lexical := e
		if topLevel {
			lexical = caller.definitionContext
		}
		scope := lexical.child()
		scope.ctx, scope.output = caller.ctx, caller.output
		scope.independentDef = n.root
		scope.parameters = make(map[string]bool, len(n.params))
		for _, param := range n.params {
			scope.parameters[param.name] = true
		}
		if err := n.bindArguments(scope, defaults, args, kw, caller.keywordOrder, true); err != nil {
			return nil, err
		}
		if n.blockParams != nil {
			scope.parameters["pageargs"] = true
		}
		if topLevel {
			scope.values["context"] = renderContext{lexical}
		}
		var buffer strings.Builder
		if n.buffered || len(n.filters) > 0 {
			scope.output = &buffer
		}
		err := func() error {
			scope.callerFrame = scope.callers.push()
			defer scope.callers.pop()
			if _, isParameter := scope.values["caller"]; !isParameter {
				scope.values["caller"] = scope.callers
			}
			ownerBlock := ""
			if n.blockParams != nil {
				ownerBlock = n.name
			}
			if err := installForwardingDefinitions(scope, n.body, ownerBlock); err != nil {
				return err
			}
			if err := installScopeDefinitions(scope, n.body, false); err != nil {
				return err
			}
			_, err := renderNodes(scope, n.body)
			return err
		}()
		if err != nil {
			return nil, err
		}
		if n.buffered || len(n.filters) > 0 {
			result, err := applyFilters(scope, buffer.String(), n.filters, "TypeError")
			if err != nil {
				return nil, err
			}
			if n.buffered {
				return result, nil
			}
			s, err := pyString(result)
			if err != nil {
				return nil, err
			}
			caller.output.WriteString(s)
		}
		return "", nil
	})
	if n.decorator != nil && !topLevel {
		wrapped, err := invokeDecorator(e, decorator, namedCallable{n.originalName(), body})
		if err != nil {
			return nil, err
		}
		body = func(active *environment, args []any, kw map[string]any) (any, error) {
			return invokeDecorated(active, renderContext{e.definitionContext}, wrapped, args, kw)
		}
	}
	if n.cached {
		cacheDefaults, err := parameterDefaults(defaultsScope, n.params)
		if err != nil {
			return nil, err
		}
		return func(active *environment, args []any, kw map[string]any) (any, error) {
			if err := n.bindArguments(e.child(), cacheDefaults, args, kw, active.keywordOrder, true); err != nil {
				return nil, err
			}
			return nil, unavailableTemplateCache()
		}, nil
	}
	if n.decorator != nil && topLevel {
		return func(active *environment, args []any, kw map[string]any) (any, error) {
			definitionContext := active.definitionContext
			original := namedCallable{n.name, func(caller *environment, args []any, kwargs map[string]any) (any, error) {
				return callWithDefinitionContext(caller, definitionContext, body, args, kwargs)
			}}
			wrapped, err := invokeDecorator(active, decorator, original)
			if err != nil {
				return nil, err
			}
			return invokeDecorated(active, renderContext{definitionContext}, wrapped, args, kw)
		}, nil
	}
	return body, nil
}

func (n defNode) originalName() string {
	if n.name != "" {
		return n.name
	}
	return "__M_anon_" + strconv.Itoa(n.line)
}

func (n defNode) boundName() string {
	if n.decorator != nil && !n.cached && !n.root {
		return "go"
	}
	return n.originalName()
}

func invokeDecorator(active *environment, decorator any, body namedCallable) (any, error) {
	fn, ok := asCallable(decorator)
	if !ok {
		return nil, fault("TypeError")
	}
	return fn(active, []any{body}, nil)
}

func invokeDecorated(active *environment, ctx renderContext, wrapped any, args []any, kw map[string]any) (any, error) {
	fn, ok := asCallable(wrapped)
	if !ok {
		return nil, fault("TypeError")
	}
	return fn(active, append([]any{ctx}, args...), kw)
}

func (n defNode) anonymousCallable(e *environment) (callable, error) {
	if n.decorator == nil {
		return n.bind(e)
	}
	if fn, exists := e.anonymousDefs[n.anonymous]; exists {
		return fn, nil
	}
	fn, err := n.bind(e)
	if err != nil {
		return nil, err
	}
	if e.anonymousDefs == nil {
		e.anonymousDefs = map[int]callable{}
	}
	e.anonymousDefs[n.anonymous] = fn
	return fn, nil
}

func installDefinitions(e *environment, nodes []templateNode) error {
	if err := installForwardingDefinitions(e, nodes, ""); err != nil {
		return err
	}
	return installScopeDefinitions(e, nodes, false)
}

// The generated render function declares modified blocks and defs at scope
// entry, even when the control path that contains their body is not entered.
func installScopeDefinitions(e *environment, nodes []templateNode, inControl bool) error {
	for _, n := range nodes {
		switch x := n.(type) {
		case defNode:
			if x.root {
				continue
			}
			if x.name == "" {
				if x.decorator != nil {
					if _, err := x.anonymousCallable(e); err != nil {
						return err
					}
				}
				continue
			}
			if inControl && x.decorator == nil && !x.cached {
				continue
			}
			if err := x.install(e); err != nil {
				return err
			}
		default:
			for _, body := range definitionControlBodies(n) {
				if err := installScopeDefinitions(e, body, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func definitionControlBodies(node templateNode) [][]templateNode {
	switch n := node.(type) {
	case ifNode:
		bodies := make([][]templateNode, 0, len(n.branches)+1)
		for _, branch := range n.branches {
			bodies = append(bodies, branch.body)
		}
		return append(bodies, n.otherwise)
	case forNode:
		return [][]templateNode{n.body, n.otherwise}
	case whileNode:
		return [][]templateNode{n.body, n.otherwise}
	case tryNode:
		bodies := make([][]templateNode, 1, len(n.handlers)+3)
		bodies[0] = n.body
		for _, handler := range n.handlers {
			bodies = append(bodies, handler.body)
		}
		return append(bodies, n.otherwise, n.finally)
	}
	return nil
}
func renderNodes(e *environment, nodes []templateNode) (flow, error) {
	for _, n := range nodes {
		if err := e.check(); err != nil {
			return normal, err
		}
		if c, ok := n.(codeNode); ok && c.module {
			continue
		}
		f, err := n.render(e)
		if err != nil || f != normal {
			return f, err
		}
	}
	return normal, nil
}

type loopState struct {
	index, length int
	parent        *loopState
}

func (l *loopState) attribute(n string) (any, error) {
	switch n {
	case "index":
		return big.NewInt(int64(l.index)), nil
	case "reverse_index":
		return big.NewInt(int64(l.length - l.index - 1)), nil
	case "first":
		return l.index == 0, nil
	case "last":
		return l.index == l.length-1, nil
	case "even":
		return l.index%2 == 0, nil
	case "odd":
		return l.index%2 != 0, nil
	case "parent":
		if l.parent == nil {
			return nil, nil
		}
		return l.parent, nil
	case "cycle":
		return callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if len(a) == 0 || len(k) > 0 {
				return nil, fault("TypeError")
			}
			return a[l.index%len(a)], nil
		}), nil
	}
	return nil, fault("AttributeError")
}
func filtersEnvironment(e *environment) {
	for _, name := range []string{"h", "x", "u", "trim", "entity", "unicode", "str"} {
		name := name
		e.values["__filter_"+name] = callable(func(e *environment, a []any, k map[string]any) (any, error) {
			if err := arity(a, k, 1, 1); err != nil {
				return nil, err
			}
			s, err := pyString(a[0])
			if err != nil {
				return nil, err
			}
			switch name {
			case "h":
				return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&#34;", "'", "&#39;").Replace(s), nil
			case "x":
				return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&#34;", "'", "&#39;").Replace(s), nil
			case "u":
				return url.QueryEscape(s), nil
			case "trim":
				return strings.TrimFunc(s, pythonSpace), nil
			case "entity":
				return escapeEntities(s), nil
			}
			return s, nil
		})
	}
}
