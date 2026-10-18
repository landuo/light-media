package encodingdetector

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"regexp"
	"sort"
	"strings"
)

type Result struct {
	Encoding   string  `json:"encoding"`
	Confidence float64 `json:"confidence"`
	Language   string  `json:"language"`
	MIMEType   string  `json:"mime_type"`
}

func found(e string, c float64) *Result { return &Result{Encoding: e, Confidence: c} }
func prefix(s []byte, n int) []byte {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func asciiByte(b byte) bool { return b == 9 || b == 10 || b == 13 || b >= 32 && b <= 126 }
func detectASCII(d []byte) *Result {
	if len(d) == 0 {
		return nil
	}
	n := 0
	for _, b := range d {
		if !asciiByte(b) {
			if b != 0 {
				return nil
			}
			n++
		}
	}
	if n == 0 {
		return found("ascii", 1)
	}
	if float64(n)/float64(len(d)) <= .05 {
		return found("ascii", .99)
	}
	return nil
}
func detectBOM(d []byte) *Result {
	for _, x := range []struct{ b, e string }{{"\x00\x00\xfe\xff", "utf-32"}, {"\xff\xfe\x00\x00", "utf-32"}, {"\xef\xbb\xbf", "utf-8-sig"}, {"\xfe\xff", "utf-16"}, {"\xff\xfe", "utf-16"}} {
		if bytes.HasPrefix(d, []byte(x.b)) {
			if x.e == "utf-32" && (len(d)-4)%4 != 0 {
				continue
			}
			return found(x.e, 1)
		}
	}
	return nil
}
func detectUTF8(d []byte) *Result {
	seq, mb := 0, 0
	for i := 0; i < len(d); {
		b := d[i]
		if b < 128 {
			i++
			continue
		}
		n := 0
		switch {
		case b >= 0xc2 && b <= 0xdf:
			n = 2
		case b >= 0xe0 && b <= 0xef:
			n = 3
		case b >= 0xf0 && b <= 0xf4:
			n = 4
		default:
			return nil
		}
		if i+n > len(d) {
			break
		}
		for j := 1; j < n; j++ {
			if d[i+j] < 0x80 || d[i+j] > 0xbf {
				return nil
			}
		}
		if n == 3 && (b == 0xe0 && d[i+1] < 0xa0 || b == 0xed && d[i+1] > 0x9f) || n == 4 && (b == 0xf0 && d[i+1] < 0x90 || b == 0xf4 && d[i+1] > 0x8f) {
			return nil
		}
		seq++
		mb += n
		i += n
	}
	if seq == 0 {
		return nil
	}
	return found("utf-8", math.Min(.99, .8+(.99-.8)*math.Min(float64(mb)/float64(len(d))*6, 1)))
}
func binaryContent(d []byte) bool {
	n := 0
	for _, b := range d {
		if b < 9 || b >= 14 && b < 32 {
			n++
		}
	}
	return len(d) > 0 && float64(n)/float64(len(d)) > .01
}
func textRunes(s string) []rune { return []rune(s) }
func looksText(s string) bool {
	r := textRunes(s)
	if len(r) == 0 {
		return false
	}
	if len(r) > 500 {
		r = r[:500]
	}
	n := 0
	for _, c := range r {
		if c < 0x110000 && (unicodeFlags[c]&1 != 0 || c == '\n' || c == '\r' || c == '\t') {
			n++
		}
	}
	return float64(n)/float64(len(r)) > .7
}
func textQuality(s string) float64 {
	r := textRunes(s)
	if len(r) > 500 {
		r = r[:500]
	}
	if len(r) == 0 {
		return -1
	}
	l, m, sp, c, a := 0, 0, 0, 0, 0
	for _, v := range r {
		f := unicodeFlags[v]
		switch {
		case f&2 != 0:
			l++
			if v < 128 {
				a++
			}
		case f&4 != 0:
			m++
		case f&8 != 0 || v == '\n' || v == '\r' || v == '\t':
			sp++
		case f&16 != 0:
			c++
		}
	}
	n := float64(len(r))
	if float64(c)/n > .1 || float64(m)/n > .2 {
		return -1
	}
	sc := float64(l)/n + (float64(a)/n)*.5
	if len(r) > 20 && sp > 0 {
		sc += .1
	}
	return sc
}
func utf1632(d []byte) *Result {
	d = prefix(d, 4096)
	if len(d) < 10 {
		return nil
	}
	n := len(d) / 4
	trim := d[:n*4]
	if len(trim) >= 16 {
		for _, x := range []struct {
			enc           string
			first, second int
		}{{"utf-32-be", 0, 1}, {"utf-32-le", 3, 2}} {
			a, b := 0, 0
			for i := 0; i < len(trim); i += 4 {
				if trim[i+x.first] == 0 {
					a++
				}
				if trim[i+x.second] == 0 {
					b++
				}
			}
			if a == n && float64(b)/float64(n) > .5 {
				s, e := decode(trim, x.enc, "strict")
				if e == nil && looksText(s) {
					return found(x.enc, .95)
				}
			}
		}
	}
	d = d[:len(d)/2*2]
	n = len(d) / 2
	candidates := []string{}
	for _, x := range []struct {
		enc string
		pos int
	}{{"utf-16-le", 1}, {"utf-16-be", 0}} {
		null := 0
		for i := x.pos; i < len(d); i += 2 {
			if d[i] == 0 {
				null++
			}
		}
		frac := float64(null) / float64(n)
		sep := frac < .15
		for _, b := range d {
			if b != 0 && !asciiByte(b) {
				sep = false
				break
			}
		}
		if frac >= .03 && !sep {
			candidates = append(candidates, x.enc)
		}
	}
	if len(candidates) == 1 {
		s, e := decode(d, candidates[0], "strict")
		if e == nil && looksText(s) {
			return found(candidates[0], .95)
		}
		return nil
	}
	best := ""
	quality := -1.
	for _, c := range candidates {
		s, e := decode(d, c, "strict")
		if e != nil {
			continue
		}
		q := textQuality(s)
		if q > quality {
			best = c
			quality = q
		}
	}
	if best != "" && quality >= .5 {
		return found(best, .95)
	}
	return nil
}

var b64 = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")

func b64value(b byte) int { return bytes.IndexByte(b64, b) }
func validUTF7B64(d []byte) bool {
	bits := len(d) * 6
	pad := bits % 16
	if pad > 0 && b64value(d[len(d)-1])&((1<<pad)-1) != 0 {
		return false
	}
	raw := make([]byte, 0, bits/8)
	buf, nb := uint64(0), 0
	for _, c := range d {
		buf = buf<<6 | uint64(b64value(c))
		nb += 6
		if nb >= 8 {
			nb -= 8
			raw = append(raw, byte(buf>>nb))
		}
	}
	high := false
	for i := 0; i+1 < len(raw); i += 2 {
		u := uint16(raw[i])<<8 | uint16(raw[i+1])
		switch {
		case u >= 0xd800 && u <= 0xdbff:
			if high {
				return false
			}
			high = true
		case u >= 0xdc00 && u <= 0xdfff:
			if !high {
				return false
			}
			high = false
		default:
			if high {
				return false
			}
			high = false
		}
	}
	return !high
}
func hasUTF7(d []byte) bool {
	for start := 0; start < len(d); {
		p := bytes.IndexByte(d[start:], '+')
		if p < 0 {
			return false
		}
		p += start
		pos := p + 1
		if pos < len(d) && d[pos] == '-' {
			start = pos + 1
			continue
		}
		if pos < len(d) && d[pos] == '+' {
			for pos < len(d) && d[pos] == '+' {
				pos++
			}
			start = pos
			continue
		}
		count := 0
		for i := p - 1; i >= 0; i-- {
			if d[i] == '\r' || d[i] == '\n' {
				continue
			}
			if b64value(d[i]) >= 0 || d[i] == '=' {
				count++
			} else {
				break
			}
		}
		if count >= 4 {
			start = pos
			continue
		}
		i := pos
		upper, lower := false, false
		for i < len(d) && b64value(d[i]) >= 0 {
			if d[i] >= 'A' && d[i] <= 'Z' {
				upper = true
			}
			if d[i] >= 'a' && d[i] <= 'z' {
				lower = true
			}
			i++
		}
		if i-pos >= 3 && !(lower && !upper) && validUTF7B64(d[pos:i]) {
			return true
		}
		start = max(pos, i)
	}
	return false
}
func escapeEncoding(d []byte) *Result {
	has := func(s string) bool { return bytes.Contains(d, []byte(s)) }
	jp := func(e string) *Result { r := found(e, .95); r.Language = "ja"; return r }
	if has("\x1b") {
		if has("\x1b$(O") || has("\x1b$(P") || has("\x1b$(Q") {
			return jp("iso2022_jp_2004")
		}
		if has("\x1b(I") {
			return jp("iso2022_jp_ext")
		}
		if has("\x1b$B") || has("\x1b$@") || has("\x1b(J") || has("\x1b$(D") {
			if has("\x0e") && has("\x0f") {
				return jp("iso2022_jp_ext")
			}
			return jp("iso2022_jp_2")
		}
		if has("\x1b$)C") {
			r := found("iso2022_kr", .95)
			r.Language = "ko"
			return r
		}
	}
	if has("~{") && has("~}") {
		for start := 0; start < len(d); {
			a := bytes.Index(d[start:], []byte("~{"))
			if a < 0 {
				break
			}
			a += start
			b := bytes.Index(d[a+2:], []byte("~}"))
			if b < 0 {
				break
			}
			b += a + 2
			v := d[a+2 : b]
			valid := len(v) >= 2 && len(v)%2 == 0
			for _, c := range v {
				if c < 0x21 || c > 0x7e {
					valid = false
				}
			}
			if valid {
				r := found("hz", .95)
				r.Language = "zh"
				return r
			}
			start = b + 2
		}
	}
	maxByte := byte(0)
	for _, b := range d {
		if b > maxByte {
			maxByte = b
		}
	}
	if has("+") && maxByte < 128 && hasUTF7(d) {
		return found("utf-7", .95)
	}
	return nil
}

var markupPatterns = []*regexp.Regexp{regexp.MustCompile(`<\?xml[^>]+encoding[\t\n\v\f\r ]*=[\t\n\v\f\r ]*['"]([^'"]+)['"]`), regexp.MustCompile(`<meta[^>]+charset[\t\n\v\f\r ]*=[\t\n\v\f\r ]*['"]?[\t\n\v\f\r ]*([^\t\n\v\f\r '">;]+)`), regexp.MustCompile(`<meta[^>]+content[\t\n\v\f\r ]*=[\t\n\v\f\r ]*['"][^'"]*charset=([^\t\n\v\f\r '">;]+)`)}
var pep = regexp.MustCompile(`(?m)^[ \t\f]*#.*?coding[:=][ \t]*([-\w.]+)`)

func declaration(d []byte) *Result {
	head := prefix(d, 4096)
	for i, p := range markupPatterns {
		lowerHead := append([]byte(nil), head...)
		for j, b := range lowerHead {
			if b >= 'A' && b <= 'Z' {
				lowerHead[j] = b + 32
			}
		}
		m := p.FindSubmatch(lowerHead)
		if m == nil {
			continue
		}
		enc := lookup(strings.Trim(string(m[1]), " \t\r\n\v\f"))
		if enc == "" {
			continue
		}
		if validate(head, enc) {
			r := found(enc, .95)
			r.MIMEType = "text/html"
			if i == 0 {
				r.MIMEType = "text/xml"
			}
			return r
		}
	}
	if !bytes.Contains(prefix(d, 200), []byte("#")) {
		return nil
	}
	lines := bytes.SplitN(d, []byte("\n"), 3)
	if len(lines) > 2 {
		lines = lines[:2]
	}
	m := pep.FindSubmatch(bytes.Join(lines, []byte("\n")))
	if m != nil {
		enc := lookup(string(m[1]))
		if enc != "" {
			if validate(head, enc) {
				r := found(enc, .95)
				r.MIMEType = "text/x-python"
				return r
			}
		}
	}
	return nil
}
func magic(d []byte) *Result {
	r := func(m string) *Result { return &Result{Confidence: 1, MIMEType: m} }
	if len(d) >= 12 && string(d[4:8]) == "ftyp" {
		sz := binary.BigEndian.Uint32(d)
		if sz >= 8 && uint64(sz) <= uint64(len(d)) {
			brand := string(d[8:12])
			m := "video/mp4"
			switch brand {
			case "avif", "avis":
				m = "image/avif"
			case "heic", "heix":
				m = "image/heic"
			case "mif1", "msf1":
				m = "image/heif"
			case "M4A ", "M4B ", "F4A ":
				m = "audio/mp4"
			case "qt  ":
				m = "video/quicktime"
			}
			return r(m)
		}
	}
	if len(d) >= 12 {
		table := meta.Riff
		if string(d[:4]) == "FORM" {
			table = meta.Form
		} else if string(d[:4]) != "RIFF" {
			table = nil
		}
		if m := table[hex.EncodeToString(d[8:12])]; m != "" {
			return r(m)
		}
	}
	if bytes.HasPrefix(d, []byte("PK\x03\x04")) {
		return r(classifyZIP(d))
	}
	if len(d) >= 8 && string(d[:4]) == "\xca\xfe\xba\xbe" {
		if binary.BigEndian.Uint32(d[4:8]) <= 20 {
			return r("application/x-mach-binary")
		}
		return r("application/java-vm")
	}
	for _, p := range meta.Magic {
		b, _ := hex.DecodeString(p[0])
		if bytes.HasPrefix(d, b) {
			return r(p[1])
		}
	}
	if len(d) >= 263 && (string(d[257:263]) == "ustar\x00" || string(d[257:263]) == "ustar ") {
		return r("application/x-tar")
	}
	return nil
}
func classifyZIP(d []byte) string {
	scan := prefix(d, 4096)
	for off := 0; off < len(scan); {
		idx := bytes.Index(scan[off:], []byte("PK\x03\x04"))
		if idx < 0 {
			break
		}
		idx += off
		if len(scan) < idx+30 {
			break
		}
		nl := int(binary.LittleEndian.Uint16(scan[idx+26:]))
		el := int(binary.LittleEndian.Uint16(scan[idx+28:]))
		ns := idx + 30
		if len(scan) < ns+nl {
			break
		}
		name := scan[ns : ns+nl]
		for _, p := range meta.ZipPrefixes {
			b, _ := hex.DecodeString(p[0])
			if bytes.HasPrefix(name, b) {
				return p[1]
			}
		}
		for _, p := range meta.ZipSuffixes {
			b, _ := hex.DecodeString(p[0])
			if bytes.Contains(name, b) {
				return p[1]
			}
		}
		if string(name) == "mimetype" && binary.LittleEndian.Uint16(scan[idx+8:]) == 0 {
			cs := ns + nl + el
			cl := uint64(binary.LittleEndian.Uint32(scan[idx+22:]))
			if uint64(len(scan)) >= uint64(cs)+cl {
				s := string(scan[cs : uint64(cs)+cl])
				switch s {
				case "application/vnd.oasis.opendocument.text", "application/vnd.oasis.opendocument.spreadsheet", "application/vnd.oasis.opendocument.presentation", "application/vnd.oasis.opendocument.graphics":
					return s
				}
			}
		}
		sz := uint64(0)
		if binary.LittleEndian.Uint16(scan[idx+6:])&8 == 0 {
			sz = uint64(binary.LittleEndian.Uint32(scan[idx+18:]))
		}
		no := uint64(ns+nl+el) + sz
		if no > uint64(len(scan)) {
			break
		}
		off = int(no)
	}
	return "application/zip"
}

type analysis struct {
	Ratio, Coverage float64
	Diversity       int
}

func analyze(d []byte, enc string) analysis {
	leads, pairs, mb := 0, 0, 0
	seen := [256]bool{}
	known := true
	for i := 0; i < len(d); {
		b := d[i]
		lead := false
		n := 0
		trail := func(lo, hi byte) bool { return i+1 < len(d) && d[i+1] >= lo && d[i+1] <= hi }
		switch enc {
		case "shift_jis_2004", "cp932":
			upper := byte(0xef)
			if enc == "cp932" {
				upper = 0xfc
			}
			lead = b >= 0x81 && b <= 0x9f || b >= 0xe0 && b <= upper
			if lead && (trail(0x40, 0x7e) || trail(0x80, 0xfc)) {
				n = 2
			}
		case "euc_jis_2004":
			lead = b == 0x8e || b == 0x8f || b >= 0xa1 && b <= 0xfe
			if b == 0x8e && trail(0xa1, 0xdf) {
				n = 2
			} else if b == 0x8f && i+2 < len(d) && trail(0xa1, 0xfe) && d[i+2] >= 0xa1 && d[i+2] <= 0xfe {
				n = 3
			} else if b >= 0xa1 && b <= 0xfe && trail(0xa1, 0xfe) {
				n = 2
			}
		case "euc_kr":
			lead = b >= 0xa1 && b <= 0xfe
			if lead && trail(0xa1, 0xfe) {
				n = 2
			}
		case "cp949":
			lead = b >= 0x81 && b <= 0xc8 || b >= 0xca && b <= 0xfd
			if lead && (trail(0x41, 0x5a) || trail(0x61, 0x7a) || trail(0x81, 0xfe)) {
				n = 2
			}
		case "gb18030":
			lead = b >= 0x81 && b <= 0xfe
			if lead && i+3 < len(d) && trail(0x30, 0x39) && d[i+2] >= 0x81 && d[i+2] <= 0xfe && d[i+3] >= 0x30 && d[i+3] <= 0x39 {
				n = 4
			} else if b >= 0xa1 && b <= 0xf7 && trail(0xa1, 0xfe) {
				n = 2
			}
		case "big5hkscs":
			lead = b >= 0x87 && b <= 0xfe
			if lead && (trail(0x40, 0x7e) || trail(0xa1, 0xfe)) {
				n = 2
			}
		case "johab":
			lead = b >= 0x84 && b <= 0xd3 || b >= 0xd8 && b <= 0xde || b >= 0xe0 && b <= 0xf9
			if lead && (trail(0x31, 0x7e) || trail(0x91, 0xfe)) {
				n = 2
			}
		default:
			known = false
		}
		if !known {
			return analysis{Diversity: 256}
		}
		if lead {
			leads++
		}
		if n > 0 {
			pairs++
			seen[b] = true
			for _, v := range d[i : i+n] {
				if v > 127 {
					mb++
				}
			}
			i += n
		} else {
			i++
		}
	}
	a := analysis{}
	if leads > 0 {
		a.Ratio = float64(pairs) / float64(leads)
	}
	non := 0
	for _, b := range d {
		if b > 127 {
			non++
		}
	}
	if non > 0 {
		a.Coverage = float64(mb) / float64(non)
	}
	for _, v := range seen {
		if v {
			a.Diversity++
		}
	}
	return a
}
func postprocess(d []byte, r []Result) []Result {
	if len(r) > 1 {
		top := r[0]
		for i := 1; i < len(r); i++ {
			if i > 1 && top.Confidence-r[i].Confidence > .005 {
				break
			}
			var pair *pairValue
			for j := range confusion {
				p := &confusion[j]
				if p.A == top.Encoding && p.B == r[i].Encoding || p.B == top.Encoding && p.A == r[i].Encoding {
					pair = p
					break
				}
			}
			if pair == nil {
				continue
			}
			p := newProfile(d, pair.Categories)
			sa, _ := bestLanguage(p, pair.A)
			sb, _ := bestLanguage(p, pair.B)
			winner := ""
			if sa > sb {
				winner = pair.A
			} else if sb > sa {
				winner = pair.B
			} else {
				pref := []int{10, 10, 10, 9, 9, 5, 5, 5, 8, 7, 7, 6, 6, 6, 6, 6, 6, 6, 5, 5, 4, 4, 3, 3, 3, 1, 2, 0, 1, 0}
				seen := [256]bool{}
				a, b := 0, 0
				for _, v := range d {
					if seen[v] {
						continue
					}
					seen[v] = true
					if cats, ok := pair.Categories[v]; ok {
						x, y := 0, 0
						if int(cats[0]) < len(pref) {
							x = pref[cats[0]]
						}
						if int(cats[1]) < len(pref) {
							y = pref[cats[1]]
						}
						if x > y {
							a += x - y
						} else {
							b += y - x
						}
					}
				}
				if a > b {
					winner = pair.A
				} else if b > a {
					winner = pair.B
				}
			}
			if winner != "" && winner == r[i].Encoding {
				v := r[i]
				v.Confidence = top.Confidence
				r = append([]Result{v}, append(r[:i], r[i+1:]...)...)
				break
			}
		}
	}
	if len(r) > 1 {
		if diffs, ok := meta.Demotions[r[0].Encoding]; ok {
			has := false
			for _, b := range d {
				if bytes.IndexByte(diffs, b) >= 0 {
					has = true
					break
				}
			}
			if !has {
				for i := 1; i < len(r); i++ {
					if r[i].Encoding == "iso8859-1" || r[i].Encoding == "iso8859-15" || r[i].Encoding == "cp1252" {
						demoted := r[0]
						v := r[i]
						v.Confidence = demoted.Confidence
						out := []Result{v}
						for j := 1; j < len(r); j++ {
							if j != i {
								out = append(out, r[j])
							}
						}
						r = append(out, demoted)
						break
					}
				}
			}
		}
	}
	if len(r) > 0 && r[0].Encoding == "koi8-r" {
		has := false
		for _, b := range d {
			if bytes.IndexByte(meta.Koi8T, b) >= 0 {
				has = true
				break
			}
		}
		if has {
			for i, v := range r {
				if v.Encoding == "koi8-t" {
					v.Confidence = r[0].Confidence
					r = append([]Result{v}, append(r[:i], r[i+1:]...)...)
					break
				}
			}
		}
	}
	return r
}
func fillMetadata(d []byte, r []Result) []Result {
	var p *profile
	for i := range r {
		v := &r[i]
		if v.Encoding != "" && v.Language == "" {
			enc := byName[v.Encoding]
			if len(enc.Languages) == 1 {
				v.Language = enc.Languages[0]
			}
			if v.Language == "" && len(d) > 0 && len(modelIndex[v.Encoding]) > 0 {
				if p == nil {
					p = newProfile(d, nil)
				}
				_, v.Language = bestLanguage(p, v.Encoding)
			}
			if v.Language == "" && len(d) > 0 && len(modelIndex["utf-8"]) > 0 {
				b := d
				if v.Encoding != "utf-8" {
					s, e := decode(d, v.Encoding, "ignore")
					if e != nil {
						continue
					}
					b = []byte(s)
				}
				if len(b) > 0 {
					_, v.Language = bestLanguage(newProfile(b, nil), "utf-8")
				}
			}
		}
		if v.MIMEType == "" {
			v.MIMEType = "text/plain"
			if v.Encoding == "" {
				v.MIMEType = "application/octet-stream"
			}
		}
		if v.Confidence > 1 {
			v.Confidence = 1
		}
	}
	return r
}
func run(d []byte) ([]Result, error) {
	d = prefix(d, 200000)
	fallback := func() []Result { return []Result{{Encoding: "cp1252", Confidence: .1}} }
	if len(d) == 0 {
		return []Result{{Encoding: "utf-8", Confidence: .1}}, nil
	}
	early := []func([]byte) *Result{detectBOM, utf1632, escapeEncoding, magic}
	for _, f := range early {
		if r := f(d); r != nil {
			return []Result{*r}, nil
		}
	}
	u, a := detectUTF8(d), detectASCII(d)
	if u == nil && a == nil && binaryContent(d) {
		return []Result{{Confidence: .95, MIMEType: "application/octet-stream"}}, nil
	}
	if r := declaration(d); r != nil {
		promote := map[string]string{"shift_jis_2004": "cp932", "euc_kr": "cp949"}
		if sup := promote[r.Encoding]; sup != "" {
			if validate(d, sup) && analyze(d, sup).Ratio > analyze(d, r.Encoding).Ratio {
				r.Encoding = sup
			}
		}
		return []Result{*r}, nil
	}
	if a != nil {
		return []Result{*a}, nil
	}
	if u != nil {
		return []Result{*u}, nil
	}
	non := 0
	for _, b := range d {
		if b > 127 {
			non++
		}
	}
	valid := []EncodingInfo{}
	analyses := map[string]analysis{}
	for _, c := range meta.Registry {
		if !validate(d, c.Name) {
			continue
		}
		if c.Multibyte {
			an := analyze(d, c.Name)
			if an.Ratio < .05 || non < 2 || an.Coverage < .35 || non >= 16 && an.Diversity < 4 {
				continue
			}
			analyses[c.Name] = an
		}
		valid = append(valid, c)
	}
	if len(valid) == 0 {
		return fallback(), nil
	}
	best := 0.
	for _, a := range analyses {
		if a.Ratio > best {
			best = a.Ratio
		}
	}
	ordered := valid
	if best >= .85 {
		ordered = append([]EncodingInfo(nil), valid...)
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].Multibyte != ordered[j].Multibyte {
				return ordered[i].Multibyte
			}
			if ordered[i].Multibyte {
				return analyses[ordered[i].Name].Ratio > analyses[ordered[j].Name].Ratio
			}
			return false
		})
	}
	r := statistical(prefix(d, 16384), ordered)
	if len(r) == 0 {
		return fallback(), nil
	}
	if best >= .85 {
		for i := range r {
			cov := analyses[r[i].Encoding].Coverage
			if cov >= .95 {
				r[i].Confidence *= 1 + cov
			}
		}
		sort.SliceStable(r, func(i, j int) bool { return r[i].Confidence > r[j].Confidence })
	}
	return postprocess(d, r), nil
}
func DetectAll(d []byte) ([]Result, error) {
	if e := loadData(); e != nil {
		return nil, e
	}
	r, e := run(d)
	if e != nil {
		return nil, e
	}
	r = fillMetadata(prefix(d, 2048), r)
	for i := range r {
		if name := meta.Compat[r[i].Encoding]; name != "" {
			r[i].Encoding = name
		}
	}
	return r, nil
}
func Detect(d []byte) (string, float64, error) {
	r, e := DetectAll(d)
	if e != nil {
		return "", 0, e
	}
	return r[0].Encoding, r[0].Confidence, nil
}
