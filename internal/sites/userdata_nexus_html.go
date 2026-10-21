package sites

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antchfx/htmlquery"
	"github.com/dlclark/regexp2/v2"
	dateparser "github.com/markusmobius/go-dateparser"
	"golang.org/x/net/html"
	"light-media/internal/pyregex"
	"light-media/internal/releasemeta/legacyanime"
)

// etree.HTML does not insert HTML5 tbody elements. Mark explicit bodies before
// parsing and remove only the bodies introduced by the Go parser. Bare AJAX
// table rows are parsed in a temporary table context, then unwrapped.
func sourceNexusDOM(raw string) (*html.Node, error) {
	if legacyanime.Strip(raw) == "" {
		return nil, nil
	}
	marker := "data-light-media-source-body"
	for strings.Contains(raw, marker) {
		marker += "x"
	}
	fragment := strings.Contains(strings.ToLower(raw), "<tr") && !strings.Contains(strings.ToLower(raw), "<table")
	if fragment {
		raw = "<table " + marker + "=\"fragment\">" + raw + "</table>"
	}
	var input strings.Builder
	z := html.NewTokenizer(strings.NewReader(raw))
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			break
		}
		value := string(z.Raw())
		token := z.Token()
		if kind == html.StartTagToken && token.Data == "tbody" {
			token.Attr = append(token.Attr, html.Attribute{Key: marker, Val: "explicit"})
			value = token.String()
		}
		input.WriteString(value)
	}
	doc, e := html.ParseWithOptions(strings.NewReader(input.String()), html.ParseOptionEnableScripting(false))
	if e != nil {
		return nil, errors.New("site_source_html_invalid")
	}
	for _, n := range find(doc, func(n *html.Node) bool {
		return n.Data == "tbody" || n.Data == "table" && attribute(n, marker) == "fragment"
	}) {
		if attribute(n, marker) == "explicit" {
			attrs := n.Attr[:0]
			for _, a := range n.Attr {
				if a.Key != marker {
					attrs = append(attrs, a)
				}
			}
			n.Attr = attrs
			continue
		}
		if n.Parent != nil {
			p := n.Parent
			for n.FirstChild != nil {
				ch := n.FirstChild
				n.RemoveChild(ch)
				p.InsertBefore(ch, n)
			}
			p.RemoveChild(n)
		}
	}
	return doc, nil
}

var sourceNexusRegex sync.Map

func sourceNexusMatch(pattern, raw string, ignoreCase bool) ([]string, error) {
	key := strconv.FormatBool(ignoreCase) + pattern
	var re *regexp2.Regexp
	if v, ok := sourceNexusRegex.Load(key); ok {
		re = v.(*regexp2.Regexp)
	} else {
		var e error
		re, e = pyregex.Compile(pattern, ignoreCase)
		if e != nil {
			return nil, sourceModuleError(nil)
		}
		re.MatchTimeout = 500 * time.Millisecond
		v, _ := sourceNexusRegex.LoadOrStore(key, re)
		re = v.(*regexp2.Regexp)
	}
	m, e := re.FindStringMatch(raw)
	if e != nil {
		return nil, errors.New("site_source_regex_limit")
	}
	if m == nil {
		return nil, nil
	}
	values := []string{}
	for _, g := range m.Groups() {
		values = append(values, g.String())
	}
	return values, nil
}

func sourceNexusReplace(pattern, raw string) (string, error) {
	re, e := pyregex.Compile(pattern, false)
	if e != nil {
		return "", sourceModuleError(nil)
	}
	re.MatchTimeout = 500 * time.Millisecond
	value, e := re.Replace(raw, "", -1, -1)
	if e != nil {
		return "", errors.New("site_source_regex_limit")
	}
	return value, nil
}

func sourceNexusPrepared(raw string) (string, error) {
	value, e := sourceNexusReplace(`\d+px`, raw)
	if e != nil {
		return "", e
	}
	return sourceNexusReplace(`#\d+`, value)
}

func sourceNexusStringInt(raw string) (int64, bool) {
	value := strings.ReplaceAll(legacyanime.Strip(raw), ",", "")
	if value == "" {
		return 0, false
	}
	n, e := legacyanime.Integer(value)
	if e != nil || !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}

func sourceNexusStringFloat(raw string) (float64, bool) {
	value := strings.ReplaceAll(legacyanime.Strip(raw), ",", "")
	if value == "" {
		return 0, false
	}
	f, e := strconv.ParseFloat(value, 64)
	return f, e == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

var sourceNexusUnitPattern = regexp.MustCompile(`[KMGTPI]*B?`)

func sourceNexusFileSize(raw string) (int64, bool, error) {
	if raw == "" {
		return 0, false, nil
	}
	if legacyanime.IsDigit(raw) {
		n, e := legacyanime.Integer(raw)
		if e != nil || !n.IsInt64() {
			return 0, false, errors.New("site_user_integer_limit")
		}
		return n.Int64(), true, nil
	}
	value := strings.ToUpper(strings.NewReplacer(",", "", " ", "").Replace(raw))
	number := legacyanime.Strip(sourceNexusUnitPattern.ReplaceAllString(value, ""))
	f, e := strconv.ParseFloat(number, 64)
	if e != nil {
		return 0, false, nil
	}
	for _, unit := range []struct {
		first, second string
		power         int
	}{{"PB", "PIB", 5}, {"TB", "TIB", 4}, {"GB", "GIB", 3}, {"MB", "MIB", 2}, {"KB", "KIB", 1}} {
		if strings.Contains(value, unit.first) || strings.Contains(value, unit.second) {
			f *= math.Pow(1024, float64(unit.power))
			break
		}
	}
	f = math.RoundToEven(f)
	if math.IsNaN(f) || math.IsInf(f, 0) || f >= math.MaxInt64 || f < math.MinInt64 {
		return 0, false, errors.New("site_user_integer_limit")
	}
	return int64(f), true, nil
}

func sourceNexusRatio(upload, download int64) float64 {
	if download <= 0 {
		return 0
	}
	// Fixed-point formatting rounds the actual binary float, as Python round
	// with ndigits does; multiplying by 1000 would change halfway cases.
	r, _ := strconv.ParseFloat(strconv.FormatFloat(float64(upload)/float64(download), 'f', 3, 64), 64)
	return r
}

func sourceNexusJoinedAt(raw string, now time.Time) string {
	value, e := dateparser.Parse(&dateparser.Configuration{CurrentTime: now, DefaultTimezone: now.Location()}, raw)
	if e != nil {
		return raw
	}
	return value.Time.Format("2006-01-02 15:04:05")
}

func sourceNexusText(n *html.Node) string {
	if n == nil {
		return ""
	}
	return htmlquery.InnerText(n)
}
