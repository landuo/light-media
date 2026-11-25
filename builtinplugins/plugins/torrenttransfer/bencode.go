package torrenttransfer

// The source uses fast-bencode1.1.8, not TorrentHelper's permissive torrentool
// parser. Keep its byte/string behavior and duplicate dictionary overwrite.
import (
	"bytes"
	"errors"
	"math/big"
	"sort"
	"strconv"
	"unicode/utf8"
)

var errBencode = errors.New("not a valid bencoded string")

func bdecode(raw []byte) (any, error) {
	if len(raw) > 16<<20 {
		return nil, errBencode
	}
	pos := 0
	var parse func(int) (any, error)
	parse = func(depth int) (any, error) {
		if depth > 200 || pos >= len(raw) {
			return nil, errBencode
		}
		switch raw[pos] {
		case 'i':
			pos++
			start := pos
			for pos < len(raw) && raw[pos] != 'e' {
				pos++
			}
			if pos == len(raw) {
				return nil, errBencode
			}
			s := string(raw[start:pos])
			pos++
			if s == "" || s == "-0" || (len(s) > 1 && s[0] == '0') || (len(s) > 2 && s[0] == '-' && s[1] == '0') {
				return nil, errBencode
			}
			for i, c := range s {
				if c < '0' || c > '9' {
					if i != 0 || (c != '-' && c != '+') {
						return nil, errBencode
					}
				}
			}
			n, ok := new(big.Int).SetString(s, 10)
			if !ok || !n.IsInt64() {
				return nil, errBencode
			}
			return n, nil
		case 'l':
			pos++
			v := []any{}
			for pos < len(raw) && raw[pos] != 'e' {
				x, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				v = append(v, x)
			}
			if pos >= len(raw) {
				return nil, errBencode
			}
			pos++
			return v, nil
		case 'd':
			pos++
			v := map[string]any{}
			for pos < len(raw) && raw[pos] != 'e' {
				k, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				var key string
				switch x := k.(type) {
				case string:
					key = x
				case []byte:
					key = string(x)
				default:
					return nil, errBencode
				}
				x, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				v[key] = x
			}
			if pos >= len(raw) {
				return nil, errBencode
			}
			pos++
			return v, nil
		default:
			start := pos
			for pos < len(raw) && raw[pos] >= '0' && raw[pos] <= '9' {
				pos++
			}
			if pos == start || pos >= len(raw) || raw[pos] != ':' {
				return nil, errBencode
			}
			s := string(raw[start:pos])
			if len(s) > 1 && s[0] == '0' {
				return nil, errBencode
			}
			n, e := strconv.Atoi(s)
			pos++
			if e != nil || n < 0 || n > len(raw)-pos {
				return nil, errBencode
			}
			v := append([]byte{}, raw[pos:pos+n]...)
			pos += n
			if utf8.Valid(v) {
				return string(v), nil
			}
			return v, nil
		}
	}
	v, e := parse(0)
	if e != nil {
		return nil, e
	}
	if pos != len(raw) {
		return nil, errors.New("invalid bencoded value (data after valid prefix)")
	}
	return v, nil
}
func bencode(value any) ([]byte, error) {
	var b bytes.Buffer
	var encode func(any) error
	str := func(raw []byte) { b.WriteString(strconv.Itoa(len(raw))); b.WriteByte(':'); b.Write(raw) }
	encode = func(v any) error {
		switch x := v.(type) {
		case string:
			str([]byte(x))
		case []byte:
			str(x)
		case *big.Int:
			b.WriteByte('i')
			b.WriteString(x.String())
			b.WriteByte('e')
		case []any:
			b.WriteByte('l')
			for _, a := range x {
				if e := encode(a); e != nil {
					return e
				}
			}
			b.WriteByte('e')
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteByte('d')
			for _, k := range keys {
				str([]byte(k))
				if e := encode(x[k]); e != nil {
					return e
				}
			}
			b.WriteByte('e')
		default:
			return errBencode
		}
		return nil
	}
	if e := encode(value); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}

func newByteInteger(v byte) *big.Int { return big.NewInt(int64(v)) }
