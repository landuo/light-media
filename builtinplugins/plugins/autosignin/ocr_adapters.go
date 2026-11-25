package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	case *sourceOCRValue:
		return sourceLength(n.value)
	case string:
		return len(sourceCodepoints(n)), nil
	case []any:
		return len(n), nil
	case Object:
		return len(n), nil
	case sourceHTTPMap:
		return len(n.values), nil
	}
	return 0, fmt.Errorf("object of type '%s' has no len()", typeName(v))
}
func matchOCR(ctx context.Context, h SiteVisitHooks, address string, kind FixedAdapter, site Object) (any, error) {
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
		if kind == AdapterHDSky {
			siteLog(h, "INFO", fmt.Sprintf("OCR识别 %s 验证码：%s", pythonScalar(site["name"]), pythonScalar(result)))
		} else {
			siteLog(h, "DEBUG", fmt.Sprintf("ocr识别%s验证码 %s", pythonScalar(site["name"]), pythonScalar(result)))
		}
		if Truth(result) {
			length, err := sourceLength(result)
			if err != nil {
				return nil, err
			}
			if length == 6 {
				if kind == AdapterHDSky {
					siteLog(h, "INFO", fmt.Sprintf("OCR识别 %s 验证码成功：%s", pythonScalar(site["name"]), pythonScalar(result)))
				} else {
					siteLog(h, "INFO", fmt.Sprintf("ocr识别%s验证码成功 %s", pythonScalar(site["name"]), pythonScalar(result)))
				}
				break
			}
		}
		if kind == AdapterHDSky {
			siteLog(h, "INFO", fmt.Sprintf("OCR识别 %s 验证码失败，正在进行重试，目前重试次数：%d", pythonScalar(site["name"]), times+1))
		} else {
			siteLog(h, "DEBUG", fmt.Sprintf("ocr识别%s验证码失败，正在进行重试，目前重试次数 %d", pythonScalar(site["name"]), times+1))
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
	fail := func(message string) (AdapterResult, error) {
		if message != "签到失败" && message != "签到失败：验证码错误" {
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
	var signed bool
	if kind == AdapterHDSky {
		signed, err = sourcePageSignInResult(page, htmlData.Regexes["HDSky._sign_regex"])
	} else {
		signed, err = sourcePageSigninEvidence(page)
	}
	if err != nil {
		return AdapterResult{}, err
	}
	if signed {
		siteLog(h, "INFO", pythonScalar(site["name"])+" 今日已签到")
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
				siteLog(h, "INFO", fmt.Sprintf("获取 %s 验证码失败，正在进行重试，目前重试次数：%d", pythonScalar(site["name"]), attempts))
				if err = sourceSleep(ctx, h, 1); err != nil {
					return AdapterResult{}, err
				}
			}
		}
		if !Truth(imageHash) {
			return fail("签到失败：未获取到验证码")
		}
		imageURL = "https://hdsky.me/image.php?action=regimage&imagehash=" + pythonScalar(imageHash)
		siteLog(h, "INFO", fmt.Sprintf("获取到 %s 验证码链接：%s", pythonScalar(site["name"]), imageURL))
	} else {
		checking := clone(site).(Object)
		checking["timeout"] = nil
		page, err = sourceBasePageValue(ctx, checking, routes[1].URL, h)
		if err != nil {
			return AdapterResult{}, err
		}
		if !Truth(page) {
			return fail("签到失败，请检查站点连通性")
		}
		doc, err := sourcePageParseHTML(page)
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
		siteLog(h, "DEBUG", fmt.Sprintf("%s 获取到%s验证码链接 %s", pythonScalar(site["name"]), pythonScalar(site["name"]), imageURL))
	}
	result, err := matchOCR(ctx, h, imageURL, kind, site)
	if err != nil {
		return AdapterResult{}, err
	}
	if !Truth(result) {
		return fail("签到失败：未获取到验证码")
	}
	form := map[string]string{"imagehash": pythonScalar(imageHash)}
	var rawForm map[string]json.RawMessage
	var formValues map[string][]string
	if source, ok := result.(*sourceOCRValue); ok && source.fromHTTP {
		values, err := sourceOCRFormValues(source.value)
		if err != nil {
			return AdapterResult{}, err
		}
		formValues = map[string][]string{"imagestring": values}
	} else if text, ok := result.(string); ok {
		form["imagestring"] = text
	} else {
		value, err := rawOCRValue(result)
		if err != nil {
			return AdapterResult{}, err
		}
		rawForm = map[string]json.RawMessage{"imagestring": value}
	}
	order := []string{"imagehash", "imagestring"}
	address := routes[2].URL
	referer := ""
	if kind == AdapterHDSky {
		form["action"] = "showup"
		order = []string{"action", "imagehash", "imagestring"}
		referer = textValue(site["url"])
	}
	response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: address, Form: form, RawForm: rawForm, FormValues: formValues, FormOrder: order, Referer: referer})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		return fail("签到失败：未获取到验证码")
	}
	if kind == AdapterOpencd {
		siteLog(h, "DEBUG", "sign_res返回 "+response.text())
	}
	o, err := responseObject(response)
	if err != nil {
		return AdapterResult{}, err
	}
	if kind == AdapterOpencd {
		if o["state"] == "success" {
			siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
			return AdapterResult{true, "签到成功"}, nil
		}
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，签到接口返回 "+pythonJSONRepr(response.jsonText))
		return fail("签到失败")
	}
	if o["success"] == true {
		siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
		return AdapterResult{true, "签到成功"}, nil
	}
	o, err = responseObject(response)
	if err != nil {
		return AdapterResult{}, err
	}
	message, exists := o["message"]
	if !exists {
		return AdapterResult{}, errors.New("'message'")
	}
	if pythonScalar(message) == "date_unmatch" {
		siteLog(h, "WARNING", pythonScalar(site["name"])+" 重复成功")
		return AdapterResult{true, "今日已签到"}, nil
	}
	o, err = responseObject(response)
	if err != nil {
		return AdapterResult{}, err
	}
	message, exists = o["message"]
	if !exists {
		return AdapterResult{}, errors.New("'message'")
	}
	if pythonScalar(message) == "invalid_imagehash" {
		siteLog(h, "WARNING", pythonScalar(site["name"])+" 签到失败：验证码错误")
		return fail("签到失败：验证码错误")
	}
	return fail("签到失败：未获取到验证码")
}
