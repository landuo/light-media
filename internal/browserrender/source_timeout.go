package browserrender

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

func sourceBrowserTimeout(raw json.RawMessage) (any, error) {
	// These bytes are created only by the source-only validated float enum;
	// the standard worker IPC envelope remains strict JSON.
	switch string(bytes.TrimSpace(raw)) {
	case "NaN":
		return math.NaN(), nil
	case "Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	}

	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, &Error{Code: "browser_source_timeout_invalid"}
	}
	return value, nil
}
func sourceTimeoutTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			n, _ := strconv.ParseFloat(v.String(), 64)
			return n != 0
		}
		n, ok := new(big.Rat).SetString(v.String())
		return !ok || n.Sign() != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}
func sourceEffectiveFSTimeout(raw json.RawMessage) (any, error) {
	value, err := sourceBrowserTimeout(raw)
	if err != nil {
		return nil, err
	}
	if !sourceTimeoutTruthy(value) {
		return json.Number("60"), nil
	}
	return value, nil
}
func sourceFSHTTPTimeout(raw json.RawMessage) (time.Duration, error) {
	value, err := sourceEffectiveFSTimeout(raw)
	if err != nil {
		return 0, err
	}
	// urllib3 explicitly rejects bool, even though Python bool subclasses int.
	number, ok := value.(json.Number)
	if !ok {
		return 0, &Error{Code: "browser_source_http_timeout_invalid"}
	}
	seconds, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds <= 0 || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return 0, &Error{Code: "browser_source_http_timeout_invalid"}
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
func sourceFSMaxTimeout(raw json.RawMessage) (json.Number, error) {
	value, err := sourceEffectiveFSTimeout(raw)
	if err != nil {
		return "", err
	}
	var integer *big.Int
	switch v := value.(type) {
	case bool:
		integer = big.NewInt(0)
		if v {
			integer.SetInt64(1)
		}
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			f, _ := strconv.ParseFloat(v.String(), 64)
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return "", &Error{Code: "browser_source_timeout_invalid"}
			}
			integer, _ = new(big.Float).SetFloat64(f).Int(nil)
			break
		}
		number, ok := new(big.Rat).SetString(v.String())
		if !ok {
			return "", &Error{Code: "browser_source_timeout_invalid"}
		}
		integer = new(big.Int).Quo(number.Num(), number.Denom())
	case string:
		// A truthy string can pass int() but cannot pass RequestUtils' HTTP
		// timeout validation. Retain the distinction from a numeric JSON value.
		var ok bool
		integer, ok = new(big.Int).SetString(strings.TrimSpace(v), 10)
		if !ok {
			return "", &Error{Code: "browser_source_timeout_invalid"}
		}
	default:
		return "", &Error{Code: "browser_source_timeout_invalid"}
	}
	return json.Number(integer.Mul(integer, big.NewInt(1000)).String()), nil
}

// Called after the ordinary goto, at the original timeout*1000 boundary.
// Playwright's actual Waiter starts its timeout division in a background task.
// str/list products do not become a successful numeric deadline: that task
// fails without rejecting the separate load-state future, which can still be
// fulfilled by networkidle. No timer is used for those source values.
func sourceCloakTimeout(raw json.RawMessage) (time.Duration, error) {
	value, err := sourceBrowserTimeout(raw)
	if err != nil {
		return 0, err
	}
	switch v := value.(type) {
	case nil, map[string]any:
		return 0, &Error{Code: "browser_source_timeout_multiply_failed"}
	case string, []any:
		return 0, nil
	case bool:
		if v {
			return time.Second, nil
		}
		return 0, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, -1) {
			return -1, nil
		}
		if math.IsInf(v, 1) {
			return 0, nil
		}
		return time.Duration(v * float64(time.Second)), nil
	case json.Number:
		seconds, _ := strconv.ParseFloat(v.String(), 64)
		// For enormous Python ints, the background int true-division raises
		// OverflowError before it can reject the separate load-state future.
		if math.IsInf(seconds, 0) && !strings.ContainsAny(v.String(), ".eE") {
			return 0, nil
		}
		if math.IsNaN(seconds) {
			return 0, nil
		}
		// These waits exceed any representable/caller lifetime. Positive
		// values have no observable deadline; negative floats reject at once.
		if seconds > float64(math.MaxInt64)/float64(time.Second) {
			return 0, nil
		}
		if seconds < -float64(math.MaxInt64)/float64(time.Second) {
			return -1, nil
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}
	return 0, &Error{Code: "browser_source_timeout_multiply_failed"}
}

// Fixed FS payloads have known insertion order. Requests uses ensure_ascii
// and its default comma/colon spacing; ordinary native calls keep their codec.
func sourceFSJSON(payload map[string]any) ([]byte, error) {
	var out bytes.Buffer
	var write func(any) error
	write = func(value any) error {
		switch v := value.(type) {
		case map[string]string:
			m := map[string]any{}
			for k, x := range v {
				m[k] = x
			}
			return write(m)
		case map[string]any:
			out.WriteByte('{')
			first := true
			seen := 0
			for _, key := range []string{"cmd", "url", "session", "maxTimeout", "proxy", "cookies", "username", "password", "name", "value"} {
				x, ok := v[key]
				if !ok {
					continue
				}
				seen++
				if !first {
					out.WriteString(", ")
				}
				first = false
				if err := write(key); err != nil {
					return err
				}
				out.WriteString(": ")
				if err := write(x); err != nil {
					return err
				}
			}
			if seen != len(v) {
				return &Error{Code: "browser_source_fs_payload_invalid"}
			}
			out.WriteByte('}')
			return nil
		case []map[string]string:
			out.WriteByte('[')
			for i, x := range v {
				if i > 0 {
					out.WriteString(", ")
				}
				if err := write(x); err != nil {
					return err
				}
			}
			out.WriteByte(']')
			return nil
		case string:
			var encoded bytes.Buffer
			e := json.NewEncoder(&encoded)
			e.SetEscapeHTML(false)
			if err := e.Encode(v); err != nil {
				return err
			}
			s := strings.TrimSuffix(encoded.String(), "\n")
			const hex = "0123456789abcdef"
			unit := func(r rune) {
				out.WriteString(`\u`)
				for _, shift := range []uint{12, 8, 4, 0} {
					out.WriteByte(hex[(r>>shift)&15])
				}
			}
			for _, r := range s {
				if r < 127 {
					out.WriteRune(r)
				} else if r <= 0xffff {
					unit(r)
				} else {
					r -= 0x10000
					unit(0xd800 + r/1024)
					unit(0xdc00 + r%1024)
				}
			}
			return nil
		default:
			b, err := json.Marshal(value)
			if err != nil {
				return err
			}
			out.Write(b)
			return nil
		}
	}
	if err := write(payload); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
