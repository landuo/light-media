package priorityrules

import "strings"

type expression struct {
	kind        string
	atom        string
	left, right *expression
}
type parser struct {
	text             string
	at, nodes, depth int
}

func parseExpression(text string) (*expression, error) {
	p := parser{text: text}
	node, err := p.or()
	if err != nil {
		return nil, err
	}
	return node, nil
}
func (p *parser) skip() {
	for p.at < len(p.text) && strings.ContainsRune(" \t\r\n\f", rune(p.text[p.at])) {
		p.at++
	}
}
func (p *parser) node(kind, name string, left, right *expression) (*expression, error) {
	p.nodes++
	if p.nodes > 4096 {
		return nil, sourceError("RuleLimit")
	}
	return &expression{kind, name, left, right}, nil
}
func (p *parser) or() (*expression, error)  { return p.binary('|', "or", p.and) }
func (p *parser) and() (*expression, error) { return p.binary('&', "and", p.unary) }
func (p *parser) binary(symbol byte, kind string, next func() (*expression, error)) (*expression, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for {
		p.skip()
		if p.at >= len(p.text) || p.text[p.at] != symbol {
			return left, nil
		}
		saved := p.at
		p.at++
		right, err := next()
		if err != nil {
			if err == sourceError("RuleLimit") {
				return nil, err
			}
			p.at = saved
			return left, nil
		}
		left, err = p.node(kind, "", left, right)
		if err != nil {
			return nil, err
		}
	}
}
func (p *parser) unary() (*expression, error) {
	p.skip()
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 128 {
		return nil, sourceError("RuleLimit")
	}
	if p.at >= len(p.text) {
		return nil, sourceError("ParseException")
	}
	if p.text[p.at] == '!' {
		p.at++
		child, err := p.unary()
		if err != nil {
			return nil, err
		}
		return p.node("not", "", child, nil)
	}
	if p.text[p.at] == '(' {
		p.at++
		child, err := p.or()
		if err != nil {
			return nil, err
		}
		p.skip()
		if p.at >= len(p.text) || p.text[p.at] != ')' {
			return nil, sourceError("ParseException")
		}
		p.at++
		return child, nil
	}
	start := p.at
	for p.at < len(p.text) && p.text[p.at] >= '0' && p.text[p.at] <= '9' {
		p.at++
	}
	if p.at >= len(p.text) || !asciiLetter(p.text[p.at]) {
		return nil, sourceError("ParseException")
	}
	p.at++
	for p.at < len(p.text) && (asciiLetter(p.text[p.at]) || p.text[p.at] >= '0' && p.text[p.at] <= '9') {
		p.at++
	}
	return p.node("atom", p.text[start:p.at], nil, nil)
}
func asciiLetter(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
