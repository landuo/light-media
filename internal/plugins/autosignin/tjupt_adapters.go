package autosignin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"light-media/internal/plugins/autosignin/imagehash"
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
	fail := func(s string) (AdapterResult, error) { return AdapterResult{false, s}, nil }
	d := h.Tjupt
	if d == nil || d.Cache == nil {
		return AdapterResult{}, ErrAdapterNotReady
	}
	if err := d.Cache.Ensure(ctx); err != nil {
		return AdapterResult{}, err
	}
	page, err := BasePageSource(ctx, site, fixedPolicies[AdapterTjupt].Routes[0].URL, h)
	if err != nil {
		return AdapterResult{}, err
	}
	if page == "" {
		return fail("签到失败，请检查站点连通性")
	}
	if strings.Contains(page, "login.php") {
		return fail("签到失败，Cookie已失效")
	}
	signed, err := SignInResult(page, htmlData.Regexes["Tjupt._sign_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if signed {
		return AdapterResult{true, "今日已签到"}, nil
	}
	doc, err := parseHTML(page)
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
	response, err := getHTTP(ctx, h, PageRequest{Method: "GET", URL: imageURL, Binary: true})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		return fail("签到失败，未获取到签到图片")
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
	cache := Object{}
	cacheKeys := []string{}
	raw, readErr := d.Cache.Read(ctx)
	if readErr == nil {
		parsed, err := parseSiteJSON(string(raw))
		if err != nil {
			return AdapterResult{}, err
		}
		var ok bool
		cache, ok = parsed.(Object)
		if !ok {
			return AdapterResult{}, cacheSubscriptError(parsed)
		}
		cacheKeys, err = orderedObjectKeys(raw)
		if err != nil {
			return AdapterResult{}, err
		}
		if answer, exists := cache[captchaHash]; exists && Truth(answer) {
			for i := 0; i < count; i++ {
				if pythonScalar(answer) == options[i] {
					return tjuptSubmit(ctx, site, h, values[i], nil, nil, "")
				}
			}
		}
	} else if !isCacheIO(readErr) {
		return AdapterResult{}, readErr
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
				continue
			}
			answers, err := parseSiteJSON(db.Text)
			if err != nil {
				return AdapterResult{}, err
			}
			list, ok := answers.([]any)
			if !ok {
				list = []any{answers}
			}
			for _, answer := range list {
				object, ok := answer.(Object)
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
					continue
				}
				candidateHash, err := hash(img.Content)
				if err != nil {
					return AdapterResult{}, err
				}
				if compare(captchaHash, candidateHash) > 0.9 {
					return tjuptSubmit(ctx, site, h, values[i], cache, cacheKeys, captchaHash)
				}
			}
		}
		if err := sourceSleep(ctx, h, 5); err != nil {
			return AdapterResult{}, err
		}
	}
	return fail("签到失败，未获取到匹配答案")
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
	response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: fixedPolicies[AdapterTjupt].Routes[1].URL, Form: map[string]string{"ban_robot": answer, "submit": "提交"}, FormOrder: []string{"ban_robot", "submit"}})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		return AdapterResult{false, "签到失败，签到接口请求失败"}, nil
	}
	signed, err := SignInResult(response.Text, htmlData.Regexes["Tjupt._succeed_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if !signed {
		return AdapterResult{false, "签到失败，请到页面查看"}, nil
	}
	// Preserve source bugs: empty cache is never populated, and nonempty cache
	// stores submitted value while reads compare that value to the option label.
	if len(cache) > 0 && hash != "" {
		if _, exists := cache[hash]; !exists {
			keys = append(keys, hash)
		}
		cache[hash] = answer
		data, err := encodeOrderedCache(cache, keys)
		if err != nil {
			return AdapterResult{}, err
		}
		if err = h.Tjupt.Cache.Write(ctx, data); err != nil && !isCacheIO(err) {
			return AdapterResult{}, err
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
	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			compact.WriteByte(',')
		}
		encoded, err := encodeSourceJSON(key)
		if err != nil {
			return nil, err
		}
		compact.Write(encoded)
		compact.WriteByte(':')
		encoded, err = encodeSourceJSON(cache[key])
		if err != nil {
			return nil, err
		}
		compact.Write(encoded)
	}
	compact.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "    "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// TjuptPublicHTTP is an uncredentialed, dedicated capability. The host must
// provide an explicit image-origin allowlist; data returned by Douban cannot
// grant an arbitrary origin. Generic site same-origin authorization is intact.
type TjuptPublicHTTP struct{ clients map[string]PageTransport }

func BindTjuptPublicHTTP(imageOrigins []string) (*TjuptPublicHTTP, error) {
	hosts := append([]string{"https://movie.douban.com"}, imageOrigins...)
	p := &TjuptPublicHTTP{clients: map[string]PageTransport{}}
	for _, address := range hosts {
		u, err := url.Parse(address)
		if err != nil || u.User != nil || u.Scheme != "https" || u.Host == "" || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, ErrSiteTargetDenied
		}
		bound, err := BindSiteHTTP(SiteHTTPBinding{URL: address, UserAgent: nil})
		if err != nil {
			return nil, err
		}
		p.clients[originOf(u)] = tjuptPublicBound{bound}
	}
	return p, nil
}
func (p *TjuptPublicHTTP) Suggest(ctx context.Context, label string) (*PageResponse, error) {
	if p == nil {
		return nil, ErrAdapterNotReady
	}
	// Raw interpolation is intentional. Source treats '&'/'#' as URL syntax,
	// not encoded parameter values. http.NewRequest quotes non-ASCII path/query.
	address := sourceRequoteURI("https://movie.douban.com/j/subject_suggest?q=" + label)
	u, err := url.Parse(address)
	if err != nil {
		return nil, ErrSiteTargetDenied
	}

	return p.clients["https://movie.douban.com:443"].Do(ctx, PageRequest{Method: "GET", URL: u.String(), NoCookies: true})
}
func (p *TjuptPublicHTTP) Image(ctx context.Context, address string) (*PageResponse, error) {
	if p == nil {
		return nil, ErrAdapterNotReady
	}
	u, err := url.Parse(address)
	if err != nil || u.User != nil {
		return nil, ErrSiteTargetDenied
	}
	client := p.clients[originOf(u)]
	if client == nil {
		return nil, ErrSiteTargetDenied
	}
	return client.Do(ctx, PageRequest{Method: "GET", URL: address, NoCookies: true, Binary: true, Referer: "https://movie.douban.com"})
}

type tjuptPublicBound struct{ bound *BoundSiteHTTP }

func (b tjuptPublicBound) Do(ctx context.Context, r PageRequest) (*PageResponse, error) {
	return b.bound.do(ctx, r, "https://movie.douban.com")
}

// requests.utils.requote_uri first decodes percent-encoded unreserved ASCII.
// A malformed escape falls back to quoting every percent, including valid ones.
func sourceRequoteURI(value string) string {
	// urllib3.parse_url first normalizes/encodes query and fragment. This
	// helper is confined to the compiled Tjupt suggest URL, not arbitrary URL
	// parsing, IDNA, userinfo, ports, paths, or redirect authorization.
	prefix, tail, ok := strings.Cut(value, "?")
	if ok {
		query, fragment, hasFragment := strings.Cut(tail, "#")
		value = prefix + "?" + sourceQueryComponent(query)
		if hasFragment {
			value += "#" + sourceQueryComponent(fragment)
		}
	}

	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	var decoded strings.Builder
	invalid := false
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) {
			a, b := sourceHex(value[i+1]), sourceHex(value[i+2])
			if a >= 0 && b >= 0 {
				c := byte(a*16 + b)
				if strings.ContainsRune(unreserved, rune(c)) {
					decoded.WriteByte(c)
					i += 2
					continue
				}
			} else {
				invalid = true
			}
		}
		decoded.WriteByte(value[i])
	}
	text := decoded.String()
	if invalid {
		text = value
	}
	const safe = "!#$&'()*+,/:;=?@[]~"
	var out strings.Builder
	for _, c := range []byte(text) {
		if strings.ContainsRune(unreserved, rune(c)) || strings.ContainsRune(safe, rune(c)) || c == '%' && !invalid {
			out.WriteByte(c)
		} else {
			fmt.Fprintf(&out, "%%%02X", c)
		}
	}
	return out.String()
}
func sourceHex(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	if c >= 'a' && c <= 'f' {
		return int(c-'a') + 10
	}
	if c >= 'A' && c <= 'F' {
		return int(c-'A') + 10
	}
	return -1
}

func sourceQueryComponent(value string) string {
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~!$&'()*+,;=:@/?"
	bytes := []byte(value)
	percent, total := 0, 0
	for i := 0; i < len(bytes); i++ {
		if bytes[i] == '%' {
			total++
			if i+2 < len(bytes) && sourceHex(bytes[i+1]) >= 0 && sourceHex(bytes[i+2]) >= 0 {
				percent++
				bytes[i+1] = "0123456789ABCDEF"[sourceHex(bytes[i+1])]
				bytes[i+2] = "0123456789ABCDEF"[sourceHex(bytes[i+2])]
			}
		}
	}
	var out strings.Builder
	for _, c := range bytes {
		if strings.ContainsRune(allowed, rune(c)) || c == '%' && percent == total {
			out.WriteByte(c)
		} else {
			fmt.Fprintf(&out, "%%%02X", c)
		}
	}
	return out.String()
}
