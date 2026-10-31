package autosignin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"light-media/internal/jinja/exec"
	"light-media/internal/plugins"
	"light-media/internal/plugins/iyuuautoseed/compat/urlprep"
)

// OrdinaryMaterialMaxBytes is a target resource boundary, not a source limit.
// It bounds both compressed bytes and every decoded representation.
const OrdinaryMaterialMaxBytes = 16 << 20

// OrdinaryMaterialRequest is a trusted host port, not an SDK network capability.
// Authority, credentials, current guard and proxy snapshot come from the binding.
// Params are query parameters for BOTH methods. Only the helper's separate Form
// is a POST body. No caller TLS option is exposed.
type OrdinaryMaterialRequest struct {
	Method, URL     string
	Params, Headers plugins.OCRValue
	Form            map[string]string
	FormOrder       []string
	AllowRedirects  bool
}

type OrdinaryMaterialResponse struct {
	Status      int
	Content     []byte
	Text        string
	TextError   error
	ContentType string
	Headers     map[string]string
}

// OrdinaryMaterialSourceError is an uncaught source preparation exception. Its
// message intentionally excludes URLs, credentials, header and parameter values.
type OrdinaryMaterialSourceError struct{ Class string }

func (e *OrdinaryMaterialSourceError) Error() string { return "material request: " + e.Class }
func materialSourceError(class string) error         { return &OrdinaryMaterialSourceError{Class: class} }

// RequestUtils catches requests.InvalidHeader/InvalidURL and returns None.
var errMaterialRequestRejected = errors.New("material request preparation rejected")

type ordinaryMaterialMode struct {
	allowRedirects  bool
	headers         http.Header
	customHeaders   []string
	contentType     string
	responseHeaders map[string]string
	textError       error
}

func (b *BoundSiteHTTP) DoOrdinaryMaterial(ctx context.Context, input OrdinaryMaterialRequest) (*OrdinaryMaterialResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || (input.Method != "GET" && input.Method != "POST") {
		return nil, ErrSiteTargetDenied
	}
	address, err := materialRequestURL(input.URL, input.Params)
	if err != nil {
		return materialPreparationFailure(err)
	}
	headers, custom, err := b.materialHeaders(input.Headers)
	if err != nil {
		return materialPreparationFailure(err)
	}
	mode := &ordinaryMaterialMode{allowRedirects: input.AllowRedirects, headers: headers, customHeaders: custom}
	response, err := b.doWithOrdinaryModes(ctx, PageRequest{Method: input.Method, URL: address, Form: input.Form, FormOrder: append([]string(nil), input.FormOrder...)}, "", nil, mode)
	if err != nil || response == nil {
		return nil, err
	}
	return &OrdinaryMaterialResponse{Status: response.Status, Content: response.Content, Text: response.Text,
		TextError: mode.textError, ContentType: mode.contentType, Headers: mode.responseHeaders}, nil
}

func materialPreparationFailure(err error) (*OrdinaryMaterialResponse, error) {
	if errors.Is(err, errMaterialRequestRejected) {
		return nil, nil
	}
	return nil, err
}

func (m *ordinaryMaterialMode) applyHeaders(req *http.Request, cookie string, hasBody bool) {
	req.Header = m.headers.Clone()
	if _, supplied := req.Header["Cookie"]; !supplied && cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if hasBody && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
}

func (m *ordinaryMaterialMode) redirect(original string, req *http.Request) error {
	if err := ordinaryOCRRedirect(original, req); err != nil {
		return err
	}
	if originOf(req.URL) != original {
		// A descriptor may name its token header arbitrarily. Never infer that
		// an unknown custom header is safe to forward to a different origin.
		for _, name := range m.customHeaders {
			req.Header.Del(name)
		}
		for _, name := range []string{"Cookie", "Authorization", "Proxy-Authorization", "Api-Token", "X-Csrf-Token"} {
			req.Header.Del(name)
		}
	}
	return nil
}

func (m *ordinaryMaterialMode) captureResponse(res *http.Response) {
	m.contentType = res.Header.Get("Content-Type")
	m.responseHeaders = make(map[string]string, len(res.Header))
	for name, values := range res.Header {
		m.responseHeaders[name] = strings.Join(values, ", ")
	}
}

func materialValue(v plugins.OCRValue) (plugins.OCRValue, error) {
	if v.Kind == "" {
		v.Kind = "null"
	}
	if err := v.Validate(); err != nil {
		return v, err
	}
	return v, nil
}

func (b *BoundSiteHTTP) materialHeaders(input plugins.OCRValue) (http.Header, []string, error) {
	value, err := materialValue(input)
	if err != nil {
		return nil, nil, err
	}
	headers := http.Header{"Accept-Encoding": {"gzip, deflate, br"}, "Connection": {"keep-alive"}}
	if !value.Truthy() {
		agent := "python-urllib3/2.7.0"
		if b.binding.UserAgent != nil {
			var ok bool
			agent, ok = b.binding.UserAgent.(string)
			if !ok {
				return nil, nil, errMaterialRequestRejected
			}
		}
		encoded, err := materialHeaderText([]rune(agent), false)
		if err != nil {
			return nil, nil, err
		}
		if encoded == "" {
			encoded = " " // net/http otherwise omits an explicitly empty User-Agent.
		}
		headers["User-Agent"] = []string{encoded}
		headers.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		return headers, nil, nil
	}
	if value.Kind != "object" {
		return nil, nil, materialSourceError("AttributeError")
	}
	headers.Set("User-Agent", "python-requests/2.34.2")
	headers.Set("Accept", "*/*")
	custom := make([]string, 0, len(value.Entries))
	for _, entry := range value.Entries {
		name, err := materialHeaderText(entry.Key, true)
		if err != nil {
			return nil, nil, err
		}
		// Host is authority, not descriptor configuration. The URL binding
		// cannot be bypassed through a distinct virtual-host credential target.
		if strings.EqualFold(name, "Host") {
			return nil, nil, ErrSiteTargetDenied
		}
		custom = append(custom, name)
		if entry.Value.Kind == "null" {
			headers.Del(name)
			continue
		}
		if entry.Value.Kind != "string" {
			return nil, nil, errMaterialRequestRejected
		}
		text, err := materialHeaderText(entry.Value.Points, false)
		if err != nil {
			return nil, nil, err
		}
		if strings.EqualFold(name, "User-Agent") && text == "" {
			text = " "
		}
		if strings.EqualFold(name, "Origin") || strings.EqualFold(name, "Referer") {
			if _, err := b.authorize(string(entry.Value.Points)); err != nil {
				return nil, nil, err
			}
		}
		headers.Set(name, text)
	}
	if _, present := headers["User-Agent"]; !present {
		headers.Set("User-Agent", "python-urllib3/2.7.0")
	}
	return headers, custom, nil
}

func materialHeaderText(points []rune, name bool) (string, error) {
	if name && len(points) == 0 {
		return "", errMaterialRequestRejected
	}
	for i, point := range points {
		if point == '\r' || point == '\n' || i == 0 && (unicode.IsSpace(point) || point >= 0x1c && point <= 0x1f) || name && point == ':' {
			return "", errMaterialRequestRejected
		}
	}
	bytes := make([]byte, len(points))
	for i, point := range points {
		if point > 255 || name && point > 127 {
			return "", materialSourceError("UnicodeEncodeError")
		}
		bytes[i] = byte(point)
	}
	return string(bytes), nil
}

func materialRequestURL(address string, input plugins.OCRValue) (string, error) {
	prepared, err := urlprep.Prepare(address)
	if err != nil {
		if class := urlprep.ErrorClass(err); class == "UnicodeEncodeError" || class == "TypeError" {
			return "", materialSourceError(class)
		}
		return "", errMaterialRequestRejected
	}
	u, err := url.Parse(prepared)
	if err != nil {
		return "", errMaterialRequestRejected
	}
	value, err := materialValue(input)
	if err != nil {
		return "", err
	}
	encoded, scalar, err := materialParams(value)
	if err != nil {
		return "", err
	}
	if encoded != "" {
		if u.RawQuery != "" {
			u.RawQuery += "&" + encoded
		} else {
			if scalar {
				return "", materialSourceError("TypeError")
			}
			u.RawQuery = encoded
		}
	}
	prepared, err = urlprep.Prepare(u.String())
	if err != nil {
		return "", errMaterialRequestRejected
	}
	return prepared, nil
}

func materialParams(value plugins.OCRValue) (string, bool, error) {
	if !value.Truthy() {
		return "", false, nil
	}
	if value.Kind == "string" {
		s, err := materialScalar(value, false)
		return s, false, err
	}
	if value.Kind != "object" && value.Kind != "list" {
		s, err := materialScalar(value, false)
		return s, true, err
	}
	type pair struct{ key, value plugins.OCRValue }
	pairs := []pair{}
	if value.Kind == "object" {
		for _, entry := range value.Entries {
			pairs = append(pairs, pair{plugins.OCRValue{Kind: "string", Points: entry.Key}, entry.Value})
		}
	} else {
		for _, entry := range value.Items {
			items, iterable := materialIterable(entry, true)
			if !iterable {
				return "", false, materialSourceError("TypeError")
			}
			if len(items) != 2 {
				return "", false, materialSourceError("ValueError")
			}
			pairs = append(pairs, pair{items[0], items[1]})
		}
	}
	parts := []string{}
	for _, pair := range pairs {
		key, err := materialScalar(pair.key, false)
		if err != nil {
			return "", false, err
		}
		values, iterable := materialIterable(pair.value, false)
		if !iterable {
			values = []plugins.OCRValue{pair.value}
		}
		for _, value := range values {
			if value.Kind == "null" {
				continue
			}
			// requests expands a collection once; urlencode(doseq=True)
			// expands nested values once more, retaining their iteration order.
			inner, iterable := materialIterable(value, false)
			if !iterable {
				inner = []plugins.OCRValue{value}
			}
			for _, item := range inner {
				text, err := materialScalar(item, false)
				if err != nil {
					return "", false, err
				}
				parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(text))
			}
		}
	}
	return strings.Join(parts, "&"), false, nil
}

func materialIterable(value plugins.OCRValue, stringsToo bool) ([]plugins.OCRValue, bool) {
	switch value.Kind {
	case "list":
		return value.Items, true
	case "object":
		items := make([]plugins.OCRValue, 0, len(value.Entries))
		for _, entry := range value.Entries {
			items = append(items, plugins.OCRValue{Kind: "string", Points: entry.Key})
		}
		return items, true
	case "string":
		if stringsToo {
			items := make([]plugins.OCRValue, 0, len(value.Points))
			for _, point := range value.Points {
				items = append(items, plugins.OCRValue{Kind: "string", Points: []rune{point}})
			}
			return items, true
		}
	}
	return nil, false
}

func materialScalar(value plugins.OCRValue, repr bool) (string, error) {
	switch value.Kind {
	case "null":
		return "None", nil
	case "bool":
		if value.Bool {
			return "True", nil
		}
		return "False", nil
	case "int":
		return value.Number, nil
	case "float":
		switch value.Number {
		case "nan":
			return "nan", nil
		case "+inf":
			return "inf", nil
		case "-inf":
			return "-inf", nil
		}
		number, err := strconv.ParseFloat(value.Number, 64)
		if err != nil {
			return "", err
		}
		return exec.AsValue(number).String(), nil
	case "string":
		for _, point := range value.Points {
			if point >= 0xd800 && point <= 0xdfff {
				if repr {
					return materialSurrogateRepr(value.Points), nil
				}
				return "", materialSourceError("UnicodeEncodeError")
			}
		}
		if repr {
			return exec.PythonRepr(exec.AsValue(string(value.Points))), nil
		}
		return string(value.Points), nil
	case "list":
		parts := []string{}
		for _, item := range value.Items {
			text, err := materialScalar(item, true)
			if err != nil {
				return "", err
			}
			parts = append(parts, text)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case "object":
		parts := []string{}
		for _, entry := range value.Entries {
			key, err := materialScalar(plugins.OCRValue{Kind: "string", Points: entry.Key}, true)
			if err != nil {
				return "", err
			}
			text, err := materialScalar(entry.Value, true)
			if err != nil {
				return "", err
			}
			parts = append(parts, key+": "+text)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", materialSourceError("TypeError")
}

func materialSurrogateRepr(points []rune) string {
	quote := '\''
	hasSingle, hasDouble := false, false
	for _, point := range points {
		hasSingle = hasSingle || point == '\''
		hasDouble = hasDouble || point == '"'
	}
	if hasSingle && !hasDouble {
		quote = '"'
	}
	var out strings.Builder
	out.WriteRune(quote)
	for _, point := range points {
		switch {
		case point == quote || point == '\\':
			out.WriteRune('\\')
			out.WriteRune(point)
		case point >= 0xd800 && point <= 0xdfff:
			out.WriteString(`\u`)
			out.WriteString(strconv.FormatInt(int64(point), 16))
		default:
			repr := exec.PythonRepr(exec.AsValue(string(point)))
			out.WriteString(repr[1 : len(repr)-1])
		}
	}
	out.WriteRune(quote)
	return out.String()
}
