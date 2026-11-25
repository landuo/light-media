package mergemessagenotify

import (
	"context"
	"errors"
	"time"
	_ "time/tzdata"
)

// email preserves source validation and SMTP encryption selection. MIME byte
// serialization and SMTP session semantics are an explicit adapter dependency.
func (p *Plugin) email(ctx context.Context, get func(string) any, m Message, ext map[string]any) (any, error) {
	if !truth(m.Text) {
		return false, nil
	}
	for _, k := range []string{"smtp_host", "smtp_port", "username", "password"} {
		if !truth(get(k)) {
			return false, nil
		}
	}
	encrypt := get("smtp_encrypt_type")
	if !(encrypt == "SSL" || encrypt == "TLS" || encrypt == "NONE") {
		return false, nil
	}
	to, e := splitMulti(get("to_addrs"))
	if e != nil {
		return nil, e
	}
	if len(to) == 0 {
		return false, nil
	}
	text, e := stringValue(m.Text)
	if e != nil {
		return nil, e
	}
	html := ""
	if truth(ext["image"]) {
		html = "<div>" + text + "</div><br><img src=\"" + str(ext["image"]) + "\"></img>"
	}
	zone := p.host.Timezone
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	tz, e := time.LoadLocation(zone)
	if e != nil {
		return nil, e
	}
	date := p.now().In(tz).Format("Mon, 02 Jan 2006 15:04:05 -0700")
	if p.host.SMTP == nil {
		return nil, ErrDependency
	}
	config := map[string]any{}
	for _, key := range []string{"smtp_host", "smtp_port", "smtp_encrypt_type", "username", "password", "to_addrs", "from_name"} {
		config[key] = get(key)
	}
	e = p.host.SMTP(ctx, config, SMTPRequest{Host: str(get("smtp_host")), Encrypt: str(encrypt), Port: get("smtp_port"), Username: str(get("username")), Password: str(get("password")), From: str(get("username")), To: to, Title: str(or(m.Title, "")), Text: text, HTML: html, FromName: str(or(get("from_name"), "")), Date: date})
	if e != nil {
		var quit SMTPQuitError
		if errors.As(e, &quit) {
			return nil, quit.Err
		}
		return false, nil
	}
	return true, nil
}
