package mergemessagenotify

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// This compares all source SMTP operations and decoded MIME fields/body, while
// retaining the original raw MIME bytes in fixtures. It does not claim byte-for-
// byte Python email serialization or an implemented SMTP network adapter.
func sourceSMTPTrace(t *testing.T, trace []any) []any {
	t.Helper()
	r := clone(trace).([]any)
	for _, v := range r {
		m := v.(map[string]any)
		if m["kind"] != "smtp_sendmail" {
			continue
		}
		msg, e := mail.ReadMessage(strings.NewReader(m["msg"].(string)))
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, msg.Body))
		if e != nil {
			t.Fatal(e)
		}
		headers := map[string]any{}
		for _, key := range []string{"Content-Type", "MIME-Version", "Content-Transfer-Encoding", "From", "Date", "To", "Subject"} {
			v, e := new(mime.WordDecoder).DecodeHeader(msg.Header.Get(key))
			if e != nil {
				t.Fatal(e)
			}
			headers[key] = v
		}
		m["msg"] = map[string]any{"headers": headers, "body": string(body)}
	}
	return r
}
func smtpSemanticTrace(r SMTPRequest) []any {
	trace := []any{map[string]any{"kind": "smtp_connect", "ssl": r.Encrypt == "SSL", "host": r.Host, "port": r.Port, "timeout": 60}}
	if r.Encrypt == "TLS" {
		trace = append(trace, map[string]any{"kind": "smtp_starttls"})
	}
	trace = append(trace, map[string]any{"kind": "smtp_login", "user": r.Username, "password": r.Password})
	typ := "text/plain"
	body := r.Text
	if r.HTML != "" {
		typ = "text/html"
		body = r.HTML
	}
	from := r.From
	if r.FromName != "" {
		from = r.FromName + " <" + r.From + ">"
	}
	headers := map[string]any{"Content-Type": typ + `; charset="utf-8"`, "MIME-Version": "1.0", "Content-Transfer-Encoding": "base64", "From": from, "Date": r.Date, "To": strings.Join(r.To, ","), "Subject": r.Title}
	trace = append(trace, map[string]any{"kind": "smtp_sendmail", "from_addr": r.From, "to_addrs": r.To, "msg": map[string]any{"headers": headers, "body": body}}, map[string]any{"kind": "smtp_quit"})
	return trace
}
func TestInstalledSMTPErrorAndFinally(t *testing.T) {
	cases(t, "testdata/installed-smtp_errors.jsonl.gz", func(i int, c map[string]any) {
		stage, _ := c["stage"].(string)
		trace := []any{}
		h := Host{Timezone: "Asia/Shanghai", Now: oracleNow, SMTP: func(_ context.Context, r SMTPRequest) error {
			frames := smtpSemanticTrace(r)
			for _, frame := range frames {
				trace = append(trace, frame)
				kind := frame.(map[string]any)["kind"]
				if kind == "smtp_"+stage {
					if stage == "quit" {
						return SMTPQuitError{errors.New("RuntimeError")}
					}
					if stage == "sendmail" {
						trace = append(trace, map[string]any{"kind": "smtp_quit"})
					}
					return errors.New("RuntimeError")
				}
			}
			return nil
		}}
		p := New(h)
		cfg := map[string]any{}
		for k, v := range c["config"].(map[string]any) {
			cfg["custom.email."+k] = v
		}
		p.Init(context.Background(), cfg, false)
		r, e := p.Send(context.Background(), "custom.email", Message{Title: "合成标题", Text: "合成正文"})
		out := map[string]any{"return": r}
		if e != nil {
			out = map[string]any{"error": e.Error()}
		}
		equal(t, fmt.Sprint(i, " outcome"), out, c["outcome"])
		equal(t, fmt.Sprint(i, " trace"), trace, sourceSMTPTrace(t, c["trace"].([]any)))
	})
}

func oracleNow() time.Time { return time.Unix(1700000000, 125000000) }
