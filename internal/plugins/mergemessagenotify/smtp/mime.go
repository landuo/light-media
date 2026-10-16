// Package mergemessagesmtp ports the installed EmailChannel's MIME/SMTP
// dependency in Go. Original PSF source and plugin provenance are in reference/.
package mergemessagesmtp

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	merge "light-media/internal/plugins/mergemessagenotify"
	"light-media/internal/releasemeta/legacyanime"
)

type Error struct {
	Class, Phase string
	Code         int
}

func (e *Error) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("SMTP %s: %s (%d)", e.Phase, e.Class, e.Code)
	}
	return "SMTP " + e.Phase + ": " + e.Class
}
func failure(class, phase string) *Error { return &Error{Class: class, Phase: phase} }

type prepared struct {
	request             merge.SMTPRequest
	from, body, subtype string
}

func prepare(r merge.SMTPRequest) (prepared, error) {
	for _, s := range append([]string{r.Title, r.Text, r.HTML, r.FromName, r.From, r.Date}, r.To...) {
		if !utf8.ValidString(s) {
			return prepared{}, failure("UnicodeEncodeError", "mime_construct")
		}
	}
	from, e := formatAddr(r.FromName, r.From)
	if e != nil {
		return prepared{}, e
	}
	body, subtype := r.Text, "plain"
	if r.HTML != "" {
		body = r.HTML
		subtype = "html"
	}
	var encoded strings.Builder
	raw := []byte(body)
	for i := 0; i < len(raw); i += 57 {
		end := i + 57
		if end > len(raw) {
			end = len(raw)
		}
		encoded.WriteString(base64.StdEncoding.EncodeToString(raw[i:end]))
		encoded.WriteByte('\n')
	}
	r.To = append([]string(nil), r.To...)
	return prepared{r, from, encoded.String(), subtype}, nil
}

// BuildMessage matches MIMEText(...,utf-8).as_string() raw LF bytes. SMTP DATA
// later performs the source's separate ASCII/CRLF/dot-stuff transformation.
func BuildMessage(r merge.SMTPRequest) ([]byte, error) {
	p, e := prepare(r)
	if e != nil {
		return nil, e
	}
	return p.serialize()
}
func (p prepared) serialize() ([]byte, error) {
	r := p.request
	headers := []struct {
		name, value string
		utf8        bool
	}{{"Content-Type", `text/` + p.subtype + `; charset="utf-8"`, false}, {"MIME-Version", "1.0", false}, {"Content-Transfer-Encoding", "base64", false}, {"From", p.from, false}, {"Date", r.Date, false}, {"To", strings.Join(r.To, ","), false}, {"Subject", r.Title, true}}
	var b strings.Builder
	for _, h := range headers {
		v, e := encodeHeader(h.name, h.value, h.utf8)
		if e != nil {
			return nil, e
		}
		b.WriteString(h.name)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(p.body)
	return []byte(b.String()), nil
}
func ascii(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}
func formatAddr(name, address string) (string, error) {
	if !ascii(address) {
		return "", failure("UnicodeEncodeError", "mime_construct")
	}
	if name == "" {
		return address, nil
	}
	if !ascii(name) {
		return encodedWord(name, chooseBase64(name)) + " <" + address + ">", nil
	}
	quote := strings.ContainsAny(name, "[]\\()<>@,:;\".")
	name = strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(name)
	if quote {
		name = "\"" + name + "\""
	}
	return name + " <" + address + ">", nil
}
func q(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		if c == ' ' {
			b.WriteByte('_')
		} else if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-!*+/", rune(c)) {
			b.WriteByte(c)
		} else {
			b.WriteByte('=')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
func chooseBase64(s string) bool { return base64.StdEncoding.EncodedLen(len([]byte(s))) < qLength(s) }
func encodedWord(s string, b64 bool) string {
	if s == "" {
		return ""
	}
	if b64 {
		return "=?utf-8?b?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
	}
	return "=?utf-8?q?" + q(s) + "?="
}
func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == 0x1c || r == 0x1d || r == 0x1e || r == 0x85 || r == 0x2028 || r == 0x2029 {
			out = append(out, s[start:i])
			i += n
			if r == '\r' && i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
		} else {
			i += n
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
func lstrip(s string) string {
	return strings.TrimLeftFunc(s, func(r rune) bool { return legacyanime.Strip(string(r)) == "" })
}

type chunk struct{ fws, text string }
type formatter struct {
	initial, max int
	parts        []chunk
	lines        []string
}

func (f *formatter) length() int {
	n := f.initial
	for _, p := range f.parts {
		n += utf8.RuneCountInString(p.fws) + utf8.RuneCountInString(p.text)
	}
	return n
}
func (f *formatter) text() string {
	var b strings.Builder
	for _, p := range f.parts {
		b.WriteString(p.fws)
		b.WriteString(p.text)
	}
	return b.String()
}
func (f *formatter) pop() chunk {
	if len(f.parts) == 0 {
		return chunk{}
	}
	p := f.parts[len(f.parts)-1]
	f.parts = f.parts[:len(f.parts)-1]
	return p
}
func (f *formatter) newline() {
	end := f.pop()
	if end != (chunk{" ", ""}) {
		f.parts = append(f.parts, end)
	}
	if f.length() > 0 {
		txt := f.text()
		onlyWS := f.initial == 0 && (txt == "" || legacyanime.Strip(txt) == "")
		if onlyWS && len(f.lines) > 0 {
			f.lines[len(f.lines)-1] += txt
		} else {
			f.lines = append(f.lines, txt)
		}
	}
	f.parts = nil
	f.initial = 0
}
func (f *formatter) appendChunk(ws, text string) {
	f.parts = append(f.parts, chunk{ws, text})
	if f.length() <= f.max {
		return
	}
	split := -1
	for _, ch := range ";, \t" {
		for i := len(f.parts) - 1; i > 0; i-- {
			if (ch == ' ' || ch == '\t') && strings.HasPrefix(f.parts[i].fws, string(ch)) {
				split = i
				break
			}
			if strings.HasSuffix(f.parts[i-1].text, string(ch)) {
				split = i
				break
			}
		}
		if split >= 0 {
			break
		}
	}
	if split < 0 {
		last := f.pop()
		if f.initial > 0 {
			f.newline()
			if last.fws == "" {
				last.fws = " "
			}
		}
		f.parts = append(f.parts, last)
		return
	}
	remainder := append([]chunk(nil), f.parts[split:]...)
	f.parts = f.parts[:split]
	f.lines = append(f.lines, f.text())
	f.parts = remainder
	f.initial = 0
}
func (f *formatter) feed(ws, text string, encoded bool) {
	if !encoded {
		s := ws + text
		start := 0
		fws := ""
		for i := 0; i < len(s); {
			if s[i] == ' ' || s[i] == '\t' {
				if i > start {
					f.appendChunk(fws, s[start:i])
					fws = ""
				}
				j := i
				for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
					j++
				}
				fws = s[i:j]
				i = j
				start = j
			} else {
				i++
			}
		}
		f.appendChunk(fws, s[start:])
		return
	}
	b64 := chooseBase64(text)
	maxlen := f.max - f.length() - 12
	current := []rune{}
	octets, quoted := 0, 0
	encodedLines := []*string{}
	for _, r := range text {
		current = append(current, r)
		rstr := string(r)
		octets += len(rstr)
		quoted += qLength(rstr)
		n := quoted
		if b64 {
			n = base64.StdEncoding.EncodedLen(octets)
		}
		if n > maxlen {
			current = current[:len(current)-1]
			if len(encodedLines) == 0 && len(current) == 0 {
				encodedLines = append(encodedLines, nil)
			} else {
				line := encodedWord(string(current), b64)
				encodedLines = append(encodedLines, &line)
			}
			current = []rune{r}
			octets = len(rstr)
			quoted = qLength(rstr)
			maxlen = f.max - 1 - 12
		}
	}
	line := encodedWord(string(current), b64)
	encodedLines = append(encodedLines, &line)
	first := encodedLines[0]
	encodedLines = encodedLines[1:]
	if first != nil {
		f.appendChunk(ws, *first)
	}
	if len(encodedLines) == 0 {
		return
	}
	last := encodedLines[len(encodedLines)-1]
	encodedLines = encodedLines[:len(encodedLines)-1]
	f.newline()
	f.parts = append(f.parts, chunk{" ", *last})
	for _, line := range encodedLines {
		f.lines = append(f.lines, " "+*line)
	}
}
func encodeHeaderRaw(name, s string, alwaysUTF8 bool, maximum int) (string, error) {
	encoded := alwaysUTF8 || !ascii(s)
	initial := len(name) + 2
	if alwaysUTF8 {
		initial = 0
	}
	if maximum == 0 {
		maximum = 1000000
	}
	f := formatter{initial: initial, max: maximum}
	lines := splitLines(s)
	if len(lines) == 0 {
		f.feed("", "", encoded)
	} else {
		f.feed("", lines[0], encoded)
	}
	for index := 1; index < len(lines); index++ {
		line := lines[index]
		f.newline()
		if encoded {
			f.feed(" ", " "+lstrip(line), true)
		} else {
			trim := lstrip(line)
			f.feed(line[:len(line)-len(trim)], trim, false)
		}
	}
	if len(lines) > 1 {
		f.newline()
	}
	f.parts = append(f.parts, chunk{" ", ""})
	f.newline()
	value := strings.Join(f.lines, "\n")
	if embeddedHeader.MatchString(value) {
		return "", failure("HeaderParseError", "mime_serialize")
	}
	return value, nil
}

func qLength(s string) int {
	n := 0
	for _, c := range []byte(s) {
		if c == ' ' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-!*+/", rune(c)) {
			n++
		} else {
			n += 3
		}
	}
	return n
}

var embeddedHeader = regexp.MustCompile(`\n[^ \t]+:`)
var newlineWithoutFWS = regexp.MustCompile(`\r\n[^ \t]|\r[^ \n\t]|\n[^ \t]`)

func encodeHeader(name, s string, alwaysUTF8 bool) (string, error) {
	value, e := encodeHeaderRaw(name, s, alwaysUTF8, 0)
	if e != nil {
		return "", e
	}
	if newlineWithoutFWS.MatchString(value) {
		return "", failure("HeaderWriteError", "mime_serialize")
	}
	return value, nil
}
