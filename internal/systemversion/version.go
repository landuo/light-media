// Package systemversion implements the installed SystemChain version methods.
package systemversion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"light-media/internal/plugins/mergemessagenotify/apprise"
)

const BackendURL = "https://api.github.com/repos/landuo/MoviePilot/releases"
const FrontendURL = "https://api.github.com/repos/jxxghp/MoviePilot-Frontend/releases"

var ErrDependency = errors.New("source version input requires an exact native representation")

type Release struct {
	Version *string
	Status  int
	Caught  bool
	BodySHA string
}

// SelectRelease only consumes the fields read by the source algorithm. None is
// its normal result for HTTP failures, invalid release shapes, or caught errors.
func SelectRelease(raw []byte) (Release, error) {
	if !utf8.Valid(raw) || unpairedSurrogate(raw) {
		return Release{}, ErrDependency
	}
	raw = sourceNumbers(raw)
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&value); e != nil {
		return Release{Caught: true}, nil
	}
	var trailing any
	if e := d.Decode(&trailing); e != io.EOF {
		return Release{Caught: true}, nil
	}
	rows, ok := value.([]any)
	if !ok {
		return Release{Caught: true}, nil
	}
	type tag struct {
		value  string
		digits []*big.Int
	}
	tags := []tag{}
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			return Release{Caught: true}, nil
		}
		name, ok := row["tag_name"].(string)
		if !ok {
			return Release{Caught: true}, nil
		}
		if !strings.HasPrefix(name, "v2.") {
			continue
		}
		keys := []*big.Int{}
		digits := ""
		flush := func() bool {
			if digits == "" {
				return true
			}
			if len(digits) > 4300 {
				return false
			}
			n, ok := new(big.Int).SetString(digits, 10)
			if !ok {
				return false
			}
			keys = append(keys, n)
			digits = ""
			return true
		}
		for _, r := range name {
			if unicode.IsDigit(r) {
				digits += string(rune('0' + decimal(r)))
			} else if !flush() {
				return Release{Caught: true}, nil
			}
		}
		if !flush() {
			return Release{Caught: true}, nil
		}
		tags = append(tags, tag{name, keys})
	}
	if len(tags) == 0 {
		return Release{}, nil
	}
	sort.SliceStable(tags, func(i, j int) bool {
		a, b := tags[i].digits, tags[j].digits
		for k := 0; k < len(a) && k < len(b); k++ {
			if c := a[k].Cmp(b[k]); c != 0 {
				return c < 0
			}
		}
		return len(a) < len(b)
	})
	v := tags[len(tags)-1].value
	return Release{Version: &v}, nil
}

// The selector never uses numeric metadata: nonfinite source JSON numbers are
// equivalent to null here (both fail when used as release/tag_name objects).
func sourceNumbers(raw []byte) []byte {
	out := []byte{}
	quoted := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '"' {
			quoted = !quoted
		}
		if quoted && c == '\\' && i+1 < len(raw) {
			out = append(out, c, raw[i+1])
			i++
			continue
		}
		if !quoted {
			matched := false
			for _, word := range []string{"-Infinity", "Infinity", "NaN"} {
				if bytes.HasPrefix(raw[i:], []byte(word)) {
					end := i + len(word)
					if end == len(raw) || strings.ContainsRune(",]} \t\r\n", rune(raw[end])) {
						out = append(out, "null"...)
						i = end - 1
						matched = true
						break
					}
				}
			}
			if matched {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func unpairedSurrogate(raw []byte) bool {
	quoted := false
	hex := func(data []byte) (uint16, bool) {
		if len(data) < 4 {
			return 0, false
		}
		var n uint16
		for _, b := range data[:4] {
			n <<= 4
			switch {
			case b >= '0' && b <= '9':
				n += uint16(b - '0')
			case b >= 'a' && b <= 'f':
				n += uint16(b - 'a' + 10)
			case b >= 'A' && b <= 'F':
				n += uint16(b - 'A' + 10)
			default:
				return 0, false
			}
		}
		return n, true
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		n, ok := hex(raw[i+1:])
		if !ok {
			continue
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return true
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return true
		}
		low, ok := hex(raw[i+3:])
		if !ok || low < 0xdc00 || low > 0xdfff {
			return true
		}
		i += 6
	}
	return false
}

func decimal(r rune) int {
	for _, t := range unicode.Digit.R16 {
		if uint32(r) >= uint32(t.Lo) && uint32(r) <= uint32(t.Hi) && (uint32(r)-uint32(t.Lo))%uint32(t.Stride) == 0 {
			return int((uint32(r)-uint32(t.Lo))/uint32(t.Stride)) % 10
		}
	}
	for _, t := range unicode.Digit.R32 {
		if uint32(r) >= t.Lo && uint32(r) <= t.Hi && (uint32(r)-t.Lo)%t.Stride == 0 {
			return int((uint32(r)-t.Lo)/t.Stride) % 10
		}
	}
	return 0
}

// Fetch uses a fresh requests-style session. Proxy/environment resolution is
// supplied by the sealed source-settings reader, never the new process env.
func Fetch(ctx context.Context, base *http.Transport, environment []string, headers map[string]string, target string, authorize func(context.Context, *url.URL) error) (Release, error) {
	pairs := [][2]string{}
	remove := []string{}
	if len(headers) == 0 {
		pairs = append(pairs, [2]string{"User-Agent", "python-urllib3/2.7.0"}, [2]string{"Content-Type", "application/x-www-form-urlencoded; charset=UTF-8"})
		remove = []string{"Accept"}
	} else {
		pairs = append(pairs, [2]string{"User-Agent", "python-requests/2.34.2"})
		names := make([]string, 0, len(headers))
		seen := map[string]bool{}
		for k := range headers {
			lower := strings.ToLower(k)
			if seen[lower] {
				return Release{}, ErrDependency
			}
			seen[lower] = true
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			pairs = append(pairs, [2]string{k, headers[k]})
		}
	}
	guardFailed := false
	wire := apprise.HTTPTransport{Base: base, Environment: func() []string { return append([]string{}, environment...) }, Authorize: func(c context.Context, u *url.URL) error {
		if authorize != nil {
			if e := authorize(c, u); e != nil {
				guardFailed = true
				return e
			}
		}
		return nil
	}}
	response, e := wire.Send(ctx, apprise.Request{Method: "GET", URL: target, HeaderOrder: pairs, RemoveHeaders: remove, Timeout: [2]float64{20, 20}, Verify: false})
	if e != nil {
		if ctx.Err() != nil {
			return Release{}, ctx.Err()
		}
		if guardFailed {
			return Release{}, e
		}
		return Release{Caught: true}, nil
	}
	if response.Status >= 400 {
		digest := sha256.Sum256(response.Content)
		return Release{Status: response.Status, BodySHA: hex.EncodeToString(digest[:])}, nil
	}
	out, e := SelectRelease(response.Content)
	out.Status = response.Status
	digest := sha256.Sum256(response.Content)
	out.BodySHA = hex.EncodeToString(digest[:])
	return out, e
}

func Message(server, front Release, localServer, localFront string) string {
	line := func(name string, remote *string, local string) string {
		if remote != nil && *remote == local {
			return "当前" + name + "版本：" + local + "，已是最新版本"
		}
		r := "None"
		if remote != nil {
			r = *remote
		}
		return "当前" + name + "版本：" + local + "，远程版本：" + r
	}
	return line("后端", server.Version, localServer) + "\n" + line("前端", front.Version, localFront)
}

func Trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

// DecodeVersionFile follows Python UTF-8 errors=replace's maximal subparts.
func DecodeVersionFile(data []byte) string {
	var out strings.Builder
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		if r != utf8.RuneError || n > 1 {
			out.WriteRune(r)
			data = data[n:]
			continue
		}
		n = 1
		b := data[0]
		want := 0
		switch {
		case b >= 0xc2 && b <= 0xdf:
			want = 2
		case b >= 0xe0 && b <= 0xef:
			want = 3
		case b >= 0xf0 && b <= 0xf4:
			want = 4
		}
		if want > 0 {
			for n < want && n < len(data) {
				c := data[n]
				if c < 0x80 || c > 0xbf || n == 1 && (b == 0xe0 && c < 0xa0 || b == 0xed && c >= 0xa0 || b == 0xf0 && c < 0x90 || b == 0xf4 && c >= 0x90) {
					break
				}
				n++
			}
		}
		out.WriteRune(utf8.RuneError)
		data = data[n:]
	}
	return Trim(out.String())
}
