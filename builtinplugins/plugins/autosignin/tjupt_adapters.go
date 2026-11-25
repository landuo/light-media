package autosignin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"light-media/builtinplugins/plugins/autosignin/imagehash"
	"golang.org/x/net/html"
)

const AdapterTjupt FixedAdapter = "Tjupt"

// These are host dependencies. No path, URL grant, credentials or writable
// filesystem handle can be selected by SDK invocation input.
type TjuptAnswerCache interface {
	Ensure(context.Context) error
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
}
type TjuptPublicMedia interface {
	Suggest(context.Context, string) (*PageResponse, error)
	Image(context.Context, string) (*PageResponse, error)
}
type TjuptDependencies struct {
	Cache   TjuptAnswerCache
	Public  TjuptPublicMedia
	Hash    func([]byte) (string, error)
	Compare func(string, string) float64
}

func init() {
	fixedPolicies[AdapterTjupt] = fixedPolicy{SiteURL: "tjupt.org", RegisteredHosts: []string{"tjupt.org", "www.tjupt.org"}, Routes: []fixedRoute{{"GET", "https://www.tjupt.org/attendance.php"}, {"POST", "https://www.tjupt.org/attendance.php"}}}
}

func TjuptSignin(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	ctx = defaultCacheDumpEntry(ctx, sourceCacheAdapter)
	fail := func(s string) (AdapterResult, error) {
		if s != "签到失败" {
			siteLog(h, "ERROR", pythonScalar(site["name"])+" "+s)
		}
		return AdapterResult{false, s}, nil
	}
	d := h.Tjupt
	if d == nil || d.Cache == nil {
		return AdapterResult{}, ErrAdapterNotReady
	}
	if err := d.Cache.Ensure(ctx); err != nil {
		return AdapterResult{}, err
	}
	page, err := sourceBasePageValue(ctx, site, fixedPolicies[AdapterTjupt].Routes[0].URL, h)
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
	signed, err := sourcePageSignInResult(page, htmlData.Regexes["Tjupt._sign_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if signed {
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
	imageURL := ""
	imageFound := false
	for _, n := range allNodes(doc) {
		if n.Data == "img" && hasAttr(n, "src") && specificAncestor(n, "table", "class", "captcha") {
			imageURL = attr(n, "src")
			imageFound = true
			break
		}
	}
	if !imageFound {
		return AdapterResult{}, errors.New("list index out of range")
	}
	if imageURL == "" {
		return fail("签到失败，未获取到签到图片")
	}
	imageURL = "https://www.tjupt.org" + imageURL
	siteLog(h, "INFO", "获取到签到图片 "+imageURL)
	response, err := getHTTP(ctx, h, PageRequest{Method: "GET", URL: imageURL, Binary: true})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到图片 "+imageURL+" 请求失败")
		return AdapterResult{false, "签到失败，未获取到签到图片"}, nil
	}
	hash := d.Hash
	if hash == nil {
		hash = imagehash.Hash
	}
	compare := d.Compare
	if compare == nil {
		compare = imagehash.Compare
	}
	captchaHash, err := hash(response.Content)
	if err != nil {
		return AdapterResult{}, err
	}
	siteLog(h, "DEBUG", "签到图片hash "+captchaHash)
	values := []string{}
	selectedTexts := map[*html.Node]bool{}
	nodes := allNodes(doc)
	for _, n := range nodes {
		if n.Data != "input" || attr(n, "name") != "ban_robot" {
			continue
		}
		if hasAttr(n, "value") {
			values = append(values, attr(n, "value"))
		}
		// XPath following-sibling::text() returns a document ordered, deduplicated
		// node set, including every following text sibling, not just the next one.
		for p := n.NextSibling; p != nil; p = p.NextSibling {
			if p.Type == html.TextNode {
				selectedTexts[p] = true
			}
		}
	}
	options := []string{}
	for _, n := range nodes {
		if selectedTexts[n] {
			options = append(options, n.Data)
		}
	}
	if len(values) == 0 || len(options) == 0 {
		return fail("签到失败，未获取到答案选项")
	}
	count := len(values)
	if len(options) < count {
		count = len(options)
	}
	answerPairs := make([]string, 0, count)
	for i := 0; i < count; i++ {
		answerPairs = append(answerPairs, "("+sourceRepr(values[i])+", "+sourceRepr(options[i])+")")
	}
	siteLog(h, "DEBUG", "获取到所有签到选项 ["+strings.Join(answerPairs, ", ")+"]")
	cache := Object{}
	cacheKeys := []string{}
	raw, readErr := d.Cache.Read(ctx)
	if readErr == nil {
		parsed, err := parseSiteJSON(string(raw))
		if err != nil {
			return AdapterResult{}, err
		}
		var ok bool
		cache, ok = sourceHTTPObject(parsed)
		if !ok {
			return AdapterResult{}, cacheSubscriptError(parsed)
		}
		if ordered, ok := parsed.(sourceHTTPMap); ok {
			cacheKeys = append([]string{}, ordered.keys...)
		} else {
			cacheKeys, err = orderedObjectKeys(raw)
			if err != nil {
				return AdapterResult{}, err
			}
		}
		answer, exists := cache[captchaHash]
		if !exists {
			siteLog(h, "DEBUG", "查询本地已知答案失败："+sourceRepr(captchaHash)+"，继续请求豆瓣查询")
		}
		if exists && Truth(answer) {
			for i := 0; i < count; i++ {
				if pythonScalar(answer) == options[i] {
					return tjuptSubmit(ctx, site, h, values[i], nil, nil, "")
				}
			}
		}
	} else {
		if !isCacheIO(readErr) {
			return AdapterResult{}, readErr
		}
		siteLog(h, "DEBUG", "查询本地已知答案失败："+readErr.Error()+"，继续请求豆瓣查询")
	}
	for i := 0; i < count; i++ {
		label := options[i]
		if label != "" {
			if d.Public == nil {
				return AdapterResult{}, ErrAdapterNotReady
			}
			db, err := d.Public.Suggest(ctx, label)
			if err != nil {
				return AdapterResult{}, err
			}
			if db == nil || db.Status != 200 {
				siteLog(h, "DEBUG", "签到选项 "+label+" 未查询到豆瓣数据")
				continue
			}
			answers, err := parseSiteJSON(db.text())
			if err != nil {
				return AdapterResult{}, err
			}
			list, ok := answers.([]any)
			if !ok {
				list = []any{answers}
			}
			if len(list) == 0 {
				siteLog(h, "DEBUG", "签到选项 "+label+" 查询到豆瓣数据为空")
			}
			for _, answer := range list {
				object, ok := sourceHTTPObject(answer)
				if !ok {
					return AdapterResult{}, fmt.Errorf("'%s' object is not subscriptable", typeName(answer))
				}
				address, exists := object["img"]
				if !exists {
					return AdapterResult{}, errors.New("'img'")
				}
				addressText, ok := address.(string)
				if !ok {
					return AdapterResult{}, errors.New("public image URL outside verified string compatibility")
				}
				img, err := d.Public.Image(ctx, addressText)
				if err != nil {
					return AdapterResult{}, err
				}
				if img == nil || img.Status != 200 {
					siteLog(h, "DEBUG", "签到答案 "+label+" "+addressText+" 请求失败")
					continue
				}
				candidateHash, err := hash(img.Content)
				if err != nil {
					return AdapterResult{}, err
				}
				siteLog(h, "DEBUG", "签到答案图片hash "+label+" "+candidateHash)
				score := compare(captchaHash, candidateHash)
				siteLog(h, "INFO", "签到图片与选项 "+label+" 豆瓣图片相似度 "+pythonScalar(score))
				if score > 0.9 {
					return tjuptSubmit(ctx, site, h, values[i], cache, cacheKeys, captchaHash)
				}
			}
		}
		if err := sourceSleep(ctx, h, 5); err != nil {
			return AdapterResult{}, err
		}
	}
	siteLog(h, "ERROR", "豆瓣图片匹配，未获取到匹配答案")
	return AdapterResult{false, "签到失败，未获取到匹配答案"}, nil
}
func cacheSubscriptError(v any) error {
	if _, ok := v.([]any); ok {
		return errors.New("list indices must be integers or slices, not str")
	}
	if _, ok := v.(string); ok {
		return errors.New("string indices must be integers, not 'str'")
	}
	return fmt.Errorf("'%s' object is not subscriptable", typeName(v))
}

// Cache IO errors are source-caught. Decode/type errors deliberately propagate.
// Implementations wrap real filesystem errors with this marker, not every
// context/error; cancellation and authorization are never swallowed as IO.
var ErrTjuptCacheIO = errors.New("Tjupt answer cache IO error")

func isCacheIO(err error) bool { return errors.Is(err, ErrTjuptCacheIO) }
func tjuptSubmit(ctx context.Context, site Object, h SiteVisitHooks, answer string, cache Object, keys []string, hash string) (AdapterResult, error) {
	siteLog(h, "DEBUG", "提交data "+pythonFormRepr(map[string]string{"ban_robot": answer, "submit": "提交"}, []string{"ban_robot", "submit"}))
	response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: fixedPolicies[AdapterTjupt].Routes[1].URL, Form: map[string]string{"ban_robot": answer, "submit": "提交"}, FormOrder: []string{"ban_robot", "submit"}})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，签到接口请求失败")
		return AdapterResult{false, "签到失败，签到接口请求失败"}, nil
	}
	signed, err := SignInResult(response.text(), htmlData.Regexes["Tjupt._succeed_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if !signed {
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，请到页面查看")
		return AdapterResult{false, "签到失败，请到页面查看"}, nil
	}
	siteLog(h, "INFO", "签到成功")
	// Preserve source bugs: empty cache is never populated, and nonempty cache
	// stores submitted value while reads compare that value to the option label.
	if len(cache) > 0 && hash != "" {
		if _, exists := cache[hash]; !exists {
			keys = append(keys, hash)
		}
		cache[hash] = answer
		data, err := sourceCacheJSONWithContext(ctx, cache, keys)
		if err != nil {
			return AdapterResult{}, err
		}
		if err = h.Tjupt.Cache.Write(ctx, data); err != nil {
			if !isCacheIO(err) {
				return AdapterResult{}, err
			}
			siteLog(h, "DEBUG", "签到成功写入本地文件失败："+err.Error())
		}
	}
	return AdapterResult{true, "签到成功"}, nil
}
func orderedObjectKeys(data []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, errors.New("expected cache object")
	}
	keys := []string{}
	seen := map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		if err != nil {
			return nil, err
		}
		key := token.(string)
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
func encodeOrderedCache(cache Object, keys []string) ([]byte, error) {
	return sourceCacheJSON(cache, keys)
}
