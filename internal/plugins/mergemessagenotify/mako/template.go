package mako

import (
	"context"
	"errors"
	"math/big"
	"net/url"
	"strings"
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
	p := templateParser{tokens: tokens}
	nodes, _, err := p.nodes(nil)
	if err != nil {
		return fallback(text, err)
	}
	b := &strings.Builder{}
	module := &environment{ctx: ctx, values: map[string]any{}, output: b, strict: true}
	module.module = module
	for _, n := range nodes {
		if c, ok := n.(codeNode); ok && c.module {
			if _, err = c.render(module); err != nil {
				return fallback(text, err)
			}
		}
	}
	e := &environment{ctx: ctx, values: map[string]any{}, output: b, parent: module, module: module}
	for k, v := range variables {
		e.values[k] = normalize(v)
	}
	e.values["UNDEFINED"] = undefined{}
	e.values["STOP_RENDERING"] = ""
	e.values["context"] = renderContext{e}
	e.values["self"] = selfNamespace{e}
	e.values["local"] = selfNamespace{e}
	e.values["capture"] = callable(captureFunction)
	filtersEnvironment(e)
	if err = installDefinitions(e, nodes); err == nil {
		_, err = renderNodes(e, nodes)
	}
	if err != nil {
		return fallback(text, err)
	}
	return b.String(), nil
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
}

func scanTemplate(s string) ([]templateToken, error) {
	out := []templateToken{}
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
			at := i + 2
			tag := ""
			for at < len(s) && s[at] >= 'a' && s[at] <= 'z' {
				tag += s[at : at+1]
				at++
			}
			known := tag == "text" || tag == "doc" || tag == "def" || tag == "block" || tag == "page" || tag == "include" || tag == "inherit" || tag == "namespace" || tag == "call"
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
			attrs, err := parseAttributes(strings.TrimSuffix(strings.TrimSpace(header), "/"))
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
			out = append(out, templateToken{kind: "tag", text: tag, attrs: attrs})
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
func parseAttributes(s string) (map[string]string, error) {
	out := map[string]string{}
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		at := strings.IndexByte(s, '=')
		if at < 1 {
			return nil, fault("SyntaxError")
		}
		name := strings.TrimSpace(s[:at])
		s = strings.TrimSpace(s[at+1:])
		if len(s) < 2 || s[0] != '\'' && s[0] != '"' {
			return nil, fault("SyntaxError")
		}
		end := strings.IndexByte(s[1:], s[0])
		if end < 0 {
			return nil, fault("SyntaxError")
		}
		out[name] = strings.ReplaceAll(s[1:1+end], "\r\n", "\n")
		s = s[end+2:]
	}
	return out, nil
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
	if !n.noDefault {
		v, err = pyString(v)
		if err != nil {
			return normal, err
		}
	}
	for _, f := range n.filters {
		fn, er := f.eval(e)
		if er != nil {
			return normal, er
		}
		c, ok := fn.(callable)
		if !ok {
			return normal, fault("NameError")
		}
		v, err = c(e, []any{v}, nil)
		if err != nil {
			return normal, err
		}
	}
	s, ok := v.(string)
	if !ok {
		return normal, fault("TypeError")
	}
	e.output.WriteString(s)
	return normal, nil
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

type failNode struct{}
type missingNamespace struct{}
type namespaceNode struct{ name string }

func (n namespaceNode) render(e *environment) (flow, error) {
	e.values[n.name] = missingNamespace{}
	return normal, nil
}

func (failNode) render(e *environment) (flow, error) { return normal, fault("TemplateLookupException") }

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
	name     string
	params   []parameter
	body     []templateNode
	buffered bool
	filters  []expression
	block    bool
}

func (n defNode) render(e *environment) (flow, error) {
	if n.block {
		fn := e.get(n.name).(callable)
		_, err := fn(e, nil, nil)
		return normal, err
	}
	return normal, nil
}
func (n defNode) install(e *environment) error {
	defaults := map[string]any{}
	for _, p := range n.params {
		if p.defaultValue != nil {
			v, err := p.defaultValue.eval(e.module)
			if err != nil {
				return err
			}
			defaults[p.name] = v
		}
	}
	e.values[n.name] = callable(func(caller *environment, args []any, kw map[string]any) (any, error) {
		if len(args) > len(n.params) {
			return nil, fault("TypeError")
		}
		scope := e.child()
		scope.ctx = caller.ctx
		scope.output = caller.output
		for k, v := range defaults {
			scope.values[k] = v
		}
		for i, p := range n.params {
			if i < len(args) {
				if _, ok := kw[p.name]; ok {
					return nil, fault("TypeError")
				}
				scope.values[p.name] = args[i]
			} else if v, ok := kw[p.name]; ok {
				scope.values[p.name] = v
			} else if _, ok := defaults[p.name]; !ok {
				return nil, fault("TypeError")
			}
		}
		for k := range kw {
			found := false
			for _, p := range n.params {
				if p.name == k {
					found = true
				}
			}
			if !found {
				return nil, fault("TypeError")
			}
		}
		var buffer strings.Builder
		if n.buffered || len(n.filters) > 0 {
			scope.output = &buffer
		}
		if err := installDefinitions(scope, n.body); err != nil {
			return nil, err
		}
		_, err := renderNodes(scope, n.body)
		if err != nil {
			return nil, err
		}
		if n.buffered || len(n.filters) > 0 {
			var result any = buffer.String()
			for _, f := range n.filters {
				fn, err := f.eval(scope)
				if err != nil {
					return nil, err
				}
				call, ok := fn.(callable)
				if !ok {
					return nil, fault("TypeError")
				}
				result, err = call(scope, []any{result}, nil)
				if err != nil {
					return nil, err
				}
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
	return nil
}
func installDefinitions(e *environment, nodes []templateNode) error {
	for _, n := range nodes {
		switch x := n.(type) {
		case defNode:
			if err := x.install(e); err != nil {
				return err
			}
		}
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
