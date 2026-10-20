package exec

import (
	"math"
	"strings"

	"light-media/internal/jinja/tokens"
)

func sourceNumber(v *Value) bool { return v.IsNumber() || v.IsBool() }
func sourceFloat(v *Value) float64 {
	if v.IsBool() {
		if v.Bool() {
			return 1
		}
		return 0
	}
	return v.Float()
}
func sourceInteger(v *Value) int {
	if v.IsBool() {
		if v.Bool() {
			return 1
		}
		return 0
	}
	return v.Integer()
}
func sourceArithmetic(left, right *Value, op tokens.Type) (*Value, bool) {
	if right == nil {
		return nil, false
	}
	bad := func() (*Value, bool) { return ValueError(SourceFailuref("TypeError: unsupported operand types")), true }
	numbers := sourceNumber(left) && sourceNumber(right)
	switch op {
	case tokens.Addition:
		if left.IsString() && right.IsString() {
			return AsValue(left.String() + right.String()), true
		}
		if left.IsList() && right.IsList() {
			return nil, false
		}
		if !numbers {
			return bad()
		}
		if left.IsFloat() || right.IsFloat() {
			return AsValue(sourceFloat(left) + sourceFloat(right)), true
		}
		return AsValue(sourceInteger(left) + sourceInteger(right)), true
	case tokens.Subtraction:
		if !numbers {
			return bad()
		}
		if left.IsFloat() || right.IsFloat() {
			return AsValue(sourceFloat(left) - sourceFloat(right)), true
		}
		return AsValue(sourceInteger(left) - sourceInteger(right)), true
	case tokens.Multiply:
		if left.IsString() && (right.IsInteger() || right.IsBool()) {
			n := sourceInteger(right)
			if n < 0 {
				n = 0
			}
			return AsValue(strings.Repeat(left.String(), n)), true
		}
		if right.IsString() && (left.IsInteger() || left.IsBool()) {
			n := sourceInteger(left)
			if n < 0 {
				n = 0
			}
			return AsValue(strings.Repeat(right.String(), n)), true
		}
		if !numbers {
			return bad()
		}
		if left.IsFloat() || right.IsFloat() {
			return AsValue(sourceFloat(left) * sourceFloat(right)), true
		}
		return AsValue(sourceInteger(left) * sourceInteger(right)), true
	case tokens.Division, tokens.FloorDivision, tokens.Modulo:
		if !numbers {
			return bad()
		}
		a, b := sourceFloat(left), sourceFloat(right)
		if b == 0 {
			return ValueError(SourceFailuref("ZeroDivisionError")), true
		}
		if op == tokens.Division {
			return AsValue(a / b), true
		}
		result := math.Floor(a / b)
		if op == tokens.Modulo {
			result = math.Mod(a, b)
			if result != 0 && math.Signbit(result) != math.Signbit(b) {
				result += b
			}
			if result == 0 {
				result = math.Copysign(0, b)
			}
		}
		if left.IsFloat() || right.IsFloat() {
			return AsValue(result), true
		}
		return AsValue(int(result)), true
	case tokens.Power:
		if !numbers {
			return bad()
		}
		return AsValue(math.Pow(sourceFloat(left), sourceFloat(right))), true
	}
	return nil, false
}
