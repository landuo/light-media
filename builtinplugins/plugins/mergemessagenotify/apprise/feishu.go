package apprise

import (
	"regexp"
	"strings"
)

const feishuEndpoint = "https://open.feishu.cn/open-apis/bot/v2/hook/"

var feishuToken = regexp.MustCompile(`(?i)^[a-z0-9_-]+$`)

type feishuService struct {
	providerHTTPOptions
	token string
}

func parseFeishuService(raw string) (*feishuService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	token := queryToken(u)
	if !valid || !feishuToken.MatchString(strings.TrimSuffix(token, "\n")) {
		return nil, false
	}
	s := &feishuService{token: strings.TrimFunc(token, sourceSpace)}
	return s, s.configure(u)
}

func (s *feishuService) requests(title, body string) ([]Request, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 19985)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		data, err := pythonJSON(object{{"msg_type", "text"}, {"content", object{{"text", chunk.body}}}})
		if err != nil {
			return nil, err
		}
		calls = append(calls, s.post(feishuEndpoint+s.token+"/", "application/json", data))
	}
	return calls, nil
}
