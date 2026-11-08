package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"light-media/internal/plugins"
	"light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/sites"
)

type originalBrushRecipe struct {
	Method     json.RawMessage `json:"method"`
	Cookie     json.RawMessage `json:"cookie"`
	Params     json.RawMessage `json:"params"`
	Header     json.RawMessage `json:"header"`
	Result     json.RawMessage `json:"result"`
	Success    json.RawMessage `json:"success"`
	ResultPath json.RawMessage `json:"result_path"`
	ResultKey  json.RawMessage `json:"result_query_param"`
	ResultBase json.RawMessage `json:"result_base_url"`
}

// Resolve the recipe only after selection, under the same configured-site
// lock, HTTP guard, proxy and budget as ordinary Brush material retrieval.
func originalBrushDownloadRecipe(ctx context.Context, client *sites.Client, base *url.URL, site SiteConfig, enclosure string, skipTips bool) ([]byte, string, error) {
	end := strings.LastIndexByte(enclosure, ']')
	if end < 1 || end > 24<<10 || strings.ContainsAny(enclosure, "\r\n\x00") {
		return nil, "", nil
	}
	address := enclosure[end+1:]
	if address == "" {
		return nil, "", nil
	}
	if end > 1 {
		decoded, err := base64.StdEncoding.DecodeString(enclosure[1:end])
		if err != nil || len(decoded) > 16<<10 || !utf8.Valid(decoded) {
			return nil, "", nil
		}
		var recipe *originalBrushRecipe
		if json.Unmarshal(decoded, &recipe) != nil || recipe == nil {
			return nil, "", nil
		}
		address, err = originalBrushResolveRecipe(ctx, client, base, site, recipe, address)
		if err != nil || address == "" {
			return nil, "", err
		}
	}
	if strings.HasPrefix(address, "magnet:?") {
		return nil, address, nil
	}
	target, err := originalBrushRecipeURL(base, address)
	if err != nil {
		return nil, "", err
	}
	if skipTips && site.Kind != "mteam" && site.Name != "天空" {
		originalBrushRecipeSkipTips(target)
	}
	headers, err := originalBrushRecipeHeaders(base, site, nil, false)
	if err != nil {
		return nil, "", err
	}
	response, err := client.BrushRecipeRequest(ctx, http.MethodGet, target, headers, true)
	if response.Status >= 400 {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	content, err := originalHostNetworkDecodeBounded(response.Content, response.ContentEncoding, 32<<20)
	return content, "", err
}

func originalBrushResolveRecipe(ctx context.Context, client *sites.Client, base *url.URL, site SiteConfig, recipe *originalBrushRecipe, address string) (string, error) {
	target, err := originalBrushRecipeURL(base, address)
	if err != nil {
		return "", err
	}
	query, valid := originalBrushRecipeParams(recipe.Params)
	if !valid {
		return "", nil
	}
	if query != "" {
		if target.RawQuery != "" {
			target.RawQuery += "&"
		}
		target.RawQuery += query
	}
	headers, err := originalBrushRecipeHeaders(base, site, recipe.Header, originalBaseJSONTruthy(recipe.Cookie))
	if err != nil {
		return "", err
	}
	method := http.MethodPost
	if selected, _ := sourceSiteString(recipe.Method); selected == "get" {
		method = http.MethodGet
	}
	response, err := client.BrushRecipeRequest(ctx, method, target, headers, false)
	if response.Status >= 400 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	content, err := originalHostNetworkDecodeBounded(response.Content, response.ContentEncoding, 1<<20)
	if err != nil {
		return "", nil
	}
	path := originalBrushRecipeText(recipe.Result)
	useJSON := originalBaseJSONTruthy(recipe.Result)
	text, err := iyuuautoseed.DecodeRequestsSiteResponse(content, response.ContentType, useJSON)
	if err != nil || len(text) > 1<<20 {
		return "", nil
	}
	if !useJSON {
		return text, nil
	}
	data := json.RawMessage(text)
	if originalBaseJSONTruthy(recipe.Success) {
		var object map[string]json.RawMessage
		key, stringKey := sourceSiteString(recipe.Success)
		if !stringKey || json.Unmarshal(data, &object) != nil || !originalBaseJSONTruthy(object[key]) {
			return "", nil
		}
	}
	for _, key := range strings.Split(path, ".") {
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil {
			return "", nil
		}
		data = object[key]
		if len(data) == 0 || string(data) == "null" {
			return "", nil
		}
	}
	value := originalBrushRecipeText(data)
	if originalBaseJSONTruthy(recipe.ResultPath) && originalBaseJSONTruthy(recipe.ResultKey) {
		rawBase := originalBrushRecipeText(recipe.ResultBase)
		if rawBase == "" {
			return "", nil
		}
		resultBase, err := originalBrushRecipeURL(base, strings.TrimRight(rawBase, "/")+"/")
		if err != nil {
			return "", err
		}
		result, err := originalBrushRecipeURL(resultBase, strings.TrimLeft(originalBrushRecipeText(recipe.ResultPath), "/"))
		if err != nil {
			return "", err
		}
		return result.String() + "?" + url.QueryEscape(originalBrushRecipeText(recipe.ResultKey)) + "=" + url.QueryEscape(value), nil
	}
	return value, nil
}

func originalBrushRecipeURL(base *url.URL, raw string) (*url.URL, error) {
	deny := bad("SITE_HTTP_TARGET_DENIED", "刷流取种配方须绑定当前站点来源")
	if raw == "" || len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00\\") {
		return nil, deny
	}
	address, err := url.Parse(raw)
	if err != nil || address.User != nil || address.Opaque != "" || address.Fragment != "" {
		return nil, deny
	}
	address = base.ResolveReference(address)
	if originalOCROrigin(address) != originalOCROrigin(base) {
		return nil, deny
	}
	return address, nil
}

func originalBrushRecipeHeaders(base *url.URL, site SiteConfig, raw json.RawMessage, useCookie bool) (map[string]string, error) {
	var overrides []sourceSiteTokenPair
	if originalBaseJSONTruthy(raw) {
		value, err := sourceSiteParseToken(raw)
		if err != nil || value.kind != '{' {
			return nil, bad("INVALID_PLUGIN_VALUE", "刷流取种配方请求头无效")
		}
		overrides = value.pairs
	}
	headers := make(http.Header)
	headers.Set("Accept-Encoding", "gzip, deflate, br")
	if len(overrides) == 0 {
		agent := site.UserAgent
		if agent == "" {
			agent = "python-urllib3/2.7.0"
		} else if agent == originalPluginServerUserAgent() {
			agent += " Plugin/brushflowlanduo"
		}
		headers.Set("User-Agent", agent)
		headers.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	} else {
		headers.Set("User-Agent", "python-requests/2.34.2")
		headers.Set("Accept", "*/*")
	}
	if useCookie {
		headers.Set("Cookie", originalOCRCookieHeader(site.Cookie))
	}
	for _, pair := range overrides {
		key := sourceSiteCodepointString(pair.key)
		allowed := !originalSiteHTTPForbiddenHeader(key)
		switch strings.ToLower(key) {
		case "cookie", "authorization", "api-key", "x-api-key", "api-token", "user-agent":
			allowed = true
		}
		if !allowed || !originalSiteHTTPHeaderName(key) {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "刷流取种配方请求头未获允许")
		}
		if pair.value.kind == 'n' {
			headers.Del(key)
			if strings.EqualFold(key, "User-Agent") {
				headers.Set(key, "python-urllib3/2.7.0")
			}
			continue
		}
		if pair.value.kind != '"' {
			return nil, bad("INVALID_PLUGIN_VALUE", "刷流取种配方请求头无效")
		}
		value := originalBrushRecipeValue(pair.value)
		if (strings.EqualFold(key, "Origin") || strings.EqualFold(key, "Referer")) && !originalSourceSiteHeaderOrigin(value, base, base) {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "刷流取种配方来源头必须同源")
		}
		headers.Set(key, value)
	}
	result := make(map[string]string, len(headers))
	for key := range headers {
		encoded, err := originalOCRHeaderLatin1(headers.Get(key))
		if err != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "刷流取种配方请求头无法编码")
		}
		result[key] = encoded
	}
	return result, nil
}

func originalBrushRecipeText(raw json.RawMessage) string {
	value, err := sourceSiteParseToken(raw)
	if err != nil {
		return ""
	}
	return originalBrushRecipeValue(value)
}

func originalBrushRecipeValue(value sourceSiteTokenValue) string {
	if value.kind == '"' {
		return sourceSiteCodepointString(value.text)
	}
	text, _ := value.repr()
	return text
}

func originalBrushRecipeParams(raw json.RawMessage) (string, bool) {
	if !originalBaseJSONTruthy(raw) {
		return "", true
	}
	value, err := sourceSiteParseToken(raw)
	if err != nil {
		return "", false
	}
	if value.kind == '"' {
		query := originalBrushRecipeValue(value)
		return query, !strings.ContainsAny(query, "\r\n\x00#")
	}
	pairs := []plugins.SiteHTTPPairV1{}
	appendValue := func(key string, item sourceSiteTokenValue) {
		if item.kind != 'n' {
			pairs = append(pairs, plugins.SiteHTTPPairV1{Name: key, Value: originalBrushRecipeValue(item)})
		}
	}
	appendPair := func(key string, item sourceSiteTokenValue) {
		switch item.kind {
		case '[':
			for _, entry := range item.items {
				appendValue(key, entry)
			}
		case '{':
			for _, entry := range item.pairs {
				pairs = append(pairs, plugins.SiteHTTPPairV1{Name: key, Value: sourceSiteCodepointString(entry.key)})
			}
		default:
			appendValue(key, item)
		}
	}
	switch value.kind {
	case '{':
		for _, pair := range value.pairs {
			appendPair(sourceSiteCodepointString(pair.key), pair.value)
		}
	case '[':
		for _, pair := range value.items {
			if pair.kind != '[' || len(pair.items) != 2 {
				return "", false
			}
			appendPair(originalBrushRecipeValue(pair.items[0]), pair.items[1])
		}
	default:
		return "", false
	}
	return originalSiteHTTPPairs(pairs), true
}

func originalBrushRecipeSkipTips(target *url.URL) {
	pairs := []plugins.SiteHTTPPairV1{}
	position := map[string]int{}
	put := func(name, value string) {
		if i, found := position[name]; found {
			pairs[i].Value = value
		} else {
			position[name] = len(pairs)
			pairs = append(pairs, plugins.SiteHTTPPairV1{Name: name, Value: value})
		}
	}
	for _, item := range strings.Split(target.RawQuery, "&") {
		name, value, found := strings.Cut(item, "=")
		if !found || value == "" {
			continue
		}
		name, nameErr := url.QueryUnescape(name)
		value, valueErr := url.QueryUnescape(value)
		if nameErr != nil || valueErr != nil {
			return // Source retains the original URL when transformation fails.
		}
		put(name, value)
	}
	put("letdown", "1")
	target.RawQuery = originalSiteHTTPPairs(pairs)
}
