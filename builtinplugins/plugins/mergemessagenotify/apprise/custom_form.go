package apprise

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
)

var formAttachmentPattern = regexp.MustCompile(`^(?:([A-Za-z0-9_İıſK-]+)?[*?+$:.%]+([A-Za-z0-9_İıſK-]+)|([A-Za-z0-9_İıſK-]+)([*?+$:.%]?))`)

type formService struct {
	options                 providerHTTPOptions
	method, endpoint        string
	headers, params, extras [][2]string
	auth                    []any
	attachPrefix            string
	attachSuffix            string
	numberAttachments       bool
}

func parseFormService(raw string) (*formService, bool) {
	u, valid := parseSourceURL(raw)
	if !valid {
		return nil, false
	}
	s := &formService{
		method: "POST", params: u.params, extras: u.extras,
		headers:      [][2]string{{"User-Agent", "Apprise"}},
		attachPrefix: "file", numberAttachments: true,
	}
	if !s.options.configure(u) {
		return nil, false
	}
	if method := u.query["method"]; method != "" {
		s.method = strings.ToUpper(sourceUnquote(method))
		switch s.method {
		case "POST", "GET", "DELETE", "PUT", "HEAD", "PATCH", "UPDATE", "OPTIONS":
		default:
			return nil, false
		}
	}
	if attachmentName := u.query["attach-as"]; attachmentName != "" {
		match := formAttachmentPattern.FindStringSubmatch(strings.TrimFunc(attachmentName, sourceSpace))
		if match == nil {
			return nil, false
		}
		if match[2] != "" {
			s.attachPrefix, s.attachSuffix = match[1], match[2]
		} else {
			s.attachPrefix, s.numberAttachments = match[3], match[4] != ""
		}
	}
	scheme := "http"
	if u.scheme == "forms" {
		scheme = "https"
	}
	path := iftttRemoveDotSegments(u.path)
	if path == "" {
		path = "/"
	}
	s.endpoint = scheme + "://" + strings.ToLower(u.host) + u.port + path
	if u.user != nil && *u.user != "" {
		var password any
		if u.password != nil {
			password = *u.password
		}
		s.auth = []any{*u.user, password}
	}
	for _, header := range u.headers {
		s.headers = orderedPairSet(s.headers, header[0], header[1])
	}
	return s, true
}

func (s *formService) successful(status int) bool { return status >= 200 && status < 300 }

func (s *formService) truncateAttachments() bool { return s.options.overflow == "truncate" }

func (s *formService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunks(title, body, s.options.emojis, s.options.overflow, s.options.format)
}

func (s *formService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.options.emojis, s.options.overflow, s.options.format, func(title, body string) (Request, error) {
		return s.request(title, body, nil)
	})
}

func (s *formService) request(title, body string, files []attachment) (Request, error) {
	payload := make([][2]string, 0, 4+len(s.extras))
	for _, pair := range [][2]string{{"version", "1.0"}, {"title", title}, {"message", body}, {"type", "info"}} {
		name := pair[0]
		for _, extra := range s.extras {
			if extra[0] == pair[0] {
				name = extra[1]
				break
			}
		}
		if name != "" {
			payload = orderedPairSet(payload, name, pair[1])
		}
	}
	for _, extra := range s.extras {
		switch extra[0] {
		case "version", "title", "message", "type":
		default:
			payload = orderedPairSet(payload, extra[0], extra[1])
		}
	}
	query := s.params
	if s.method == "GET" {
		for _, param := range query {
			payload = orderedPairSet(payload, param[0], param[1])
		}
		query, payload = payload, nil
	}
	var data []byte
	var contentType string
	switch {
	case len(files) != 0:
		var err error
		data, contentType, err = s.multipart(payload, files)
		if err != nil {
			return Request{}, err
		}
	case len(payload) != 0:
		var form strings.Builder
		for i, field := range payload {
			if i != 0 {
				form.WriteByte('&')
			}
			form.WriteString(url.QueryEscape(field[0]))
			form.WriteByte('=')
			form.WriteString(url.QueryEscape(field[1]))
		}
		data, contentType = []byte(form.String()), "application/x-www-form-urlencoded"
	}
	headers := make(map[string]string, len(s.headers)+1)
	headerOrder := make([][2]string, 0, len(s.headers)+1)
	slots := make(map[string]int, len(s.headers))
	for _, header := range s.headers {
		// Requests overwrites headers by case-insensitive name, keeping the
		// spelling of the last value that was applied.
		key := strings.ToLower(header[0])
		if slot, exists := slots[key]; exists {
			delete(headers, headerOrder[slot][0])
			headerOrder[slot] = header
		} else {
			slots[key] = len(headerOrder)
			headerOrder = append(headerOrder, header)
		}
		headers[header[0]] = header[1]
	}
	if _, exists := slots["content-type"]; contentType != "" && !exists {
		headerOrder = append(headerOrder, [2]string{"Content-Type", contentType})
		headers["Content-Type"] = contentType
	}
	params := make(map[string]string, len(query))
	for _, pair := range query {
		params[pair[0]] = pair[1]
	}
	return Request{
		Method: s.method, URL: s.endpoint, Data: data,
		Headers: headers, HeaderOrder: headerOrder, Params: params, ParamOrder: query, Auth: s.auth,
		Verify: s.options.verify, Timeout: s.options.timeout,
	}, nil
}

var formMultipartEscape = strings.NewReplacer("\r", "%0D", "\n", "%0A", "\"", "%22")

func (s *formService) multipart(fields [][2]string, files []attachment) ([]byte, string, error) {
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
	for _, field := range fields {
		part, err := writer.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {fmt.Sprintf(`form-data; name="%s"`, formMultipartEscape.Replace(field[0]))},
		})
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write([]byte(field[1])); err != nil {
			return nil, "", err
		}
	}
	for i, file := range files {
		name := file.name
		if name == "" {
			name = fmt.Sprintf("file%03d.dat", i+1)
		}
		field := s.attachPrefix
		if s.numberAttachments {
			field += fmt.Sprintf("%02d", i+1) + s.attachSuffix
		}
		if strings.ContainsAny(file.mediaType, "\r\n") {
			return nil, "", errAttachmentUnavailable
		}
		part, err := writer.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, formMultipartEscape.Replace(name))},
			"Content-Type":        {file.mediaType},
		})
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(file.data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return data.Bytes(), writer.FormDataContentType(), nil
}
