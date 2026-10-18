package mako

import (
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type token struct{ kind, text string }

func lex(s string) ([]token, error) {
	out := []token{}
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) {
			i += n
			continue
		}
		if r == '#' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		raw := false
		start := i
		if (s[i] == 'r' || s[i] == 'R' || s[i] == 'u' || s[i] == 'U') && i+1 < len(s) && (s[i+1] == '\'' || s[i+1] == '"') {
			raw = s[i] == 'r' || s[i] == 'R'
			i++
		}
		if s[i] == '\'' || s[i] == '"' {
			q := s[i]
			i++
			triple := i+1 < len(s) && s[i] == q && s[i+1] == q
			if triple {
				i += 2
			}
			var b strings.Builder
			closed := false
			for i < len(s) {
				if s[i] == q && (!triple || i+2 < len(s) && s[i+1] == q && s[i+2] == q) {
					i++
					if triple {
						i += 2
					}
					closed = true
					break
				}
				if s[i] == '\\' && i+1 < len(s) {
					i++
					c := s[i]
					i++
					if raw {
						b.WriteByte('\\')
						b.WriteByte(c)
						continue
					}
					switch c {
					case '\n':
						continue
					case '\r':
						if i < len(s) && s[i] == '\n' {
							i++
						}
						continue
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case 'a':
						b.WriteByte(7)
					case 'b':
						b.WriteByte(8)
					case 'f':
						b.WriteByte(12)
					case 'v':
						b.WriteByte(11)
					case '\\', '\'', '"':
						b.WriteByte(c)
					case 'x', 'u', 'U':
						count := 2
						if c == 'u' {
							count = 4
						}
						if c == 'U' {
							count = 8
						}
						if i+count > len(s) {
							return nil, fault("SyntaxError")
						}
						v, e := strconv.ParseUint(s[i:i+count], 16, 32)
						if e != nil || v > utf8.MaxRune {
							return nil, fault("SyntaxError")
						}
						b.WriteRune(rune(v))
						i += count
					default:
						if c >= '0' && c <= '7' {
							oct := string(c)
							for len(oct) < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
								oct += string(s[i])
								i++
							}
							v, _ := strconv.ParseUint(oct, 8, 32)
							b.WriteRune(rune(v))
						} else {
							b.WriteByte('\\')
							b.WriteByte(c)
						}
					}
					continue
				}
				r, n = utf8.DecodeRuneInString(s[i:])
				if !triple && (r == '\n' || r == '\r') {
					return nil, fault("SyntaxError")
				}
				b.WriteRune(r)
				i += n
			}
			if !closed {
				return nil, fault("SyntaxError")
			}
			out = append(out, token{"string", b.String()})
			continue
		}
		i = start
		r, n = utf8.DecodeRuneInString(s[i:])
		if unicode.IsLetter(r) || r == '_' {
			i += n
			for i < len(s) {
				rr, nn := utf8.DecodeRuneInString(s[i:])
				if !(unicode.IsLetter(rr) || unicode.IsDigit(rr) || rr == '_') {
					break
				}
				i += nn
			}
			name := s[start:i]
			if (name == "f" || name == "F" || name == "b" || name == "B") && i < len(s) && (s[i] == '\'' || s[i] == '"') {
				return nil, ErrUnsupported
			}
			out = append(out, token{"name", name})
			continue
		}
		if r >= '0' && r <= '9' || r == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			i += n
			for i < len(s) {
				c := s[i]
				if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || strings.ContainsRune("_xXoObB.", rune(c)) {
					i++
					continue
				}
				if (c == '+' || c == '-') && i > start && (s[i-1] == 'e' || s[i-1] == 'E') {
					i++
					continue
				}
				break
			}
			out = append(out, token{"number", s[start:i]})
			continue
		}
		matched := false
		for _, op := range []string{"**", "//", "<<", ">>", "<=", ">=", "==", "!=", ":=", "->"} {
			if strings.HasPrefix(s[i:], op) {
				out = append(out, token{op, op})
				i += len(op)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if strings.ContainsRune("+-*/%~&|^<>()[]{}.,:=;", r) {
			out = append(out, token{string(r), string(r)})
			i += n
			continue
		}
		return nil, fault("SyntaxError")
	}
	return append(out, token{"EOF", ""}), nil
}

type expression interface {
	eval(*environment) (any, error)
}
type literal struct{ v any }

func (x literal) eval(e *environment) (any, error) { return x.v, e.check() }

type nameExpr string

func (x nameExpr) eval(e *environment) (any, error) {
	v := e.get(string(x))
	if _, ok := v.(undefined); ok && e.strict {
		return nil, fault("NameError")
	}
	return v, e.check()
}

type unaryExpr struct {
	op string
	x  expression
}

func (x unaryExpr) eval(e *environment) (any, error) {
	v, err := x.x.eval(e)
	if err != nil {
		return nil, err
	}
	switch x.op {
	case "not":
		return !truth(v), nil
	case "+":
		if z, ok := integer(v); ok {
			return z, nil
		}
		if f, ok := floating(v); ok {
			return f, nil
		}
	case "-":
		if z, ok := integer(v); ok {
			return z.Neg(z), nil
		}
		if f, ok := floating(v); ok {
			return -f, nil
		}
	case "~":
		if z, ok := integer(v); ok {
			return z.Not(z), nil
		}
	}
	return nil, fault("TypeError")
}

type binaryExpr struct {
	op   string
	a, b expression
}

func (x binaryExpr) eval(e *environment) (any, error) {
	a, err := x.a.eval(e)
	if err != nil {
		return nil, err
	}
	if x.op == "and" && !truth(a) || x.op == "or" && truth(a) {
		return a, nil
	}
	b, err := x.b.eval(e)
	if err != nil {
		return nil, err
	}
	if x.op == "and" || x.op == "or" {
		return b, nil
	}
	return binary(x.op, a, b)
}

type conditionalExpr struct{ yes, condition, no expression }

func (x conditionalExpr) eval(e *environment) (any, error) {
	v, err := x.condition.eval(e)
	if err != nil {
		return nil, err
	}
	if truth(v) {
		return x.yes.eval(e)
	}
	return x.no.eval(e)
}

type comparison struct {
	first expression
	ops   []string
	rest  []expression
}

func (x comparison) eval(e *environment) (any, error) {
	a, err := x.first.eval(e)
	if err != nil {
		return nil, err
	}
	for i, bexpr := range x.rest {
		b, err := bexpr.eval(e)
		if err != nil {
			return nil, err
		}
		v, err := binary(x.ops[i], a, b)
		if err != nil {
			return nil, err
		}
		if !truth(v) {
			return false, nil
		}
		a = b
	}
	return true, nil
}

type sequenceExpr struct {
	items []expression
	tuple bool
}

func (x sequenceExpr) eval(e *environment) (any, error) {
	v := []any{}
	for _, item := range x.items {
		a, err := item.eval(e)
		if err != nil {
			return nil, err
		}
		v = append(v, a)
	}
	if x.tuple {
		return tuple(v), nil
	}
	return v, nil
}

type dictExpr struct{ keys, values []expression }

func (x dictExpr) eval(e *environment) (any, error) {
	d := dictionary{}
	for i, kexpr := range x.keys {
		k, err := kexpr.eval(e)
		if err != nil {
			return nil, err
		}
		v, err := x.values[i].eval(e)
		if err != nil {
			return nil, err
		}
		d, err = dictSet(d, k, v)
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

type attribute struct {
	base expression
	key  string
}

func (x attribute) eval(e *environment) (any, error) {
	v, err := x.base.eval(e)
	if err != nil {
		return nil, err
	}
	return getAttribute(v, x.key)
}

type subscript struct {
	base, index, stop, step expression
	slice                   bool
}

func (x subscript) eval(e *environment) (any, error) {
	v, err := x.base.eval(e)
	if err != nil {
		return nil, err
	}
	if !x.slice {
		idx, err := x.index.eval(e)
		if err != nil {
			return nil, err
		}
		if d, ok := v.(unorderedDictionary); ok {
			v = dictionary(d)
		}
		if d, ok := v.(dictionary); ok {
			r, yes := dictGet(d, idx)
			if !yes {
				return nil, fault("KeyError")
			}
			return r, nil
		}
		items, err := iterable(v)
		if err != nil {
			return nil, err
		}
		i, err := indexInt(idx)
		if err != nil {
			return nil, err
		}
		if i < 0 {
			i += len(items)
		}
		if i < 0 || i >= len(items) {
			return nil, fault("IndexError")
		}
		return items[i], nil
	}
	items, err := iterable(v)
	if err != nil {
		return nil, err
	}
	n := len(items)
	step := 1
	if x.step != nil {
		s, er := x.step.eval(e)
		if er != nil {
			return nil, er
		}
		step, err = indexInt(s)
		if err != nil {
			return nil, err
		}
		if step == 0 {
			return nil, fault("ValueError")
		}
	}
	start, end := 0, n
	if step < 0 {
		start = n - 1
		end = -1
	}
	bound := func(ex expression, def int) (int, error) {
		if ex == nil {
			return def, nil
		}
		v, er := ex.eval(e)
		if er != nil {
			return 0, er
		}
		if v == nil {
			return def, nil
		}
		i, er := indexInt(v)
		if er != nil {
			return 0, er
		}
		if i < 0 {
			i += n
		}
		if step > 0 {
			if i < 0 {
				i = 0
			}
			if i > n {
				i = n
			}
		} else {
			if i < 0 {
				i = -1
			}
			if i >= n {
				i = n - 1
			}
		}
		return i, nil
	}
	start, err = bound(x.index, start)
	if err != nil {
		return nil, err
	}
	end, err = bound(x.stop, end)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for i := start; step > 0 && i < end || step < 0 && i > end; i += step {
		if err = e.check(); err != nil {
			return nil, err
		}
		out = append(out, items[i])
	}
	switch v.(type) {
	case string:
		var b strings.Builder
		for _, a := range out {
			b.WriteString(a.(string))
		}
		return b.String(), nil
	case tuple:
		return tuple(out), nil
	}
	return out, nil
}

type argument struct {
	key    string
	value  expression
	expand int
}
type callExpr struct {
	function expression
	args     []argument
}

func (x callExpr) eval(e *environment) (any, error) {
	fn, err := x.function.eval(e)
	if err != nil {
		return nil, err
	}
	call, ok := fn.(callable)
	if !ok {
		if _, ok := fn.(undefined); ok {
			return nil, fault("NameError")
		}
		return nil, fault("TypeError")
	}
	args := []any{}
	kw := map[string]any{}
	for _, arg := range x.args {
		v, err := arg.value.eval(e)
		if err != nil {
			return nil, err
		}
		if arg.expand == 1 {
			vs, err := iterable(v)
			if err != nil {
				return nil, err
			}
			args = append(args, vs...)
		} else if arg.expand == 2 {
			d, ok := v.(dictionary)
			if !ok {
				return nil, fault("TypeError")
			}
			for _, kv := range d {
				k, ok := kv.key.(string)
				if !ok {
					return nil, fault("TypeError")
				}
				if _, ok = kw[k]; ok {
					return nil, fault("TypeError")
				}
				kw[k] = kv.value
			}
		} else if arg.key != "" {
			if _, ok := kw[arg.key]; ok {
				return nil, fault("TypeError")
			}
			kw[arg.key] = v
		} else {
			args = append(args, v)
		}
	}
	return call(e, args, kw)
}

type lambdaExpr struct {
	parameters []string
	body       expression
}

func (x lambdaExpr) eval(e *environment) (any, error) {
	return callable(func(_ *environment, a []any, k map[string]any) (any, error) {
		if len(a)+len(k) != len(x.parameters) {
			return nil, fault("TypeError")
		}
		scope := e.child()
		for i, n := range x.parameters {
			if i < len(a) {
				scope.values[n] = a[i]
			} else if v, ok := k[n]; ok {
				scope.values[n] = v
			} else {
				return nil, fault("TypeError")
			}
		}
		return x.body.eval(scope)
	}), nil
}

type comprehension struct {
	item, key           expression
	target              []string
	sequence, condition expression
	kind                string
}

func (x comprehension) eval(e *environment) (any, error) {
	v, err := x.sequence.eval(e)
	if err != nil {
		return nil, err
	}
	items, err := iterable(v)
	if err != nil {
		return nil, err
	}
	scope := e.child()
	out := []any{}
	d := dictionary{}
	for _, v := range items {
		if err = scope.check(); err != nil {
			return nil, err
		}
		if err = assignTargets(scope, x.target, v); err != nil {
			return nil, err
		}
		if x.condition != nil {
			ok, er := x.condition.eval(scope)
			if er != nil {
				return nil, er
			}
			if !truth(ok) {
				continue
			}
		}
		a, er := x.item.eval(scope)
		if er != nil {
			return nil, er
		}
		if x.kind == "dict" {
			k, er := x.key.eval(scope)
			if er != nil {
				return nil, er
			}
			d, er = dictSet(d, k, a)
			if er != nil {
				return nil, er
			}
		} else {
			out = append(out, a)
		}
	}
	if x.kind == "dict" {
		return d, nil
	}
	return out, nil
}
func assignTargets(e *environment, names []string, v any) error {
	if len(names) == 1 {
		e.values[names[0]] = v
		return nil
	}
	vs, err := iterable(v)
	if err != nil || len(vs) != len(names) {
		return fault("ValueError")
	}
	for i, n := range names {
		e.values[n] = vs[i]
	}
	return nil
}

type parser struct {
	tokens []token
	at     int
	err    error
}

func parseExpression(s string) (expression, error) {
	ts, err := lex(s)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: ts}
	v := p.expr(0)
	if p.err != nil {
		return nil, p.err
	}
	if p.peek().kind != "EOF" {
		return nil, fault("SyntaxError")
	}
	return v, nil
}
func (p *parser) peek() token {
	if p.at >= len(p.tokens) {
		return token{"EOF", ""}
	}
	return p.tokens[p.at]
}
func (p *parser) pop() token {
	t := p.peek()
	if t.kind != "EOF" {
		p.at++
	}
	return t
}
func (p *parser) is(s string) bool { t := p.peek(); return t.text == s || t.kind == s }
func (p *parser) take(s string) bool {
	if p.is(s) {
		p.pop()
		return true
	}
	return false
}
func (p *parser) require(s string) {
	if !p.take(s) {
		p.err = fault("SyntaxError")
	}
}
func precedence(op string) int {
	switch op {
	case "or":
		return 10
	case "and":
		return 20
	case "==", "!=", "<", "<=", ">", ">=", "in", "is", "not in", "is not":
		return 30
	case "|":
		return 40
	case "^":
		return 45
	case "&":
		return 50
	case "<<", ">>":
		return 60
	case "+", "-":
		return 70
	case "*", "/", "//", "%":
		return 80
	case "**":
		return 100
	}
	return -1
}
func (p *parser) expr(min int) expression {
	if p.err != nil {
		return literal{nil}
	}
	var left expression
	t := p.pop()
	switch t.kind {
	case "number":
		s := strings.ReplaceAll(t.text, "_", "")
		if strings.ContainsAny(s, ".eE") && !strings.HasPrefix(strings.ToLower(s), "0x") {
			v, e := strconv.ParseFloat(s, 64)
			if e != nil {
				p.err = fault("SyntaxError")
			}
			left = literal{v}
		} else {
			base := 10
			if len(s) > 1 && s[0] == '0' && strings.ContainsRune("xXoObB", rune(s[1])) {
				base = 0
			}
			z, ok := new(big.Int).SetString(s, base)
			if !ok {
				p.err = fault("SyntaxError")
			}
			left = literal{z}
		}
	case "string":
		s := t.text
		for p.peek().kind == "string" {
			s += p.pop().text
		}
		left = literal{s}
	case "name":
		switch t.text {
		case "None":
			left = literal{nil}
		case "True":
			left = literal{true}
		case "False":
			left = literal{false}
		case "not":
			left = unaryExpr{"not", p.expr(25)}
		case "lambda":
			names := []string{}
			if !p.is(":") {
				for {
					a := p.pop()
					if a.kind != "name" {
						p.err = fault("SyntaxError")
					}
					names = append(names, a.text)
					if !p.take(",") {
						break
					}
				}
			}
			p.require(":")
			left = lambdaExpr{names, p.expr(0)}
		default:
			left = nameExpr(t.text)
		}
	case "+", "-", "~":
		left = unaryExpr{t.kind, p.expr(90)}
	case "(":
		if p.take(")") {
			left = literal{tuple{}}
		} else {
			first := p.expr(0)
			if p.take(",") {
				items := []expression{first}
				for !p.is(")") && p.err == nil {
					items = append(items, p.expr(0))
					if !p.take(",") {
						break
					}
				}
				p.require(")")
				left = sequenceExpr{items, true}
			} else {
				p.require(")")
				left = first
			}
		}
	case "[":
		if p.take("]") {
			left = literal{[]any{}}
		} else {
			first := p.expr(0)
			if p.take("for") {
				left = p.comprehend(first, nil, "list", "]")
			} else {
				items := []expression{first}
				for p.take(",") && !p.is("]") {
					items = append(items, p.expr(0))
				}
				p.require("]")
				left = sequenceExpr{items, false}
			}
		}
	case "{":
		if p.take("}") {
			left = literal{dictionary{}}
		} else {
			k := p.expr(0)
			p.require(":")
			v := p.expr(0)
			if p.take("for") {
				left = p.comprehend(v, k, "dict", "}")
			} else {
				keys, vals := []expression{k}, []expression{v}
				for p.take(",") && !p.is("}") {
					keys = append(keys, p.expr(0))
					p.require(":")
					vals = append(vals, p.expr(0))
				}
				p.require("}")
				left = dictExpr{keys, vals}
			}
		}
	default:
		p.err = fault("SyntaxError")
		return literal{nil}
	}
	for p.err == nil {
		if p.take(".") {
			t := p.pop()
			if t.kind != "name" {
				p.err = fault("SyntaxError")
			}
			left = attribute{left, t.text}
			continue
		}
		if p.take("(") {
			args := []argument{}
			for !p.is(")") && p.err == nil {
				a := argument{}
				if p.take("**") {
					a.expand = 2
				} else if p.take("*") {
					a.expand = 1
				}
				if a.expand == 0 && p.peek().kind == "name" && p.at+1 < len(p.tokens) && p.tokens[p.at+1].kind == "=" {
					a.key = p.pop().text
					p.pop()
				}
				a.value = p.expr(0)
				args = append(args, a)
				if !p.take(",") {
					break
				}
			}
			p.require(")")
			left = callExpr{left, args}
			continue
		}
		if p.take("[") {
			sub := subscript{base: left}
			if !p.is(":") {
				sub.index = p.expr(0)
			}
			if p.take(":") {
				sub.slice = true
				if !p.is(":") && !p.is("]") {
					sub.stop = p.expr(0)
				}
				if p.take(":") {
					if !p.is("]") {
						sub.step = p.expr(0)
					}
				}
			}
			p.require("]")
			left = sub
			continue
		}
		if min <= 0 && p.take("if") {
			c := p.expr(1)
			p.require("else")
			left = conditionalExpr{left, c, p.expr(0)}
			continue
		}
		op := p.peek().text
		consume := 1
		if op == "not" && p.at+1 < len(p.tokens) && p.tokens[p.at+1].text == "in" {
			op = "not in"
			consume = 2
		}
		if op == "is" && p.at+1 < len(p.tokens) && p.tokens[p.at+1].text == "not" {
			op = "is not"
			consume = 2
		}
		prec := precedence(op)
		if prec < min || prec < 0 {
			break
		}
		p.at += consume
		rightMin := prec + 1
		if op == "**" {
			rightMin = 90
		}
		right := p.expr(rightMin)
		if prec == 30 {
			if c, ok := left.(comparison); ok {
				c.ops = append(c.ops, op)
				c.rest = append(c.rest, right)
				left = c
			} else {
				left = comparison{left, []string{op}, []expression{right}}
			}
		} else {
			left = binaryExpr{op, left, right}
		}
	}
	return left
}
func (p *parser) comprehend(item, key expression, kind, end string) expression {
	names := []string{}
	paren := p.take("(")
	for {
		t := p.pop()
		if t.kind != "name" {
			p.err = fault("SyntaxError")
		}
		names = append(names, t.text)
		if !p.take(",") {
			break
		}
	}
	if paren {
		p.require(")")
	}
	p.require("in")
	seq := p.expr(1)
	var cond expression
	if p.take("if") {
		cond = p.expr(1)
	}
	if p.is("for") {
		p.err = ErrUnsupported
	}
	p.require(end)
	return comprehension{item, key, names, seq, cond, kind}
}
