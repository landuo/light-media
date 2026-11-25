package autosignin

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	AdapterPt52    FixedAdapter = "Pt52"
	AdapterCHDBits FixedAdapter = "CHDBits"
	AdapterU2      FixedAdapter = "U2"
)

func init() {
	fixedPolicies[AdapterPt52] = fixedPolicy{SiteURL: "52pt.site", RegisteredHosts: []string{"52pt.site", "www.52pt.site"}, Routes: []fixedRoute{{"GET", "https://52pt.site/bakatest.php"}, {"POST", "https://52pt.site/bakatest.php"}}}
	fixedPolicies[AdapterCHDBits] = fixedPolicy{SiteURL: "ptchdbits.co", RegisteredHosts: []string{"ptchdbits.co", "www.ptchdbits.co"}, Routes: []fixedRoute{{"GET", "https://ptchdbits.co/bakatest.php"}, {"POST", "https://ptchdbits.co/bakatest.php"}}}
	fixedPolicies[AdapterU2] = fixedPolicy{SiteURL: "u2.dmhy.org", RegisteredHosts: []string{"u2.dmhy.org", "www.u2.dmhy.org"}, Routes: []fixedRoute{{"GET", "https://u2.dmhy.org/showup.php"}, {"POST", "https://u2.dmhy.org/showup.php?action=show"}}}
}
func hasAncestor(n *html.Node, tag string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == tag {
			return true
		}
	}
	return false
}
func inputValues(doc *html.Node, name string, form bool) []string {
	out := []string{}
	for _, n := range allNodes(doc) {
		if n.Type == html.ElementNode && n.Data == "input" && attr(n, "name") == name && hasAttr(n, "value") && (!form || hasAncestor(n, "form")) {
			out = append(out, attr(n, "value"))
		}
	}
	return out
}
func defaultChoose(minimum, maximum int) (int, error) {
	if maximum < minimum {
		return 0, errors.New("empty range in randrange(0, 0)")
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(maximum-minimum+1)))
	if err != nil {
		return 0, err
	}
	return minimum + int(v.Int64()), nil
}
func chooseAnswer(h SiteVisitHooks, minimum, maximum int) (int, error) {
	fn := h.Choose
	if fn == nil {
		fn = defaultChoose
	}
	return fn(minimum, maximum)
}

var quizQuestion = regexp.MustCompile(`请问：.+`)

func QuizSignin(kind FixedAdapter, ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) {
		if message != "签到失败" && message != "签到失败，未确认签到结果" && !strings.HasPrefix(message, "【") {
			siteLog(h, "ERROR", pythonScalar(site["name"])+" "+message)
		}
		return AdapterResult{false, message}, nil
	}
	routes := fixedPolicies[kind].Routes
	page, err := sourceBasePageValue(ctx, site, routes[0].URL, h)
	if err != nil {
		return AdapterResult{}, err
	}
	if !Truth(page) {
		return fail("签到失败，请检查站点连通性")
	}
	expired, err := sourcePageContains(page, "login.php")
	if err != nil {
		return AdapterResult{}, err
	}
	if expired {
		return fail("签到失败，Cookie已失效")
	}
	yes, err := sourcePageSignInResult(page, htmlData.Regexes[string(kind)+"._sign_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if yes {
		message := "今日已签到"
		if kind != AdapterPt52 {
			message = pythonScalar(site["name"]) + " " + message
		}
		siteLog(h, "INFO", message)
		return AdapterResult{true, "今日已签到"}, nil
	}
	doc, err := sourcePageParseHTML(page)
	if err != nil {
		return AdapterResult{}, err
	}
	if doc == nil || doc.FirstChild == nil {
		return fail("签到失败")
	}
	questionIDs := inputValues(doc, "questionid", false)
	if len(questionIDs) == 0 {
		return AdapterResult{}, errors.New("list index out of range")
	}
	options := inputValues(doc, "choice[]", false)
	question := ""
	found := false
	for _, n := range allNodes(doc) {
		if n.Type != html.ElementNode || n.Data != "td" || attr(n, "class") != "text" {
			continue
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				if strings.Contains(c.Data, "请问：") {
					question = c.Data
					found = true
				}
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return AdapterResult{}, errors.New("list index out of range")
	}
	if !quizQuestion.MatchString(question) {
		siteLog(h, "ERROR", "未获取到签到问题")
		return fail("【" + pythonScalar(site["name"]) + "】签到失败，未获取到签到问题")
	}
	siteLog(h, "DEBUG", "获取到签到问题 "+strings.TrimPrefix(quizQuestion.FindString(question), "请问："))
	choice, err := chooseAnswer(h, 0, len(options)-1)
	if err != nil {
		return AdapterResult{}, err
	}
	if choice < 0 || choice >= len(options) {
		return AdapterResult{}, errors.New("list index out of range")
	}
	timeout := any(nil)
	if kind == AdapterPt52 {
		timeout = site["timeout"]
	}
	data := map[string]string{"questionid": questionIDs[0], "choice[]": options[choice], "usercomment": "太难了！", "wantskip": "不会"}
	order := []string{"questionid", "choice[]", "usercomment", "wantskip"}
	siteLog(h, "DEBUG", "签到请求参数 "+pythonFormRepr(data, order))
	response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: routes[1].URL, Form: data, FormOrder: order, Timeout: timeout})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		return fail("签到失败，签到接口请求失败")
	}
	yes, err = SignInResult(response.text(), htmlData.Regexes[string(kind)+"._success_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if yes {
		checking := clone(site).(Object)
		checking["render"] = false
		if kind == AdapterCHDBits {
			checking["timeout"] = nil
		}
		page, err = sourceBasePageValue(ctx, checking, routes[0].URL, h)
		if err != nil {
			return AdapterResult{}, err
		}
		confirmed, err := sourcePageSignInResult(page, htmlData.Regexes[string(kind)+"._sign_regex"])
		if err != nil {
			return AdapterResult{}, err
		}
		if confirmed {
			siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
			return AdapterResult{true, "签到成功"}, nil
		}
		return fail("签到失败，未确认签到结果")
	}
	yes, err = SignInResult(response.text(), htmlData.Regexes[string(kind)+"._sign_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if yes {
		siteLog(h, "INFO", pythonScalar(site["name"])+" 今日已签到")
		return AdapterResult{true, "今日已签到"}, nil
	}
	return fail("签到失败，请到页面查看")
}

const pyWhitespaceClass = `[\x{0009}-\x{000d}\x{001c}-\x{0020}\x{0085}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]`

var u2Redirect = regexp.MustCompile(`window` + pyWhitespaceClass + `*\.` + pyWhitespaceClass + `*location` + pyWhitespaceClass + `*\.` + pyWhitespaceClass + `*href` + pyWhitespaceClass + `*=` + pyWhitespaceClass + `*['"]([^'"]+)['"]`)

func U2SuccessResponse(text string) bool {
	m := u2Redirect.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	address, err := joinedURL("https://u2.dmhy.org/showup.php", m[1])
	if err != nil {
		return false
	}
	u, err := url.Parse(address)
	return err == nil && u.User == nil && strings.ToLower(u.Host) == "u2.dmhy.org" && u.EscapedPath() == "/showup.php"
}
func U2SignedState(page string) (bool, error) { return sourceU2SignedState(page) }
func sourceU2SignedState(page any) (bool, error) {
	if !Truth(page) {
		return false, nil
	}
	logged, err := sourcePageLoggedIn(page)
	if err != nil || !logged {
		return false, err
	}
	doc, err := sourcePageParseHTML(page)
	if err != nil {
		return false, err
	}
	for _, n := range allNodes(doc) {
		if n.Data == "input" && attr(n, "name") == "req" || n.Data == "form" && strings.Contains(attr(n, "action"), "showup.php") {
			return false, nil
		}
	}
	return sourcePageSignInResult(page, htmlData.Regexes["U2._sign_regex"])
}
func U2Signin(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) {
		if message != "签到失败" {
			siteLog(h, "ERROR", pythonScalar(site["name"])+" "+message)
		}
		return AdapterResult{false, message}, nil
	}
	now := h.Now
	if now == nil {
		now = time.Now
	}
	if now().Hour() < 9 {
		return fail("签到失败，9点前不签到")
	}
	routes := fixedPolicies[AdapterU2].Routes
	page, err := sourceBasePageValue(ctx, site, routes[0].URL, h)
	if err != nil {
		return AdapterResult{}, err
	}
	if !Truth(page) {
		return fail("签到失败，请检查站点连通性")
	}
	logged, err := sourcePageLoggedIn(page)
	if err != nil {
		return AdapterResult{}, err
	}
	if !logged {
		return fail("签到失败，Cookie已失效")
	}
	yes, err := sourceU2SignedState(page)
	if err != nil {
		return AdapterResult{}, err
	}
	if yes {
		siteLog(h, "INFO", pythonScalar(site["name"])+" 今日已签到")
		return AdapterResult{true, "今日已签到"}, nil
	}
	doc, err := sourcePageParseHTML(page)
	if err != nil {
		return AdapterResult{}, err
	}
	if doc == nil {
		return fail("签到失败")
	}
	req, hash, form, csrf := inputValues(doc, "req", true), inputValues(doc, "hash", true), inputValues(doc, "form", true), inputValues(doc, "_csrf", true)
	names, values := []string{}, []string{}
	for _, n := range allNodes(doc) {
		if n.Type == html.ElementNode && n.Data == "input" && n.Parent != nil && n.Parent.Data == "td" && hasAncestor(n, "form") && attr(n, "type") == "submit" {
			if hasAttr(n, "name") {
				names = append(names, attr(n, "name"))
			}
			if hasAttr(n, "value") {
				values = append(values, attr(n, "value"))
			}
		}
	}
	if len(req) == 0 || len(hash) == 0 || len(form) == 0 || len(csrf) == 0 || len(names) == 0 || len(values) == 0 {
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，未获取到相关签到参数")
		return fail("签到失败")
	}
	answer, err := chooseAnswer(h, 0, 3)
	if err != nil {
		return AdapterResult{}, err
	}
	if answer < 0 || answer >= len(names) || answer >= len(values) {
		return AdapterResult{}, errors.New("list index out of range")
	}
	data := map[string]string{"req": req[0], "hash": hash[0], "form": form[0], "_csrf": csrf[0], "message": "一切随缘~"}
	data[names[answer]] = values[answer]
	response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: routes[1].URL, Headers: Object{"User-Agent": site["ua"], "Referer": "https://u2.dmhy.org/showup.php", "Origin": "https://u2.dmhy.org"}, Form: data, FormOrder: []string{"req", "hash", "form", "_csrf", "message", names[answer]}})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		return fail("签到失败，签到接口请求失败")
	}
	if U2SuccessResponse(response.text()) {
		page, err = sourceBasePageValue(ctx, site, routes[0].URL, h)
		if err != nil {
			return AdapterResult{}, err
		}
		confirmed, err := sourceU2SignedState(page)
		if err != nil {
			return AdapterResult{}, err
		}
		if confirmed {
			siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
			return AdapterResult{true, "签到成功"}, nil
		}
	}
	return fail("签到失败，未确认签到结果")
}
