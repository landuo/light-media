package cleaninvalidseed

import (
	py "light-media/internal/releasemeta/legacyanime"
	"math"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// URLNetloc preserves the source helper's unusual non-http behavior: udp://...
// remains the complete input as the "netloc", and matching is case-sensitive.
func URLNetloc(v any) ([]string, error) {
	if !Truth(v) {
		return []string{"", ""}, nil
	}
	s, e := stringValue(v, "startswith")
	if e != nil {
		return nil, e
	}
	if !strings.HasPrefix(s, "http") {
		return []string{"http", s}, nil
	}
	s = strings.TrimLeftFunc(s, func(r rune) bool { return r <= 32 })
	s = strings.NewReplacer("\r", "", "\n", "", "\t", "").Replace(s)
	scheme, rest := "", s
	if i := strings.IndexByte(s, ':'); i > 0 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		valid := true
		for _, r := range s[:i] {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.') {
				valid = false
				break
			}
		}
		if valid {
			scheme = strings.ToLower(s[:i])
			rest = s[i+1:]
		}
	}
	netloc := ""
	if strings.HasPrefix(rest, "//") {
		netloc = rest[2:]
		if i := strings.IndexAny(netloc, "/?#"); i >= 0 {
			netloc = netloc[:i]
		}
		open, close := strings.Contains(netloc, "["), strings.Contains(netloc, "]")
		if open != close {
			return nil, source("ValueError", "Invalid IPv6 URL")
		}
		if open {
			hostpart := netloc
			if i := strings.LastIndex(hostpart, "@"); i >= 0 {
				hostpart = hostpart[i+1:]
			}
			host := ""
			if i := strings.IndexByte(hostpart, '['); i >= 0 {
				if i != 0 {
					return nil, source("ValueError", "Invalid IPv6 URL")
				}
				bracket := hostpart[i+1:]
				host, rest, _ = strings.Cut(bracket, "]")
				if rest != "" && !strings.HasPrefix(rest, ":") {
					return nil, source("ValueError", "Invalid IPv6 URL")
				}
			} else {
				host, _, _ = strings.Cut(hostpart, ":")
			}
			if strings.HasPrefix(host, "v") {
				version, addr, ok := strings.Cut(host[1:], ".")
				valid := ok && version != "" && addr != ""
				for _, r := range version {
					if !((r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || (r >= '0' && r <= '9')) {
						valid = false
					}
				}
				if !valid {
					return nil, source("ValueError", "IPvFuture address is invalid")
				}
			} else {
				ip, e := netip.ParseAddr(host)
				if e != nil || !ip.Is6() || strings.Count(host, "%") > 1 {
					return nil, source("ValueError", "Invalid IPv6 URL")
				}
			}
		}
	}
	for _, r := range netloc {
		if nfkcURLReserved[r] {
			return nil, source("ValueError", "NFKC netloc")
		}
	}
	return []string{scheme, netloc}, nil
}

// Generated from actual installed Python3.12/Unicode15 NFKC closure; these are
// all individual scalars introducing /?#@: (reference/urlparse-source.json).
var nfkcURLReserved = map[rune]bool{8263: true, 8264: true, 8265: true, 8448: true, 8449: true, 8453: true, 8454: true, 10868: true, 65043: true, 65046: true, 65109: true, 65110: true, 65119: true, 65131: true, 65283: true, 65295: true, 65306: true, 65311: true, 65312: true}

func pyFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "inf"
	}
	if math.IsInf(v, -1) {
		return "-inf"
	}
	if math.IsNaN(v) {
		return "nan"
	}
	e := strconv.FormatFloat(v, 'e', -1, 64)
	parts := strings.Split(e, "e")
	exp, _ := strconv.Atoi(parts[1])
	if exp >= -4 && exp < 16 {
		v := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(v, ".") {
			v += ".0"
		}
		return v
	}
	sign := "+"
	if exp < 0 {
		sign = "-"
		exp = -exp
	}
	s := strconv.Itoa(exp)
	if len(s) < 2 {
		s = "0" + s
	}
	return parts[0] + "e" + sign + s
}
func FileSize(v any) string {
	if v == nil {
		return ""
	}
	s := str(v)
	rs := []rune(s)
	out := []rune{}
	removed := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		// The installed helper passes re.I as the fourth positional argument
		// of re.sub: it means count=2, NOT IGNORECASE. Preserve that bug.
		if removed < 2 {
			if py.Strip(string(r)) == "" || r == 'B' {
				removed++
				continue
			}
			if r == 'i' && i+1 < len(rs) && rs[i+1] == 'B' {
				i++
				removed++
				continue
			}
		}
		out = append(out, r)
	}
	s = string(out)
	digits := strings.ReplaceAll(s, ".", "")
	isDigits := py.IsDigit(digits)
	if isDigits {
		var numeric strings.Builder
		for _, r := range s {
			if r == '.' {
				numeric.WriteRune(r)
				continue
			}
			n, e := py.Integer(string(r))
			if e != nil {
				return ""
			}
			numeric.WriteString(n.String())
		}
		f, e := strconv.ParseFloat(numeric.String(), 64)
		if e != nil && !math.IsInf(f, 0) {
			return ""
		}
		bounds := []float64{1023, 1048575, 1073741823, 1099511627775}
		idx := sort.SearchFloat64s(bounds, f) - 1
		if idx < 0 {
			return pyFloat(f) + "B"
		}
		b := bounds[idx]
		unit := []string{"K", "M", "G", "T"}[idx]
		text := strconv.FormatFloat(f/(b+1), 'f', 2, 64)
		r, _ := strconv.ParseFloat(text, 64)
		return pyFloat(r) + unit
	}
	if strings.ContainsAny(strings.ToUpper(s), "KMGTPK") {
		return s
	}
	return s + "B"
}
