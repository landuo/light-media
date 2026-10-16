package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
)

const (
	AdapterHares    FixedAdapter = "Hares"
	AdapterPTerClub FixedAdapter = "PTerClub"
	AdapterRousiPro FixedAdapter = "RousiPro"
	AdapterYemaPT   FixedAdapter = "YemaPT"
	AdapterZhuQue   FixedAdapter = "ZhuQue"
)

func init() {
	fixedPolicies[AdapterHares] = fixedPolicy{SiteURL: "club.hares.top", RegisteredHosts: []string{"club.hares.top", "www.club.hares.top"}, Routes: []fixedRoute{{"GET", "https://club.hares.top"}, {"GET", "https://club.hares.top/attendance.php?action=sign"}}}
	fixedPolicies[AdapterPTerClub] = fixedPolicy{SiteURL: "pterclub.com", RegisteredHosts: []string{"pterclub.com", "www.pterclub.com"}, Routes: []fixedRoute{{"GET", "https://pterclub.com/attendance-ajax.php"}}}
	fixedPolicies[AdapterRousiPro] = fixedPolicy{SiteURL: "rousi.pro", RegisteredHosts: []string{"rousi.pro", "www.rousi.pro"}, Routes: []fixedRoute{{"POST", "https://rousi.pro/api/points/attendance"}, {"GET", "https://rousi.pro/api/v1/profile"}, {"GET", "https://rousi.pro/api/points/attendance/stats"}}}
	fixedPolicies[AdapterYemaPT] = fixedPolicy{SiteURL: "yemapt.org", RegisteredHosts: []string{"yemapt.org", "www.yemapt.org"}}
	fixedPolicies[AdapterZhuQue] = fixedPolicy{SiteURL: "zhuque.in", RegisteredHosts: []string{"zhuque.in", "www.zhuque.in"}, Routes: []fixedRoute{{"GET", "https://zhuque.in"}, {"POST", "https://zhuque.in/api/gaming/fireGenshinCharacterMagic"}}}
}

var ErrJSONSyntaxNotReady = errors.New("site JSON error shape outside verified Python compatibility")

func jsonPositionError(kind, text string, byteOffset int) error {
	if byteOffset < 0 {
		byteOffset = 0
	}
	if byteOffset > len(text) {
		byteOffset = len(text)
	}
	prefix := text[:byteOffset]
	line := strings.Count(prefix, "\n") + 1
	last := strings.LastIndex(prefix, "\n")
	column := utf8.RuneCountInString(prefix[last+1:]) + 1
	return fmt.Errorf("%s: line %d column %d (char %d)", kind, line, column, utf8.RuneCountInString(prefix))
}
func parseSiteJSON(text string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		if strings.HasPrefix(text, "\ufeff") {
			return nil, jsonPositionError("Unexpected UTF-8 BOM (decode using utf-8-sig)", text, 0)
		}
		position := 0
		for position < len(text) && strings.ContainsRune(" \t\r\n", rune(text[position])) {
			position++
		}
		invalidLiteral := false
		if position < len(text) {
			switch text[position] {
			case 't':
				invalidLiteral = !strings.HasPrefix(text[position:], "true")
			case 'f':
				invalidLiteral = !strings.HasPrefix(text[position:], "false")
			case 'n':
				invalidLiteral = !strings.HasPrefix(text[position:], "null")
			case '-':
				invalidLiteral = position+1 == len(text) || text[position+1] < '0' || text[position+1] > '9'
			}
		}
		if position == len(text) || invalidLiteral || !strings.ContainsRune(`{["-0123456789tfn`, rune(text[position])) {
			return nil, jsonPositionError("Expecting value", text, position)
		}
		return nil, ErrJSONSyntaxNotReady
	}
	offset := int(decoder.InputOffset())
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		for offset < len(text) && strings.ContainsRune(" \t\r\n", rune(text[offset])) {
			offset++
		}
		return nil, jsonPositionError("Extra data", text, offset)
	}
	return value, nil
}
func jsonObject(value any) (Object, error) {
	o, ok := value.(Object)
	if !ok {
		return nil, fmt.Errorf("'%s' object has no attribute 'get'", typeName(value))
	}
	return o, nil
}
func responseObject(response *PageResponse) (Object, error) {
	v, err := parseSiteJSON(response.Text)
	if err != nil {
		return nil, err
	}
	return jsonObject(v)
}
func strictCode(value any, want int) bool {
	if typeName(value) != "int" {
		return false
	}
	return equal(value, want)
}
func authString(value any) string {
	if !Truth(value) {
		return ""
	}
	return legacyanime.Strip(pythonScalar(value))
}
func bearer(value string) string {
	value = legacyanime.Strip(value)
	if strings.HasPrefix(legacyanime.LowerCase(value), "bearer ") {
		return value
	}
	return "Bearer " + value
}
func anySigninEvidence(value any) (bool, error) {
	if !Truth(value) {
		return false, nil
	}
	s, ok := value.(string)
	if !ok {
		return false, errors.New("HTML evidence type outside verified compatibility")
	}
	return HasSigninEvidence(s)
}
func getHTTP(ctx context.Context, h SiteVisitHooks, r PageRequest) (*PageResponse, error) {
	if h.HTTP == nil {
		return nil, ErrAdapterNotReady
	}
	return h.HTTP.Do(ctx, r)
}
func RousiCode(response *PageResponse) int {
	if response == nil {
		return -1
	}
	o, err := responseObject(response)
	if err != nil {
		return -1
	}
	value := o["code"]
	if typeName(value) != "int" {
		return -1
	}
	number, err := sourceInteger(value)
	if err != nil || !number.IsInt64() {
		return -1
	}
	return int(number.Int64())
}
func rousiAlready(response *PageResponse) (bool, error) {
	if response == nil || response.Status != 400 || RousiCode(response) != 1 {
		return false, nil
	}
	o, err := responseObject(response)
	if err != nil {
		return false, err
	}
	message := o["message"]
	if !Truth(message) {
		message = o["msg"]
	}
	s, ok := message.(string)
	if !ok {
		return false, nil
	}
	return HasSigninEvidence(s)
}
func RousiVisit(ctx context.Context, site Object, h SiteVisitHooks, login bool) (AdapterResult, error) {
	action := "签到"
	if login {
		action = "模拟登录"
	}
	fail := func(message string) (AdapterResult, error) { return AdapterResult{false, message}, nil }
	key, token := authString(site["apikey"]), authString(site["token"])
	if key == "" && token == "" {
		return fail(action + "失败，缺少个人 API Key 或兼容 Authorization 信息")
	}
	base := Object{"User-Agent": site["ua"], "Accept": "application/json, text/plain, */*"}
	if !login {
		base["Content-Type"] = "application/json"
	}
	var response *PageResponse
	authType := "个人 API Key"
	call := func(auth string, personal bool) (*PageResponse, error) {
		headers := clone(base).(Object)
		method, address := "POST", "https://rousi.pro/api/points/attendance"
		var body any = Object{"mode": "fixed"}
		if login {
			method = "GET"
			body = nil
			if personal {
				address = "https://rousi.pro/api/v1/profile"
			} else {
				address = "https://rousi.pro/api/points/attendance/stats"
			}
			headers["Authorization"] = bearer(auth)
		} else if personal {
			headers["api-token"] = auth
		} else {
			headers["Authorization"] = bearer(auth)
		}
		return getHTTP(ctx, h, PageRequest{Method: method, URL: address, Headers: headers, NoCookies: true, JSON: body, Timeout: site["timeout"]})
	}
	var err error
	if key != "" {
		response, err = call(key, true)
		if err != nil {
			return AdapterResult{}, err
		}
		if response != nil && response.Status == 200 && RousiCode(response) == 0 {
			return AdapterResult{true, action + "成功"}, nil
		}
		if !login {
			already, err := rousiAlready(response)
			if err != nil {
				return AdapterResult{}, err
			}
			if already {
				return AdapterResult{true, "今日已签到"}, nil
			}
		}
	}
	if token != "" {
		authType = "Authorization"
		response, err = call(token, false)
		if err != nil {
			return AdapterResult{}, err
		}
	}
	if response != nil && response.Status == 200 && RousiCode(response) == 0 {
		return AdapterResult{true, action + "成功"}, nil
	}
	if !login {
		already, err := rousiAlready(response)
		if err != nil {
			return AdapterResult{}, err
		}
		if already {
			return AdapterResult{true, "今日已签到"}, nil
		}
	}
	if response != nil && (response.Status == 401 || response.Status == 403) {
		return fail(action + "失败，" + authType + " 已失效或权限不足")
	}
	if response != nil {
		return fail(fmt.Sprintf("%s失败，状态码：%d", action, response.Status))
	}
	return fail(action + "失败，无法访问网站")
}
func JSONSignin(kind FixedAdapter, ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	if kind == AdapterRousiPro {
		return RousiVisit(ctx, site, h, false)
	}
	if kind == AdapterYemaPT {
		return YemaVisit(ctx, site, h, false)
	}
	fail := func(message string) (AdapterResult, error) { return AdapterResult{false, message}, nil }
	routes := fixedPolicies[kind].Routes
	page, err := BasePageSource(ctx, site, routes[0].URL, h)
	if err != nil {
		return AdapterResult{}, err
	}
	prefix := "签到"
	if kind == AdapterHares || kind == AdapterZhuQue {
		prefix = "模拟访问"
		if kind == AdapterZhuQue {
			prefix = "模拟登录"
		}
	}
	if page == "" {
		return fail(prefix + "失败，请检查站点连通性")
	}
	if strings.Contains(page, "login.php") {
		return fail(prefix + "失败，Cookie已失效")
	}
	if kind == AdapterPTerClub {
		v, err := parseSiteJSON(page)
		if err != nil {
			return fail("签到失败，签到接口返回数据异常")
		}
		o, err := jsonObject(v)
		if err != nil {
			return AdapterResult{}, err
		}
		if o["status"] == "1" {
			return AdapterResult{true, "签到成功"}, nil
		}
		if o["status"] == "0" {
			yes, err := anySigninEvidence(o["message"])
			if err != nil {
				return AdapterResult{}, err
			}
			if yes {
				return AdapterResult{true, "今日已签到"}, nil
			}
		}
		return fail("签到失败，签到接口未返回明确的成功或重复签到结果")
	}
	if kind == AdapterHares {
		response, err := getHTTP(ctx, h, PageRequest{Method: "GET", URL: routes[1].URL, Headers: Object{"Accept": "application/json", "User-Agent": site["ua"]}, Timeout: site["timeout"]})
		if err != nil {
			return AdapterResult{}, err
		}
		if response == nil || response.Status != 200 {
			return fail("签到失败，签到接口请求失败")
		}
		o, err := responseObject(response)
		if err != nil {
			return AdapterResult{}, err
		}
		if strictCode(o["code"], 0) {
			return AdapterResult{true, "签到成功"}, nil
		}
		if equal(o["code"], 1) {
			yes, err := anySigninEvidence(o["msg"])
			if err != nil {
				return AdapterResult{}, err
			}
			if yes {
				return AdapterResult{true, "今日已签到"}, nil
			}
		}
		return fail("签到失败，签到接口未返回明确的成功或重复签到结果")
	}
	if kind == AdapterZhuQue {
		doc, err := parseHTML(page)
		if err != nil {
			return AdapterResult{}, err
		}
		if doc == nil || doc.FirstChild == nil {
			return fail("模拟登录失败")
		}
		csrf := ""
		found := false
		for _, n := range allNodes(doc) {
			if n.Data == "meta" && attr(n, "name") == "x-csrf-token" && hasAttr(n, "content") {
				csrf = attr(n, "content")
				found = true
				break
			}
		}
		if !found {
			return AdapterResult{}, errors.New("list index out of range")
		}
		message := "失败"
		if csrf != "" {
			response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: routes[1].URL, Headers: Object{"x-csrf-token": csrf, "Content-Type": "application/json; charset=utf-8", "User-Agent": site["ua"]}, JSON: Object{"all": 1, "resetModal": "true"}, Timeout: site["timeout"]})
			if err != nil {
				return AdapterResult{}, err
			}
			if response == nil {
				return AdapterResult{}, errors.New("'NoneType' object has no attribute 'text'")
			}
			o, err := responseObject(response)
			if err != nil {
				return AdapterResult{}, err
			}
			status, ok := o["status"]
			if !ok {
				return AdapterResult{}, errors.New("'status'")
			}
			if equal(status, 200) {
				data, ok := o["data"].(Object)
				if !ok {
					return AdapterResult{}, errors.New("invalid skill data")
				}
				bonus, ok := data["bonus"]
				if !ok {
					return AdapterResult{}, errors.New("'bonus'")
				}
				n, err := sourceInteger(bonus)
				if err != nil {
					return AdapterResult{}, err
				}
				message = "成功，获得" + n.String() + "魔力"
			}
		}
		return AdapterResult{true, "模拟登录成功，技能释放" + message}, nil
	}
	return AdapterResult{}, ErrAdapterNotReady
}
func YemaVisit(ctx context.Context, site Object, h SiteVisitHooks, login bool) (AdapterResult, error) {
	path := "api/consumer/checkIn"
	action := "签到"
	if login {
		path = "api/user/profile"
		action = "模拟登录"
	}
	address, err := joinedURL(textValue(site["url"]), path)
	if err != nil {
		return AdapterResult{}, err
	}
	response, err := getHTTP(ctx, h, PageRequest{Method: "GET", URL: address, Referer: textValue(site["url"]), Headers: Object{"Content-Type": "application/json", "User-Agent": site["ua"], "Accept": "application/json, text/plain, */*"}, Timeout: site["timeout"]})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil {
		return AdapterResult{false, action + "失败，无法打开网站"}, nil
	}
	if login && response.Status != 200 {
		return AdapterResult{false, fmt.Sprintf("模拟登录失败，状态码：%d", response.Status)}, nil
	}
	o, err := responseObject(response)
	if err != nil {
		return AdapterResult{}, err
	}
	if response.Status == 200 && o["success"] == true {
		return AdapterResult{true, action + "成功"}, nil
	}
	if login {
		return AdapterResult{false, fmt.Sprintf("模拟登录失败，状态码：%d", response.Status)}, nil
	}
	return AdapterResult{false, "签到失败，签到结果：" + pythonScalar(o["errorMessage"])}, nil
}
