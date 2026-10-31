package plugins

import (
	"errors"
	"math"
	"math/big"
	"strconv"
)

// OCRValue preserves Python JSON values without coercing the source's result
// into its str annotation. Code points preserve lone surrogates; decimal text
// preserves arbitrary integers and IEEE non-finite values across JSON RPC.
type OCRValue struct {
	Kind    string     `json:"kind"`
	Bool    bool       `json:"bool,omitempty"`
	Number  string     `json:"number,omitempty"`
	Points  []rune     `json:"points,omitempty"`
	Items   []OCRValue `json:"items,omitempty"`
	Entries []OCREntry `json:"entries,omitempty"`
}
type OCREntry struct {
	Key   []rune   `json:"key"`
	Value OCRValue `json:"value"`
}

func ocrValidPoints(points []rune) bool {
	for _, point := range points {
		if point < 0 || point > 0x10ffff {
			return false
		}
	}
	return true
}
func (v OCRValue) Validate() error {
	invalid := func() error { return errors.New("invalid OCR value envelope") }
	if !ocrValidPoints(v.Points) {
		return invalid()
	}
	switch v.Kind {
	case "null":
		if v.Bool || v.Number != "" || len(v.Points) != 0 || len(v.Items) != 0 || len(v.Entries) != 0 {
			return invalid()
		}
	case "bool":
		if v.Number != "" || len(v.Points) != 0 || len(v.Items) != 0 || len(v.Entries) != 0 {
			return invalid()
		}
	case "int":
		n, ok := new(big.Int).SetString(v.Number, 10)
		if !ok || n.String() != v.Number || v.Bool || len(v.Points) != 0 || len(v.Items) != 0 || len(v.Entries) != 0 {
			return invalid()
		}
	case "float":
		switch v.Number {
		case "nan", "+inf", "-inf":
		default:
			n, err := strconv.ParseFloat(v.Number, 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return invalid()
			}
		}
		if v.Bool || len(v.Points) != 0 || len(v.Items) != 0 || len(v.Entries) != 0 {
			return invalid()
		}
	case "string":
		if v.Bool || v.Number != "" || len(v.Items) != 0 || len(v.Entries) != 0 {
			return invalid()
		}
	case "list":
		if v.Bool || v.Number != "" || len(v.Points) != 0 || len(v.Entries) != 0 {
			return invalid()
		}
		for _, item := range v.Items {
			if item.Validate() != nil {
				return invalid()
			}
		}
	case "object":
		if v.Bool || v.Number != "" || len(v.Points) != 0 || len(v.Items) != 0 {
			return invalid()
		}
		seen := make(map[string]bool, len(v.Entries))
		for _, entry := range v.Entries {
			if !ocrValidPoints(entry.Key) || entry.Value.Validate() != nil {
				return invalid()
			}
			// UTF-8 conversion would collapse distinct unpaired surrogate keys.
			key := ""
			for _, point := range entry.Key {
				key += strconv.FormatInt(int64(point), 16) + ","
			}
			if seen[key] {
				return invalid()
			}
			seen[key] = true
		}
	default:
		return invalid()
	}
	return nil
}
func (v OCRValue) Truthy() bool {
	switch v.Kind {
	case "null":
		return false
	case "bool":
		return v.Bool
	case "int":
		n, ok := new(big.Int).SetString(v.Number, 10)
		return ok && n.Sign() != 0
	case "float":
		if v.Number == "nan" || v.Number == "+inf" || v.Number == "-inf" {
			return true
		}
		n, err := strconv.ParseFloat(v.Number, 64)
		return err == nil && n != 0
	case "string":
		return len(v.Points) != 0
	case "list":
		return len(v.Items) != 0
	case "object":
		return len(v.Entries) != 0
	}
	return false
}
