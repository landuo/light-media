package mergemessagesmtp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"light-media/internal/releasemeta/legacyanime"
	merge "light-media/work/mergemessagenotify"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var ErrBinding = errors.New("SMTP trusted binding mismatch")
var ErrUnsupported = errors.New("SMTP source behavior not implemented")
var ErrNotConfigured = errors.New("SMTP source configuration is incomplete")

// BindingFromTrustedConfig accepts only the host-owned component configuration
// snapshot (unprefixed custom.email fields), never an Invocation/worker map.
// Unknown config fields are neither used nor rewritten. Dynamic non-string
// fields are rejected instead of adopting the frozen core's str() conversion.
func BindingFromTrustedConfig(config map[string]any) (Binding, error) {
	var b Binding
	for _, k := range []string{"smtp_host", "smtp_port", "smtp_encrypt_type", "username", "password", "to_addrs"} {
		if !configTruth(config[k]) {
			return b, ErrNotConfigured
		}
	}
	var ok bool
	if b.Host, ok = config["smtp_host"].(string); !ok {
		return b, ErrUnsupported
	}
	if b.Username, ok = config["username"].(string); !ok {
		return b, ErrUnsupported
	}
	if b.Password, ok = config["password"].(string); !ok {
		return b, ErrUnsupported
	}
	if b.Encrypt, ok = config["smtp_encrypt_type"].(string); !ok {
		return b, ErrUnsupported
	}
	if !(b.Encrypt == "NONE" || b.Encrypt == "SSL" || b.Encrypt == "TLS") {
		return b, ErrNotConfigured
	}
	if b.Port, ok = portValue(config["smtp_port"]); !ok {
		return b, ErrUnsupported
	}
	raw, ok := config["to_addrs"].(string)
	if !ok {
		return b, ErrUnsupported
	}
	seen := map[string]bool{}
	b.Recipients = []string{}
	for _, v := range strings.Split(raw, ",") {
		v = legacyanime.Strip(v)
		if v != "" && !seen[v] {
			b.Recipients = append(b.Recipients, v)
			seen[v] = true
		}
	}
	if len(b.Recipients) == 0 {
		return b, ErrNotConfigured
	}
	name := config["from_name"]
	if name == nil {
		b.FromName = "MoviePilot"
	} else if configTruth(name) {
		if b.FromName, ok = name.(string); !ok {
			return b, ErrUnsupported
		}
	}
	return b, nil
}
func configTruth(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case float64:
		return x != 0
	case json.Number:
		n, e := strconv.ParseFloat(string(x), 64)
		return e != nil || n != 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return r.Len() != 0
	}
	return true
}

type Binding struct {
	Host                                  string
	Port                                  int
	Encrypt, Username, Password, FromName string
	Recipients                            []string
}
type Options struct {
	DialContext   func(context.Context, string, string) (net.Conn, error)
	TLSConfig     *tls.Config
	LocalHostname string
	Timeout       time.Duration
}
type Adapter struct {
	binding Binding
	options Options
}

func New(binding Binding, options Options) (*Adapter, error) {
	if !ascii(binding.Host) {
		return nil, ErrUnsupported
	}
	if binding.Host == "" || strings.ContainsAny(binding.Host, "/\r\n\x00") || binding.Port < 1 || binding.Port > 65535 || !(binding.Encrypt == "NONE" || binding.Encrypt == "SSL" || binding.Encrypt == "TLS") || binding.Username == "" || binding.Password == "" || len(binding.Recipients) == 0 {
		return nil, ErrBinding
	}
	binding.Recipients = append([]string(nil), binding.Recipients...)
	seen := map[string]bool{}
	for _, recipient := range binding.Recipients {
		if recipient == "" || seen[recipient] {
			return nil, ErrBinding
		}
		seen[recipient] = true
	}
	if options.Timeout == 0 {
		options.Timeout = 60 * time.Second
	}
	if options.Timeout < 0 {
		return nil, ErrBinding
	}
	if options.TLSConfig != nil {
		if options.TLSConfig.InsecureSkipVerify {
			return nil, ErrBinding
		}
		options.TLSConfig = options.TLSConfig.Clone()
		if options.TLSConfig.RootCAs != nil {
			options.TLSConfig.RootCAs = options.TLSConfig.RootCAs.Clone()
		}
	} else {
		options.TLSConfig = &tls.Config{}
	}
	if options.TLSConfig.ServerName == "" {
		options.TLSConfig.ServerName = binding.Host
	}
	if options.TLSConfig.MinVersion == 0 {
		options.TLSConfig.MinVersion = tls.VersionTLS12
	}
	return &Adapter{binding, options}, nil
}
func portValue(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case json.Number:
		n, e := strconv.Atoi(string(x))
		return n, e == nil
	case string:
		n, e := strconv.Atoi(x)
		return n, e == nil
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}
func (a *Adapter) matches(r merge.SMTPRequest) bool {
	p, ok := portValue(r.Port)
	b := a.binding
	if !ok || r.Host != b.Host || p != b.Port || r.Encrypt != b.Encrypt || r.Username != b.Username || r.Password != b.Password || r.From != b.Username || r.FromName != b.FromName || len(r.To) != len(b.Recipients) {
		return false
	}
	seen := map[string]int{}
	for _, v := range b.Recipients {
		seen[v]++
	}
	for _, v := range r.To {
		seen[v]--
		if seen[v] < 0 {
			return false
		}
	}
	return true
}

// Send fits Merge's named SMTP dependency. The request must match an immutable
// host-granted binding before any dial; no endpoint, CA or recipient comes from
// worker data. It deliberately does not retry an uncertain send.
func (a *Adapter) Send(ctx context.Context, r merge.SMTPRequest) (err error) {
	if a == nil || ctx == nil || !a.matches(r) {
		return ErrBinding
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	p, e := prepare(r)
	if e != nil {
		return merge.SMTPQuitError{Err: e}
	}
	c, e := a.connect(ctx)
	if e != nil {
		return e
	}
	defer c.close()
	if r.Encrypt == "TLS" {
		if e = c.startTLS(ctx, a.options.TLSConfig); e != nil {
			return e
		}
	}
	if e = c.login(r.Username, r.Password); e != nil {
		return e
	}
	// __connect_smtp has now returned, assigning the source outer smtp variable.
	// Only from this point onward is finally smtp.quit() executed.
	defer func() {
		if _, _, e := c.command("quit", ""); e != nil {
			err = merge.SMTPQuitError{Err: e}
		}
		c.close()
	}()
	raw, e := p.serialize()
	if e != nil {
		return e
	}
	return c.sendmail(r.From, r.To, raw)
}
