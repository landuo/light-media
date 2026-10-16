package autosignin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	AdapterHDSky  FixedAdapter = "HDSky"
	AdapterOpencd FixedAdapter = "Opencd"
)

func init() {
	fixedPolicies[AdapterHDSky] = fixedPolicy{SiteURL: "hdsky.me", RegisteredHosts: []string{"hdsky.me", "www.hdsky.me"}, Routes: []fixedRoute{{"GET", "https://hdsky.me"}, {"POST", "https://hdsky.me/image_code_ajax.php"}, {"POST", "https://hdsky.me/showup.php"}}}
	fixedPolicies[AdapterOpencd] = fixedPolicy{SiteURL: "open.cd", RegisteredHosts: []string{"open.cd", "www.open.cd"}, Routes: []fixedRoute{{"GET", "https://www.open.cd"}, {"GET", "https://www.open.cd/plugin_sign-in.php"}, {"POST", "https://www.open.cd/plugin_sign-in.php?cmd=signin"}}}
}
func sourceSleep(ctx context.Context, h SiteVisitHooks, seconds int) error {
	if h.Sleep != nil {
		return h.Sleep(ctx, time.Duration(seconds)*time.Second)
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func ocrText(ctx context.Context, h SiteVisitHooks, address string) (any, error) {
	if h.OCR == nil {
		return nil, ErrAdapterNotReady
	}
	return h.OCR(ctx, address)
}
func sourceLength(v any) (int, error) {
	switch n := v.(type) {
	case string:
		return utf8.RuneCountInString(n), nil
	case []any:
		return len(n), nil
	case Object:
		return len(n), nil
	}
	return 0, fmt.Errorf("object of type '%s' has no len()", typeName(v))
}
func matchOCR(ctx context.Context, h SiteVisitHooks, address string) (any, error) {
	var result any
	for times := 0; times <= 3; times++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		result, err = ocrText(ctx, h, address)
		if err != nil {
			return nil, err
		}
		if Truth(result) {
			length, err := sourceLength(result)
			if err != nil {
				return nil, err
			}
			if length == 6 {
				break
			}
		}
		if err = sourceSleep(ctx, h, 1); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func specificAncestor(n *html.Node, tag, key, value string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Data == tag && attr(p, key) == value {
			return true
		}
	}
	return false
}
func OCRSignin(kind FixedAdapter, ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) { return AdapterResult{false, message}, nil }
	routes := fixedPolicies[kind].Routes
	page, err := BasePageSource(ctx, site, routes[0].URL, h)
	if err != nil {
		return AdapterResult{}, err
	}
	if page == "" {
		return fail("签到失败，请检查站点连通性")
	}
	if strings.Contains(page, "login.php") {
		return fail("签到失败，Cookie已失效")
	}
	var signed bool
	if kind == AdapterHDSky {
		signed, err = SignInResult(page, htmlData.Regexes["HDSky._sign_regex"])
	} else {
		signed, err = HasSigninEvidence(page)
	}
	if err != nil {
		return AdapterResult{}, err
	}
	if signed {
		return AdapterResult{true, "今日已签到"}, nil
	}
	var imageURL string
	var imageHash any
	if kind == AdapterHDSky {
		attempts := 0
		for !Truth(imageHash) && attempts <= 3 {
			if err := ctx.Err(); err != nil {
				return AdapterResult{}, err
			}
			response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: routes[1].URL, Referer: "https://hdsky.me/index.php", AcceptType: "*/*", Form: map[string]string{"action": "new"}, Timeout: site["timeout"]})
			if err != nil {
				return AdapterResult{}, err
			}
			if response != nil && response.Status == 200 {
				o, err := responseObject(response)
				if err != nil {
					return AdapterResult{}, err
				}
				if o["success"] == true {
					var exists bool
					imageHash, exists = o["code"]
					if !exists {
						return AdapterResult{}, errors.New("'code'")
					}
					break
				}
				attempts++
				if err = sourceSleep(ctx, h, 1); err != nil {
					return AdapterResult{}, err
				}
			}
		}
		if !Truth(imageHash) {
			return fail("签到失败：未获取到验证码")
		}
		imageURL = "https://hdsky.me/image.php?action=regimage&imagehash=" + pythonScalar(imageHash)
	} else {
		checking := clone(site).(Object)
		checking["timeout"] = nil
		page, err = BasePageSource(ctx, checking, routes[1].URL, h)
		if err != nil {
			return AdapterResult{}, err
		}
		if page == "" {
			return fail("签到失败，请检查站点连通性")
		}
		doc, err := parseHTML(page)
		if err != nil {
			return AdapterResult{}, err
		}
		if doc == nil {
			return fail("签到失败")
		}
		src, hash := "", ""
		srcFound, hashFound := false, false
		for _, n := range allNodes(doc) {
			if !specificAncestor(n, "form", "id", "frmSignin") {
				continue
			}
			if !srcFound && n.Data == "img" && hasAttr(n, "src") {
				src = attr(n, "src")
				srcFound = true
			}
			if !hashFound && n.Data == "input" && attr(n, "name") == "imagehash" && hasAttr(n, "value") {
				hash = attr(n, "value")
				hashFound = true
			}
		}
		if !srcFound || !hashFound {
			return AdapterResult{}, errors.New("list index out of range")
		}
		if src == "" || hash == "" {
			return fail("签到失败，获取签到参数失败")
		}
		imageHash = hash
		imageURL = "https://www.open.cd/" + src
	}
	result, err := matchOCR(ctx, h, imageURL)
	if err != nil {
		return AdapterResult{}, err
	}
	if !Truth(result) {
		return fail("签到失败：未获取到验证码")
	}
	text, ok := result.(string)
	if !ok {
		return AdapterResult{}, errors.New("OCR form value outside verified string compatibility")
	}
	form := map[string]string{"imagehash": pythonScalar(imageHash), "imagestring": text}
	order := []string{"imagehash", "imagestring"}
	address := routes[2].URL
	referer := ""
	if kind == AdapterHDSky {
		form["action"] = "showup"
		order = []string{"action", "imagehash", "imagestring"}
		referer = textValue(site["url"])
	}
	response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: address, Form: form, FormOrder: order, Referer: referer})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		return fail("签到失败：未获取到验证码")
	}
	o, err := responseObject(response)
	if err != nil {
		return AdapterResult{}, err
	}
	if kind == AdapterOpencd {
		if o["state"] == "success" {
			return AdapterResult{true, "签到成功"}, nil
		}
		return fail("签到失败")
	}
	if o["success"] == true {
		return AdapterResult{true, "签到成功"}, nil
	}
	message, exists := o["message"]
	if !exists {
		return AdapterResult{}, errors.New("'message'")
	}
	switch pythonScalar(message) {
	case "date_unmatch":
		return AdapterResult{true, "今日已签到"}, nil
	case "invalid_imagehash":
		return fail("签到失败：验证码错误")
	}
	return fail("签到失败：未获取到验证码")
}
