package words

import (
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var implicitEP = regexp.MustCompile(`(?:\d|\))\s*EP|EP\s*(?:\d|\()`)
var epToken = regexp.MustCompile(`EP`)

func wordByte(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_'
}

// Arithmetic keeps integer operations exact. A true division switches to
// float64, matching Python's float propagation, floor and modulo signs.
type number struct {
	i *big.Int
	f float64
}

func integer(n int64) number { return number{i: big.NewInt(n)} }
func (n number) float() float64 {
	if n.i == nil {
		return n.f
	}
	f, _ := new(big.Float).SetInt(n.i).Float64()
	return f
}

type arithmetic struct {
	s          string
	pos, depth int
}

func (p *arithmetic) space() {
	for p.pos < len(p.s) && strings.ContainsRune(" \t\r\n", rune(p.s[p.pos])) {
		p.pos++
	}
}
func (p *arithmetic) expr(min int) (number, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 64 {
		return number{}, errors.New("expression too deep")
	}
	p.space()
	var left number
	var err error
	if p.pos >= len(p.s) {
		return left, errors.New("missing operand")
	}
	ch := p.s[p.pos]
	if ch == '+' || ch == '-' {
		p.pos++
		left, err = p.expr(3)
		if err == nil && ch == '-' {
			if left.i != nil {
				left.i.Neg(left.i)
			} else {
				left.f = -left.f
			}
		}
	} else if ch == '(' {
		p.pos++
		left, err = p.expr(0)
		p.space()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return left, errors.New("unclosed expression")
		}
		p.pos++
	} else {
		start := p.pos
		for p.pos < len(p.s) && wordByte(p.s[p.pos]) {
			p.pos++
		}
		text := p.s[start:p.pos]
		if text == "True" {
			left = integer(1)
		} else if text == "False" {
			left = integer(0)
		} else {
			digits := strings.ReplaceAll(text, "_", "")
			if len(digits) > 1 && digits[0] == '0' && digits[1] >= '0' && digits[1] <= '9' && strings.Trim(digits, "0") != "" {
				return left, errors.New("invalid decimal leading zero")
			}
			value, ok := new(big.Int).SetString(text, 0)
			if !ok {
				return left, errors.New("invalid integer operand")
			}
			left = number{i: value}
		}
	}
	if err != nil {
		return left, err
	}
	for {
		p.space()
		if p.pos >= len(p.s) {
			break
		}
		op := string(p.s[p.pos])
		priority := 1
		if op == "*" || op == "/" || op == "%" {
			priority = 2
		} else if op != "+" && op != "-" {
			break
		}
		if priority < min {
			break
		}
		p.pos++
		if op == "/" && p.pos < len(p.s) && p.s[p.pos] == '/' {
			op = "//"
			p.pos++
		}
		right, err := p.expr(priority + 1)
		if err != nil {
			return left, err
		}
		left, err = operate(left, right, op)
		if err != nil {
			return left, err
		}
		if left.i != nil && left.i.BitLen() > 4096 {
			return left, errors.New("arithmetic too large")
		}
	}
	return left, nil
}
func operate(a, b number, op string) (number, error) {
	if a.i != nil && b.i != nil && op != "/" {
		out := new(big.Int)
		switch op {
		case "+":
			out.Add(a.i, b.i)
		case "-":
			out.Sub(a.i, b.i)
		case "*":
			out.Mul(a.i, b.i)
		case "//", "%":
			if b.i.Sign() == 0 {
				return number{}, errors.New("division by zero")
			}
			q, r := new(big.Int), new(big.Int)
			q.QuoRem(a.i, b.i, r)
			if r.Sign() != 0 && a.i.Sign() != b.i.Sign() {
				q.Sub(q, big.NewInt(1))
				r.Add(r, b.i)
			}
			if op == "//" {
				out = q
			} else {
				out = r
			}
		}
		return number{i: out}, nil
	}
	x, y := a.float(), b.float()
	if y == 0 && (op == "/" || op == "//" || op == "%") {
		return number{}, errors.New("division by zero")
	}
	var z float64
	switch op {
	case "+":
		z = x + y
	case "-":
		z = x - y
	case "*":
		z = x * y
	case "/":
		z = x / y
	case "//":
		z = math.Floor(x / y)
	case "%":
		z = x - math.Floor(x/y)*y
	}
	if math.IsInf(z, 0) || math.IsNaN(z) {
		return number{}, errors.New("nonfinite arithmetic")
	}
	return number{f: z}, nil
}
func calculate(expression string, episode int64) (int64, error) {
	if len(expression) > 4096 || implicitEP.MatchString(expression) {
		return 0, errors.New("invalid episode expression")
	}
	locations := epToken.FindAllStringIndex(expression, -1)
	var b strings.Builder
	last, count := 0, 0
	for _, location := range locations {
		i, j := location[0], location[1]
		if i > 0 && wordByte(expression[i-1]) || j < len(expression) && wordByte(expression[j]) {
			continue
		}
		b.WriteString(expression[last:i])
		b.WriteString(strconv.FormatInt(episode, 10))
		last = j
		count++
	}
	b.WriteString(expression[last:])
	if strings.Contains(expression, "EP") && count == 0 {
		return 0, errors.New("invalid EP token")
	}
	text, err := pythonArithmeticText(b.String())
	if err != nil {
		return 0, err
	}
	p := arithmetic{s: text}
	value, err := p.expr(0)
	p.space()
	if err != nil || p.pos != len(p.s) {
		return 0, errors.New("invalid episode arithmetic")
	}
	if value.i != nil {
		if !value.i.IsInt64() {
			return 0, errors.New("episode out of range")
		}
		return value.i.Int64(), nil
	}
	if value.f >= math.MaxInt64 || value.f < math.MinInt64 {
		return 0, errors.New("episode out of range")
	}
	return int64(value.f), nil
}

// Retain the eval-mode lexical rules that affect the permitted arithmetic AST:
// leading indentation, comments, parentheses and explicit line continuation.
func pythonArithmeticText(text string) (string, error) {
	leading := strings.TrimLeft(text, "\r\n")
	if len(leading) > 0 && (leading[0] == ' ' || leading[0] == '\t') {
		return "", errors.New("unexpected expression indentation")
	}
	var out strings.Builder
	depth := 0
	ended := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch == '\\' && i+1 < len(text) && text[i+1] == '\n' {
			i++
			continue
		}
		if ch == '#' {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			if i == len(text) {
				break
			}
			ch = '\n'
		}
		if ch == '\r' || ch == '\n' {
			if depth == 0 && strings.TrimSpace(out.String()) != "" {
				ended = true
			}
			out.WriteByte(' ')
			continue
		}
		if ended && ch != ' ' && ch != '\t' {
			return "", errors.New("multiple expression lines")
		}
		if ch == '(' {
			depth++
		}
		if ch == ')' {
			depth--
		}
		out.WriteByte(ch)
	}
	return strings.TrimSpace(out.String()), nil
}

func chineseNumber(value string) (int64, error) {
	var b strings.Builder
	for _, r := range value {
		if at := strings.IndexRune("一二三四五六七八九", r); at >= 0 {
			b.WriteByte('1' + byte(at/3))
		} else {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if !strings.Contains(s, "十") {
		return strconv.ParseInt(s, 10, 64)
	}
	parts := strings.Split(s, "十")
	if len(parts) != 2 || len(parts[0]) > 1 || len(parts[1]) > 1 {
		return 0, errors.New("invalid Chinese number")
	}
	tens, ones := int64(1), int64(0)
	var err error
	if parts[0] != "" {
		tens, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, err
		}
	}
	if parts[1] != "" {
		ones, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, err
		}
	}
	return tens*10 + ones, nil
}

func formatNumber(original string, value int64) (string, error) {
	if strings.IndexFunc(original, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		raw := strconv.FormatInt(value, 10)
		negative := strings.HasPrefix(raw, "-")
		digits := strings.TrimPrefix(raw, "-")
		if strings.HasPrefix(original, "0") && len(digits) < len(original) {
			digits = strings.Repeat("0", len(original)-len(digits)) + digits
		}
		if negative {
			digits = "-" + digits
		}
		return digits, nil
	}
	if value > 9999999999999999 || value < -9999999999999999 {
		return "", errors.New("Chinese number out of range")
	}
	if value < 0 {
		positive, err := formatNumber(original, -value)
		return "负" + positive, err
	}
	if value == 0 {
		return "零", nil
	}
	digits := []rune("零一二三四五六七八九")
	units := []string{"", "十", "百", "千"}
	bigUnits := []string{"", "万", "亿", "万亿"}
	groups := []int64{}
	for value > 0 {
		groups = append(groups, value%10000)
		value /= 10000
	}
	var out strings.Builder
	zero := false
	for g := len(groups) - 1; g >= 0; g-- {
		n := groups[g]
		if n == 0 {
			if out.Len() > 0 {
				zero = true
			}
			continue
		}
		if out.Len() > 0 && (zero || n < 1000) {
			out.WriteRune('零')
		}
		zero = false
		groupWritten := false
		for pos, div := 3, int64(1000); pos >= 0; pos, div = pos-1, div/10 {
			d := n / div % 10
			if d == 0 {
				if groupWritten && n%div != 0 {
					zero = true
				}
				continue
			}
			if zero {
				out.WriteRune('零')
				zero = false
			}
			if !(d == 1 && pos == 1 && out.Len() == 0) {
				out.WriteRune(digits[d])
			}
			out.WriteString(units[pos])
			groupWritten = true
		}
		out.WriteString(bigUnits[g])
	}
	return out.String(), nil
}
