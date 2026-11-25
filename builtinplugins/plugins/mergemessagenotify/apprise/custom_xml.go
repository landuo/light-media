package apprise

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

const customXMLStart = `<?xml version='1.0' encoding='utf-8'?>
<soapenv:Envelope
    xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
    xmlns:xsd="http://www.w3.org/2001/XMLSchema"
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
    <soapenv:Body>
        <Notification`

const customXMLEnd = `
       </Notification>
    </soapenv:Body>
</soapenv:Envelope>`

const customXMLSchema = ` xmlns:xsi="https://raw.githubusercontent.com/caronc/apprise/master/apprise/assets/NotifyXML-1.1.xsd"`

var customXMLEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "&apos;", `"`, "&quot;")

type customXMLService struct {
	options          providerHTTPOptions
	endpoint, method string
	headers          map[string]string
	headerOrder      [][2]string
	auth             []any
	overrides        map[string]string
	extras           object
}

func parseCustomXMLService(raw string) (*customXMLService, bool) {
	u, ok := parseSourceURL(raw)
	if !ok {
		return nil, false
	}
	s := &customXMLService{overrides: map[string]string{}}
	if !s.options.configure(u) {
		return nil, false
	}
	s.method = http.MethodPost
	if method := u.query["method"]; method != "" {
		s.method = strings.ToUpper(sourceUnquote(method))
	}
	switch s.method {
	case "POST", "GET", "DELETE", "PUT", "HEAD", "PATCH", "UPDATE", "OPTIONS":
	default:
		return nil, false
	}
	scheme := "http"
	if u.scheme == "xmls" {
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
	s.headerOrder = [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", "application/xml"}}
	for _, header := range u.headers {
		s.headerOrder = orderedPairSet(s.headerOrder, header[0], header[1])
	}
	s.headers = make(map[string]string, len(s.headerOrder))
	spelling := make(map[string]string, len(s.headerOrder))
	for _, header := range s.headerOrder {
		key := strings.ToLower(header[0])
		if previous, ok := spelling[key]; ok {
			delete(s.headers, previous)
		}
		spelling[key] = header[0]
		s.headers[header[0]] = header[1]
	}
	for _, extra := range u.extras {
		key := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return -1
		}, extra[0])
		switch key {
		case "":
			continue
		case "Version", "Subject", "Message", "MessageType":
			s.overrides[key] = extra[1]
		default:
			s.extras = s.extras.set(key, extra[1])
		}
	}
	return s, true
}

func (s *customXMLService) successful(status int) bool { return status >= 200 && status < 300 }

func (s *customXMLService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.options.emojis, s.options.overflow, s.options.format, func(title, body string) (Request, error) {
		return s.request(title, body, nil)
	})
}

func (s *customXMLService) truncateAttachments() bool { return s.options.overflow == "truncate" }

func (s *customXMLService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunks(title, body, s.options.emojis, s.options.overflow, s.options.format)
}

func (s *customXMLService) request(title, body string, attachments []attachment) (Request, error) {
	var fields object
	for _, pair := range [][2]string{
		{"Version", "1.1"}, {"Subject", title}, {"Message", body}, {"MessageType", "info"},
	} {
		key := pair[0]
		if replacement, overridden := s.overrides[key]; overridden {
			key = replacement
		}
		if key != "" {
			fields = fields.set(key, pair[1])
		}
	}
	for _, extra := range s.extras {
		fields = fields.set(extra.key, extra.value)
	}

	var data strings.Builder
	data.WriteString(customXMLStart)
	if len(s.overrides) == 0 && len(s.extras) == 0 {
		data.WriteString(customXMLSchema)
	}
	data.WriteString(">\n            ")
	for _, field := range fields {
		data.WriteString("<" + field.key + ">" + customXMLEscape.Replace(field.value.(string)) + "</" + field.key + ">")
	}
	data.WriteString("\n            ")
	if len(attachments) != 0 {
		data.WriteString(`<Attachments format="base64">`)
		for i, item := range attachments {
			name := item.name
			if name == "" {
				name = fmt.Sprintf("file%03d.dat", i+1)
			}
			data.WriteString(`<Attachment filename="` + customXMLEscape.Replace(name) + `" mimetype="` + customXMLEscape.Replace(item.mediaType) + `">`)
			data.WriteString(base64.StdEncoding.EncodeToString(item.data))
			data.WriteString("</Attachment>")
		}
		data.WriteString("</Attachments>")
	}
	data.WriteString(customXMLEnd)
	// The original parses the -query overrides but does not pass params to Requests.
	return Request{
		Method: s.method, URL: s.endpoint, Data: []byte(data.String()),
		Headers: s.headers, HeaderOrder: s.headerOrder, Auth: s.auth,
		Verify: s.options.verify, Timeout: s.options.timeout,
	}, nil
}
