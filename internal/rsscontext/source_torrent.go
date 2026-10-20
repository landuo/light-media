package rsscontext

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"light-media/internal/releasemeta/legacyanime"
)

// FromDict is distinct from the dataclass constructor: it retains extra instance
// fields and ignores the two computed properties, matching the installed method.
func (t *TorrentInfo) FromDict(fields map[string]any) {
	for key, value := range fields {
		if key != "volume_factor" && key != "freedate_diff" {
			t.Set(key, value)
		}
	}
}

func sourceFactorFloat(value any) (float64, error) {
	var n *big.Int
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case *big.Int:
		n = v
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			f, err := strconv.ParseFloat(string(v), 64)
			if err == nil || math.IsInf(f, 0) {
				return f, nil
			}
			return 0, errors.New("TypeError")
		}
		var ok bool
		n, ok = new(big.Int).SetString(string(v), 10)
		if !ok {
			return 0, errors.New("TypeError")
		}
	default:
		return 0, errors.New("TypeError")
	}
	if n == nil {
		return 0, errors.New("TypeError")
	}
	f, _ := new(big.Float).SetInt(n).Float64()
	if math.IsInf(f, 0) {
		return 0, errors.New("OverflowError")
	}
	return f, nil
}

func TorrentFreeString(upload, download any) (string, error) {
	if upload == nil || download == nil {
		return "未知", nil
	}
	u, err := sourceFactorFloat(upload)
	if err != nil {
		return "", err
	}
	d, err := sourceFactorFloat(download)
	if err != nil {
		return "", err
	}
	key := strconv.FormatFloat(u, 'f', 2, 64) + " " + strconv.FormatFloat(d, 'f', 2, 64)
	if value, ok := map[string]string{
		"1.00 1.00": "普通", "1.00 0.00": "免费", "2.00 1.00": "2X", "4.00 1.00": "4X",
		"2.00 0.00": "2X免费", "4.00 0.00": "4X免费", "1.00 0.50": "50%", "2.00 0.50": "2X 50%",
		"1.00 0.70": "70%", "1.00 0.30": "30%", "1.00 0.75": "75%", "1.00 0.25": "25%",
	}[key]; ok {
		return value, nil
	}
	return "未知", nil
}

func (t *TorrentInfo) VolumeFactor() (string, error) {
	return TorrentFreeString(t.Get("uploadvolumefactor"), t.Get("downloadvolumefactor"))
}

// This is the installed Python3.12 TimeRE pattern for exactly
// '%Y-%m-%d %H:%M:%S'. Python \d is Unicode Nd and its whitespace also includes
// the four ASCII information separators. It accepts unpadded fields and a
// space-padded day, while invalid calendar values remain ValueError.
var torrentSourceDate = regexp.MustCompile(`\A(\p{Nd}{4})-(1[0-2]|0[1-9]|[1-9])-(3[0-1]|[1-2]\p{Nd}|0[1-9]|[1-9]| [1-9])[\t\n\v\f\r\x{1c}-\x{1f}\x{85}\p{Zs}\p{Zl}\p{Zp}]+(2[0-3]|[0-1]\p{Nd}|\p{Nd}):([0-5]\p{Nd}|\p{Nd}):(6[0-1]|[0-5]\p{Nd}|\p{Nd})\z`)

func sourceTorrentDate(value string) (time.Time, bool) {
	match := torrentSourceDate.FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, false
	}
	var values [6]int
	for i := range values {
		n, err := legacyanime.Integer(strings.TrimSpace(match[i+1]))
		if err != nil || !n.IsInt64() {
			return time.Time{}, false
		}
		values[i] = int(n.Int64())
	}
	y, m, d, h, minute, second := values[0], time.Month(values[1]), values[2], values[3], values[4], values[5]
	if y < 1 || y > 9999 || second > 59 {
		return time.Time{}, false
	}
	at := time.Date(y, m, d, h, minute, second, 0, time.UTC)
	return at, at.Year() == y && at.Month() == m && at.Day() == d && at.Hour() == h && at.Minute() == minute && at.Second() == second
}

func sourceNaiveNow(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1000*1000, time.UTC)
}

func sourceFloorDiv(n, d int64) int64 {
	q := n / d
	if n%d < 0 {
		q--
	}
	return q
}

func (t *TorrentInfo) FreeDateDiff(now time.Time) (any, error) {
	value := t.Get("freedate")
	if !truth(value) {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, errors.New("TypeError")
	}
	at, ok := sourceTorrentDate(text)
	if !ok {
		return text, nil
	}
	clock := sourceNaiveNow(now)
	seconds := at.Unix() - clock.Unix()
	if at.Nanosecond() < clock.Nanosecond() {
		seconds--
	}
	days := sourceFloorDiv(seconds, 86400)
	within := seconds - days*86400
	hours, minutes := within/3600, within%3600/60
	if days > 0 {
		return strconv.FormatInt(days, 10) + "天" + strconv.FormatInt(hours, 10) + "小时" + strconv.FormatInt(minutes, 10) + "分钟", nil
	}
	if hours > 0 {
		return strconv.FormatInt(hours, 10) + "小时" + strconv.FormatInt(minutes, 10) + "分钟", nil
	}
	if minutes > 0 {
		return strconv.FormatInt(minutes, 10) + "分钟", nil
	}
	return "", nil
}

// PubMinutes retains the original method's integer0 for absent/invalid input,
// and float floor-division result (including future negative values) otherwise.
func (t *TorrentInfo) PubMinutes(now time.Time) any {
	value, _ := t.PubMinutesSourceResult(now)
	return value
}

// PubMinutesSourceResult exposes the caught source exception separately from
// the source return value0. A host can report that diagnostic without putting
// untrusted date text or other private fields into stdout or a public error.
func (t *TorrentInfo) PubMinutesSourceResult(now time.Time) (any, error) {
	value := t.Get("pubdate")
	if !truth(value) {
		return 0, nil
	}
	text, ok := value.(string)
	if !ok {
		return 0, errors.New("TypeError")
	}
	at, ok := sourceTorrentDate(text)
	if !ok {
		return 0, errors.New("ValueError")
	}
	clock := sourceNaiveNow(now)
	micros := (clock.Unix()-at.Unix())*1_000_000 + int64(clock.Nanosecond()-at.Nanosecond())/1000
	seconds, _ := new(big.Rat).SetFrac(big.NewInt(micros), big.NewInt(1_000_000)).Float64()
	mod := math.Mod(seconds, 60)
	div := (seconds - mod) / 60
	if mod != 0 && mod < 0 {
		div--
	}
	if div == 0 {
		return math.Copysign(0, seconds), nil
	}
	floor := math.Floor(div)
	if div-floor > .5 {
		floor++
	}
	return floor, nil
}

func (t *TorrentInfo) ToDictAt(now time.Time) (map[string]any, error) {
	if t == nil {
		return nil, nil
	}
	out := make(map[string]any, len(t.Fields)+2)
	for key, value := range t.Fields {
		out[key] = value
	}
	factor, err := t.VolumeFactor()
	if err != nil {
		return nil, err
	}
	out["volume_factor"] = factor
	diff, err := t.FreeDateDiff(now)
	if err != nil {
		return nil, err
	}
	out["freedate_diff"] = diff
	return out, nil
}
