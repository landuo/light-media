package autosignin

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"light-media/internal/pyregex"
	"light-media/internal/releasemeta/legacyanime"
)

var ErrHTMLShapeNotReady = errors.New("HTML recovery shape outside verified lxml compatibility")

//go:embed html-data.json
var htmlDataJSON []byte
var htmlData = func() struct {
	Challenge  struct{ Titles, Selectors []string }
	Regexes    map[string][]string
	WordRanges [][2]rune `json:"unicode_word_ranges"`
} {
	var d struct {
		Challenge  struct{ Titles, Selectors []string }
		Regexes    map[string][]string
		WordRanges [][2]rune `json:"unicode_word_ranges"`
	}
	if err := json.Unmarshal(htmlDataJSON, &d); err != nil {
		panic(err)
	}
	return d
}()

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
func allNodes(n *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		out = append(out, n)
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	if n != nil {
		walk(n)
	}
	return out
}
func elementText(n *html.Node) string {
	var b strings.Builder
	for _, c := range allNodes(n) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}
func textPieces(n *html.Node) []string {
	var out []string
	for _, c := range allNodes(n) {
		if c.Type == html.TextNode {
			out = append(out, c.Data)
		}
	}
	return out
}
func pySpace(r rune) bool { return legacyanime.Strip(string(r)) == "" }
func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if pySpace(r) {
			return -1
		}
		return r
	}, s)
}
func normalizeSpace(s string) string {
	var b strings.Builder
	pending := false
	for _, r := range s {
		if pySpace(r) {
			pending = b.Len() > 0
			continue
		}
		if pending {
			b.WriteByte(' ')
			pending = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
func sourceWord(r rune) bool {
	i := sort.Search(len(htmlData.WordRanges), func(i int) bool { return htmlData.WordRanges[i][1] >= r })
	return i < len(htmlData.WordRanges) && htmlData.WordRanges[i][0] <= r
}

// re.IGNORECASE on these source ASCII literals additionally accepts U+0130,
// U+0131, U+017F and U+212A. Do not delegate this to Go Unicode tables.
func pyASCIIInsensitive(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case 'İ', 'ı':
			return 'i'
		case 'ſ':
			return 's'
		case 'K':
			return 'k'
		}
		return r
	}, s)
	return legacyanime.LowerCase(s)
}

var hiddenStyle = regexp.MustCompile(`(?:display[\t\n\r\f ]*:[\t\n\r\f ]*none|visibility[\t\n\r\f ]*:[\t\n\r\f ]*hidden)`)

// HTML5's implicit head/body/tbody are corrected where their identity is
// irrelevant to the source XPath. More complex HTML4 recovery remains explicit,
// rather than claiming that x/net/html is the original libxml2 parser.
func parseHTML(source string) (*html.Node, error) {
	if legacyanime.Strip(source) == "" {
		return nil, nil
	}
	if strings.ContainsRune(source, 0) {
		return nil, ErrHTMLShapeNotReady
	}
	// noscript is ordinary recoverable body content for libxml2, whereas
	// HTML5 moves its non-whitespace text out of the head element.
	parseInput := regexp.MustCompile(`(?i)(</?)noscript([\t\n\r\f >])`).ReplaceAllString(source, "${1}mp-noscript${2}")
	// A comment placeholder preserves libxml's table text position instead
	// of accepting HTML5 foster parenting. The marker is chosen absent from
	// source and restored before any visibility filter examines comments.
	marker := "mp-table-text-"
	for strings.Contains(parseInput, marker) {
		marker += "x"
	}
	tableText := map[string]string{}
	var normalized strings.Builder
	tokenizer := html.NewTokenizer(strings.NewReader(parseInput))
	inTable, inCell := 0, 0
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		raw := string(tokenizer.Raw())
		tok := tokenizer.Token()
		switch kind {
		case html.StartTagToken:
			if tok.Data == "table" {
				inTable++
			}
			if tok.Data == "td" || tok.Data == "th" {
				inCell++
			}
		case html.EndTagToken:
			if tok.Data == "table" && inTable > 0 {
				inTable--
			}
			if (tok.Data == "td" || tok.Data == "th") && inCell > 0 {
				inCell--
			}
		case html.TextToken:
			if inTable > 0 && inCell == 0 && legacyanime.Strip(tok.Data) != "" {
				key := fmt.Sprintf("%s%d", marker, len(tableText))
				tableText[key] = tok.Data
				raw = "<!--" + key + "-->"
			}
		}
		normalized.WriteString(raw)
	}
	parseInput = normalized.String()
	doc, err := html.ParseWithOptions(strings.NewReader(parseInput), html.ParseOptionEnableScripting(false))
	if err != nil {
		return nil, err
	}
	for _, n := range allNodes(doc) {
		if n.Type == html.CommentNode {
			if text, ok := tableText[n.Data]; ok {
				n.Type = html.TextNode
				n.Data = text
			}
		}
	}
	meaningful := false
	for _, n := range allNodes(doc) {
		if n.Type == html.ElementNode && n.Data == "mp-noscript" {
			n.Data = "noscript"
		}
		if n.Type == html.TextNode && legacyanime.Strip(n.Data) != "" {
			meaningful = true
		}
		if n.Type == html.ElementNode && n.Data != "html" && n.Data != "head" && n.Data != "body" {
			meaningful = true
		}
	}
	if !meaningful && !strings.Contains(strings.ToLower(source), "<html") && !strings.Contains(strings.ToLower(source), "<body") && !strings.Contains(strings.ToLower(source), "<head") {
		return nil, nil
	}
	var root, head, body *html.Node
	for _, n := range allNodes(doc) {
		if n.Type != html.ElementNode {
			continue
		}
		switch n.Data {
		case "html":
			if root == nil {
				root = n
			}
		case "head":
			head = n
		case "body":
			body = n
		}
	}
	if root == nil {
		return nil, nil
	}
	if head != nil && body != nil {
		// lxml treats these elements as body content when no explicit head exists.
		if !strings.Contains(strings.ToLower(source), "<head") {
			for c := head.FirstChild; c != nil; {
				next := c.NextSibling
				if c.Type == html.ElementNode && (c.Data == "template" || c.Data == "noscript") {
					head.RemoveChild(c)
					body.AppendChild(c)
				}
				c = next
			}
		}
		if body.FirstChild == nil && len(body.Attr) == 0 && !strings.Contains(strings.ToLower(source), "<body") {
			root.RemoveChild(body)
		}
		if head.FirstChild == nil && len(head.Attr) == 0 && !strings.Contains(strings.ToLower(source), "<head") {
			root.RemoveChild(head)
		}
	}
	lower := strings.ToLower(source)
	if strings.Contains(lower, "<table") && !strings.Contains(lower, "<tbody") {
		for _, n := range allNodes(root) {
			if n.Type == html.ElementNode && n.Data == "tbody" && n.Parent != nil {
				p := n.Parent
				for n.FirstChild != nil {
					c := n.FirstChild
					n.RemoveChild(c)
					p.InsertBefore(c, n)
				}
				p.RemoveChild(n)
			}
		}
	}
	return root, nil
}
func visibleDocument(source string) (*html.Node, error) {
	root, err := parseHTML(source)
	if err != nil || root == nil {
		return root, err
	}
	for _, n := range allNodes(root) {
		remove := n.Type == html.CommentNode
		if n.Type == html.ElementNode {
			switch n.Data {
			case "head", "script", "style", "template", "noscript", "textarea":
				remove = true
			}
			remove = remove || hasAttr(n, "hidden") || attr(n, "aria-hidden") == "true" || (n.Data == "input" && attr(n, "type") == "hidden")
		}
		if remove && n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
	for _, n := range allNodes(root) {
		if n.Type == html.ElementNode && hasAttr(n, "style") && hiddenStyle.MatchString(pyASCIIInsensitive(mapWhitespace(attr(n, "style")))) && n.Parent != nil {
			p := n.Parent
			tail := n.NextSibling
			p.RemoveChild(n)
			if tail != nil && tail.Type == html.TextNode {
				p.RemoveChild(tail)
			}
		}
	}
	return root, nil
}
func mapWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if pySpace(r) {
			return ' '
		}
		return r
	}, s)
}
func htmlEscape(s string, attribute bool) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	if attribute {
		s = strings.ReplaceAll(s, `"`, "&quot;")
	}
	return s
}

var voidElement = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true}

func serializeHTML(n *html.Node) string {
	var b strings.Builder
	var render func(*html.Node)
	render = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(htmlEscape(n.Data, false))
		case html.ElementNode:
			b.WriteByte('<')
			b.WriteString(n.Data)
			for _, a := range n.Attr {
				b.WriteByte(' ')
				b.WriteString(a.Key)
				b.WriteString(`="`)
				b.WriteString(htmlEscape(a.Val, true))
				b.WriteByte('"')
			}
			b.WriteByte('>')
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				render(c)
			}
			if !voidElement[n.Data] {
				b.WriteString("</")
				b.WriteString(n.Data)
				b.WriteByte('>')
			}
		}
	}
	if n != nil {
		render(n)
	}
	return b.String()
}
func VisibleHTML(source string) (string, error) {
	doc, err := visibleDocument(source)
	if err != nil {
		return "", err
	}
	return serializeHTML(doc), nil
}
func password(doc *html.Node, fold bool) bool {
	for _, n := range allNodes(doc) {
		if n.Type != html.ElementNode || n.Data != "input" {
			continue
		}
		value := attr(n, "type")
		if fold {
			value = strings.Map(func(r rune) rune {
				if strings.ContainsRune("PASSWORD", r) {
					return r + ('a' - 'A')
				}
				return r
			}, value)
		}
		if value == "password" {
			return true
		}
	}
	return false
}
func HasLoginForm(source string) (bool, error) {
	doc, err := visibleDocument(source)
	return doc != nil && password(doc, true), err
}
func legacyLoggedIn(doc *html.Node) bool {
	if doc == nil || doc.FirstChild == nil || password(doc, false) {
		return false
	}
	for _, n := range allNodes(doc) {
		if n.Type != html.ElementNode {
			continue
		}
		if n.Data == "a" {
			for _, k := range []string{"href", "data-url", "onclick", "lay-on"} {
				if strings.Contains(attr(n, k), "logout") {
					return true
				}
			}
			if strings.Contains(attr(n, "href"), "mybonus") || strings.Contains(attr(n, "href"), "usercp") || attr(n, "id") == "myitem" {
				return true
			}
		}
		if n.Data == "form" && strings.Contains(attr(n, "action"), "logout") {
			return true
		}
		if n.Data == "div" && attr(n, "class") == "user-info-side" {
			return true
		}
	}
	return false
}
func logoutWord(s string) bool {
	runes := []rune(s)
	for i := range runes {
		if i > 0 && sourceWord(runes[i-1]) {
			continue
		}
		for _, word := range []string{"logout", "logoff", "signout"} {
			length := len(word)
			if len(runes)-i < length {
				continue
			}
			if pyASCIIInsensitive(string(runes[i:i+length])) == word && (len(runes) == i+length || !sourceWord(runes[i+length])) {
				return true
			}
		}
	}
	return false
}
func HasLoginEvidence(source string) (bool, error) {
	doc, err := visibleDocument(source)
	if err != nil {
		return false, err
	}
	if doc == nil || password(doc, true) {
		return false, nil
	}
	if legacyLoggedIn(doc) {
		return true, nil
	}
	for _, n := range allNodes(doc) {
		if n.Type != html.ElementNode {
			continue
		}
		if n.Data != "a" && n.Data != "button" && n.Data != "form" && n.Data != "shark-icon-button" && attr(n, "role") != "button" {
			continue
		}
		for _, k := range []string{"href", "action", "formaction", "data-url", "onclick", "lay-on"} {
			if logoutWord(attr(n, k)) {
				return true, nil
			}
		}
		if n.Data != "form" {
			switch legacyanime.LowerCase(compact(elementText(n))) {
			case "退出登录", "退出登錄", "登出", "logout", "signout":
				return true, nil
			}
		}
	}
	return false, nil
}

var negativeSignin = regexp.MustCompile(`(?:签到|簽到)(?:失败|失敗|未成功)|(?:未|没有|沒有)(?:签到|簽到)成功`)
var notSigned = regexp.MustCompile(`^(?:您|你)?(?:今天|今日)?(?:尚未|还未|還未|未|没有|沒有)(?:签到|簽到)[！!。.]?$`)
var signed = regexp.MustCompile(`^(?:签到成功|簽到成功|(?:今日|今天)?(?:已签到|已簽到|已经签到|已經簽到)|已经打卡)[！!。.]?$`)
var reward = regexp.MustCompile(`(?:您|你)(?:今天|今日)(?:已(?:经)?签(?:到|过到)|已經簽到)(?:过|過|了)|(?:本次|此次)(?:签到|簽到)(?:您)?(?:获得|獲得)(?:了)?` + sourceDecimalClass + `|(?:签到|簽到)已得` + sourceDecimalClass + `|(?:今天|今日)(?:签到|簽到)(?:您|你)?(?:获得|獲得)(?:了)?` + sourceDecimalClass + `+(?:点|點)魔力值`)
var englishSigned = regexp.MustCompile(`you have already attend, no refresh please\.|you have already attended ` + sourceDecimalClass + `+ days, continuous ` + sourceDecimalClass + `+ days, this time you will get ` + sourceDecimalClass + `+ bonus\.`)

func HasSigninEvidence(source string) (bool, error) {
	doc, err := visibleDocument(source)
	if err != nil {
		return false, err
	}
	if doc == nil || password(doc, false) {
		return false, nil
	}
	text := normalizeSpace(strings.Join(textPieces(doc), " "))
	if negativeSignin.MatchString(compact(text)) {
		return false, nil
	}
	nodes := allNodes(doc)
	for _, n := range nodes {
		if n.Type != html.ElementNode || !(n.Data == "button" || n.Data == "input" && attr(n, "type") == "submit") {
			continue
		}
		label := attr(n, "value")
		if label == "" {
			label = elementText(n)
		}
		switch legacyanime.LowerCase(compact(label)) {
		case "签到", "簽到", "立即签到", "立即簽到", "每日签到", "打卡", "checkin", "checkinnow":
			return false, nil
		}
	}
	for _, n := range nodes {
		if n.Type == html.ElementNode && notSigned.MatchString(compact(elementText(n))) {
			return false, nil
		}
	}
	for _, n := range nodes {
		if n.Type != html.ElementNode {
			continue
		}
		if n.Data == "input" {
			kind := legacyanime.LowerCase(attr(n, "type"))
			if kind != "button" && kind != "submit" {
				continue
			}
		}
		label := attr(n, "value")
		if label == "" {
			label = elementText(n)
		}
		if signed.MatchString(compact(label)) {
			return true, nil
		}
	}
	return reward.MatchString(compact(text)) || englishSigned.MatchString(pyASCIIInsensitive(text)), nil
}
func UnderChallenge(source string) (bool, error) {
	if source == "" {
		return false, nil
	}
	tokenCheck := html.NewTokenizer(strings.NewReader(source))
	onlyComment, commentSeen := true, false
	for {
		kind := tokenCheck.Next()
		if kind == html.ErrorToken {
			break
		}
		tok := tokenCheck.Token()
		if kind == html.CommentToken {
			commentSeen = true
		} else if kind != html.TextToken || legacyanime.Strip(tok.Data) != "" {
			onlyComment = false
		}
	}
	if onlyComment && commentSeen {
		return false, errors.New("Document is empty")
	}

	doc, err := parseHTML(source)
	if err != nil {
		return false, err
	}
	var titles []string
	for _, n := range allNodes(doc) {
		if n.Type == html.ElementNode && n.Data == "title" {
			titles = append(titles, normalizeSpace(elementText(n)))
		}
	}
	title := strings.Join(titles, " ")
	for _, known := range htmlData.Challenge.Titles {
		if legacyanime.LowerCase(title) == legacyanime.LowerCase(known) {
			return true, nil
		}
	}
	for _, n := range allNodes(doc) {
		if n.Type != html.ElementNode {
			continue
		}
		switch attr(n, "id") {
		case "cf-challenge-running", "cf-please-wait", "challenge-spinner", "trk_jschal_js":
			return true, nil
		case "js_info":
			for p := n.Parent; p != nil; p = p.Parent {
				if p.Data == "td" && hasClass(p, "info") {
					return true, nil
				}
			}
		}
		if hasClass(n, "ray_id") || hasClass(n, "attack-box") {
			return true, nil
		}
		if n.Data == "h2" {
			for p := n.Parent; p != nil; p = p.Parent {
				if p.Data == "div" && hasClass(p, "text-box") {
					for q := p.Parent; q != nil; q = q.Parent {
						if q.Data == "div" && hasClass(q, "vc") {
							return true, nil
						}
					}
				}
			}
		}
	}
	return false, nil
}
func hasClass(n *html.Node, name string) bool {
	for _, x := range strings.FieldsFunc(attr(n, "class"), func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
		if x == name {
			return true
		}
	}
	return false
}

// All installed constant regexes use only the pinned decimal class, fixed
// lookaround and ASCII syntax. Arbitrary worker expressions are not accepted.
func SignInResult(source string, patterns []string) (bool, error) {
	s, err := VisibleHTML(source)
	if err != nil {
		return false, err
	}
	digits := regexp.MustCompile(sourceDecimalClass + `+px`)
	hash := regexp.MustCompile(`#` + sourceDecimalClass + `+`)
	s = hash.ReplaceAllString(digits.ReplaceAllString(s, ""), "")
	doc, err := parseHTML(s)
	if err != nil {
		return false, err
	}
	text := elementText(doc)
	if negativeSignin.MatchString(text) {
		return false, nil
	}
	for _, pattern := range patterns {
		known := false
		for _, rs := range htmlData.Regexes {
			for _, r := range rs {
				if pattern == r {
					known = true
				}
			}
		}
		if !known {
			return false, ErrRetryPatternNotReady
		}
		target := text
		if strings.Contains(pattern, "<") || strings.Contains(pattern, "value=") {
			target = s
		}
		pattern = strings.ReplaceAll(pattern, `\d`, sourceDecimalClass)
		compiled, err := pyregex.Compile(pattern, false)
		if err != nil {
			return false, err
		}
		matched, err := compiled.MatchString(target)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// Keep io/bytes references near the bounded document reader used by transport.
func readDocument(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("site document exceeds %d bytes", limit)
	}
	return bytes.Clone(data), nil
}
