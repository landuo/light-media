// Package mergemessagenotify ports installed MergeMessageNotify 1.20.3
// (hotlcc). Original source and checksums are bound in migration/ledger.json.
package mergemessagenotify

import (
	"context"
	"encoding/base64"
	"errors"
	"time"
)

var ErrDependency = errors.New("required channel dependency unavailable")

type NotificationType struct {
	Name  string
	Value string
}

var NotificationTypes = []NotificationType{{"Download", "资源下载"}, {"Organize", "整理入库"}, {"Subscribe", "订阅"}, {"SiteMessage", "站点"}, {"MediaServer", "媒体服务器"}, {"Manual", "手动处理"}, {"Plugin", "插件"}, {"Other", "其它"}}

func NotifyType(name string) *NotificationType {
	for _, t := range NotificationTypes {
		if t.Name == name {
			return &t
		}
	}
	return nil
}

type Message struct {
	Title   any               `json:"title"`
	Text    any               `json:"text"`
	Type    *NotificationType `json:"-"`
	RawType any               `json:"-"`
	Ext     map[string]any    `json:"ext_info,omitempty"`
}

// Request records Python requests kwargs exactly, including absence vs None,
// bytes vs text and JSON vs form data. The host authorizes each destination.
type Request struct {
	Method string
	URL    string
	Kwargs map[string]any
}
type Response struct {
	Status    int
	JSON      any
	JSONError bool
	Text      string
	Content   []byte
}

func (r Response) OK() bool { return r.Status < 400 }
func (r Response) json() (map[string]any, error) {
	if r.JSONError {
		return nil, errors.New("ValueError")
	}
	v := r.JSON
	if !truth(v) {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("AttributeError")
	}
	return m, nil
}

type SMTPRequest struct {
	Host, Encrypt                     string
	Port                              any
	Username, Password                string
	From                              string
	To                                []string
	Title, Text, HTML, FromName, Date string
}

// SMTPQuitError identifies an exception from the original finally: smtp.quit()
// path. Unlike connect/login/sendmail errors it escapes send_message.
type SMTPQuitError struct{ Err error }

func (e SMTPQuitError) Error() string { return e.Err.Error() }
func (e SMTPQuitError) Unwrap() error { return e.Err }

type AppriseRequest struct {
	URLs                []string
	Title, Body, Attach any
}

// Host never has an implicit network implementation in the candidate. SMTP and
// Apprise return original library success/errors; adapters require separate proof.
type Host struct {
	HTTP       func(context.Context, Request) (Response, error)
	SMTP       func(context.Context, map[string]any, SMTPRequest) error
	Apprise    func(context.Context, AppriseRequest) (any, error)
	SaveConfig func(context.Context, map[string]any) (bool, error)
	LoadConfig func(context.Context) (map[string]any, error)
	Log        func(context.Context, string, string)
	Render     func(context.Context, any, map[string]any) (any, error)
	Now        func() time.Time
	UUID       func() string
	Proxy      any
	Timezone   string
}

func encode64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

type Channel struct {
	Key, Name    string
	Order        int
	Defaults     map[string]any
	Elements     []any
	Suggested    map[string]any
	SaveDefaults bool
}
type Attempt struct {
	Key    string
	Result any
	Error  string
}
type Dispatch struct {
	Attempts        []Attempt
	Success, Failed int
	Ignored         bool
	Error           string
}
