package notice

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"light-media/internal/releasemeta/legacyanime"
)

// Period uses arbitrary integers: source does not validate clock ranges and
// Python int accepts Unicode15 decimal digits, underscores and large values.
type Period [4]*big.Int

func ParseSchedule(value any) ([]Period, error) {
	v, err := jsonCopy(value)
	if err != nil {
		return nil, err
	}
	out := []Period{}
	if !truth(v) {
		return out, nil
	}
	list, ok := v.([]any)
	if !ok {
		list = []any{v}
	}
	for _, item := range list {
		if !truth(item) {
			continue
		}
		period, ok := item.(map[string]any)
		if !ok {
			return nil, SourceError{"AttributeError"}
		}
		if !truth(period["start"]) || !truth(period["end"]) {
			continue
		}
		start, sok := period["start"].(string)
		end, eok := period["end"].(string)
		if !sok || !eok {
			continue
		}
		sp, ep := strings.Split(start, ":"), strings.Split(end, ":")
		if len(sp) < 2 || len(ep) < 2 {
			continue
		}
		parts := []string{sp[0], sp[1], ep[0], ep[1]}
		var p Period
		valid := true
		for i, s := range parts {
			n, e := legacyanime.Integer(s)
			if e != nil {
				valid = false
				break
			}
			p[i] = n
		}
		if valid {
			out = append(out, p)
		}
	}
	return out, nil
}

type SourceError struct{ Class string }

func (e SourceError) Error() string { return "original exception class: " + e.Class }
func TimeToMinutes(value string) (*big.Int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return nil, SourceError{"ValueError"}
	}
	h, err := legacyanime.Integer(parts[0])
	if err != nil {
		return nil, SourceError{"ValueError"}
	}
	m, err := legacyanime.Integer(parts[1])
	if err != nil {
		return nil, SourceError{"ValueError"}
	}
	return minutes(h, m), nil
}
func minutes(h, m *big.Int) *big.Int { return new(big.Int).Add(new(big.Int).Mul(h, big.NewInt(60)), m) }
func Allowed(periods []Period, at time.Time) bool {
	if len(periods) == 0 {
		return true
	}
	now := big.NewInt(int64(at.Hour()*60 + at.Minute()))
	for _, p := range periods {
		start, end := minutes(p[0], p[1]), minutes(p[2], p[3])
		if start.Cmp(end) <= 0 {
			if start.Cmp(now) <= 0 && now.Cmp(end) <= 0 {
				return true
			}
		} else if now.Cmp(start) >= 0 || now.Cmp(end) <= 0 {
			return true
		}
	}
	return false
}
func copyPeriods(in []Period) ([]Period, error) {
	out := make([]Period, len(in))
	for i, p := range in {
		for j, n := range p {
			if n == nil {
				return nil, fmt.Errorf("period %d: missing integer", i)
			}
			out[i][j] = new(big.Int).Set(n)
		}
	}
	return out, nil
}
