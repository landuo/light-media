package serverstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const ServerURL = "https://movie-pilot.org/status"
const Version = "1.4"

type Object = map[string]any
type Clock func() float64

type Response struct {
	Status   int               `json:"status"`
	Text     string            `json:"text"`
	Content  []byte            `json:"content"`
	Headers  map[string]string `json:"headers"`
	Protocol int               `json:"protocol"`
}
type Certificate struct {
	Version         string
	Issuer, Subject [][][2]string
	NotAfter        string
}

// Network is a trusted fixed-target host boundary. No handler accepts a URL,
// DNS hostname, timeout, TLS policy or shell command from its worker input.
type Network interface {
	Status(context.Context, string, time.Duration) (*Response, error)
	DNS(context.Context, string, int) ([]string, error)
	TLS(context.Context, string, int, time.Duration) (Certificate, error)
}
type LegacyStatusTypeError struct{}

func (LegacyStatusTypeError) Error() string { return "synthetic signature" }

type Sample struct {
	Timestamp float64     `json:"timestamp"`
	Accepts   json.Number `json:"accepts"`
	Handled   json.Number `json:"handled"`
	Requests  json.Number `json:"requests"`
}
type cache struct {
	At   float64
	Info Object
}
type Machine struct {
	Enable             bool
	URL                string
	Now                Clock
	Network            Network
	Last               *Sample
	DNSCache, TLSCache *cache
}

func New(network Network, now Clock) *Machine {
	if now == nil {
		now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	return &Machine{URL: ServerURL, Network: network, Now: now}
}
func decode(raw json.RawMessage, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(dst)
}
func truth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
func (m *Machine) Init(raw json.RawMessage) error {
	var config any
	if len(raw) > 0 {
		if err := decode(raw, &config); err != nil {
			return err
		}
	}
	if !truth(config) {
		config = Object{}
	}
	c, ok := config.(map[string]any)
	if !ok {
		kind := "str"
		switch v := config.(type) {
		case json.Number:
			kind = "int"
			if strings.ContainsAny(string(v), ".eE") {
				kind = "float"
			}
		case bool:
			kind = "bool"
		case []any:
			kind = "list"
		}
		return fmt.Errorf("'%s' object has no attribute 'get'", kind)
	}
	m.Enable = truth(c["enable"])
	m.Last = nil
	m.DNSCache = nil
	m.TLSCache = nil
	return nil
}
func (m *Machine) State() bool { return m.Enable }
func (m *Machine) Stop()       {}
func Form(enable bool) Object {
	return Object{"conf": []any{Object{"component": "VForm", "content": []any{Object{"component": "VRow", "content": []any{Object{"component": "VCol", "props": Object{"cols": 12, "md": 6}, "content": []any{Object{"component": "VSwitch", "props": Object{"model": "enable", "label": "启用插件"}}}}}}}}}, "model": Object{"enable": enable}}
}

var activeRE = regexp.MustCompile(pythonCaseLiteral("Active connections:") + pySpace + `*(` + pyDigit + `+)`)
var readWriteRE = regexp.MustCompile(pythonCaseLiteral("Reading:") + pySpace + `*(` + pyDigit + `+)` + pySpace + `+` + pythonCaseLiteral("Writing:") + pySpace + `*(` + pyDigit + `+)` + pySpace + `+` + pythonCaseLiteral("Waiting:") + pySpace + `*(` + pyDigit + `+)`)
var digitsRE = regexp.MustCompile(pyDigit + `+`)

// Python regex \d and int both accept Unicode decimal digits. Counters remain
// arbitrary precision rather than overflowing int64 or losing JSON integers.
func decimal(s string) json.Number {
	var out strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
			continue
		}
		for _, zero := range pythonDecimalZeroes {
			if r >= zero && r <= zero+9 {
				out.WriteByte(byte('0' + r - zero))
				break
			}
		}
	}
	n := new(big.Int)
	n.SetString(out.String(), 10)
	return json.Number(n.String())
}
func Parse(text string) (Object, error) {
	lines := []string{}
	for _, s := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == 0x1c || r == 0x1d || r == 0x1e || r == 0x85 || r == 0x2028 || r == 0x2029
	}) {
		if s = strings.TrimFunc(s, pythonSpace); s != "" {
			lines = append(lines, s)
		}
	}
	joined := strings.Join(lines, "\n")
	active, rw := activeRE.FindStringSubmatch(joined), readWriteRE.FindStringSubmatch(joined)
	var counters []string
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), "server accepts") && i+1 < len(lines) {
			n := digitsRE.FindAllString(lines[i+1], -1)
			if len(n) >= 3 {
				counters = n[:3]
				break
			}
		}
	}
	if counters == nil {
		for _, line := range lines {
			n := digitsRE.FindAllString(line, -1)
			if len(n) == 3 && !strings.Contains(line, "Reading") && !strings.Contains(line, "Writing") {
				counters = n
				break
			}
		}
	}
	if active == nil {
		return nil, errors.New("缺少活跃连接数据")
	}
	if counters == nil {
		return nil, errors.New("缺少连接计数数据")
	}
	if rw == nil {
		return nil, errors.New("缺少读写等待数据")
	}
	return Object{"active_connections": decimal(active[1]), "accepts": decimal(counters[0]), "handled": decimal(counters[1]), "requests": decimal(counters[2]), "reading": decimal(rw[1]), "writing": decimal(rw[2]), "waiting": decimal(rw[3])}, nil
}
func number(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case json.Number:
		f, _ := x.Float64()
		return f
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	}
	return 0
}
func integer(v any) *big.Int {
	n := new(big.Int)
	switch x := v.(type) {
	case json.Number:
		if _, ok := n.SetString(string(x), 10); !ok {
			f, _, err := big.ParseFloat(string(x), 10, 53, big.ToNearestEven)
			if err == nil {
				f.Int(n)
			}
		}
	case int:
		n.SetInt64(int64(x))
	case float64:
		big.NewFloat(x).Int(n)
	case bool:
		if x {
			n.SetInt64(1)
		}
	}
	return n
}
func jnumber(n *big.Int) json.Number { return json.Number(n.String()) }
func get(o Object, key string) any {
	if v, ok := o[key]; ok {
		return v
	}
	return json.Number("0")
}
func ratio(v, t any, multiplier float64) any {
	if !truth(t) {
		return nil
	}
	if !pythonInteger(v) || !pythonInteger(t) {
		return number(v) / number(t) * multiplier
	}
	f, _ := new(big.Rat).SetFrac(integer(v), integer(t)).Float64()
	return f * multiplier
}
func pythonInteger(v any) bool {
	switch n := v.(type) {
	case json.Number:
		return !strings.ContainsAny(string(n), ".eE")
	case int, int64, bool:
		return true
	}
	return false
}
func delta(cur, prev any, seconds float64) any {
	if !pythonInteger(cur) || !pythonInteger(prev) {
		if seconds <= 0 || number(cur) < number(prev) {
			return nil
		}
		return (number(cur) - number(prev)) / seconds
	}
	if seconds <= 0 || integer(cur).Cmp(integer(prev)) < 0 {
		return nil
	}
	return number(jnumber(new(big.Int).Sub(integer(cur), integer(prev)))) / seconds
}
func (m *Machine) Metrics(status Object, seconds float64) (Object, error) {
	now := m.Now()
	active, accepts, handled, total := get(status, "active_connections"), get(status, "accepts"), get(status, "handled"), get(status, "requests")
	reading, writing, waiting := get(status, "reading"), get(status, "writing"), get(status, "waiting")
	busy := jnumber(new(big.Int).Add(integer(reading), integer(writing)))
	drop := new(big.Int).Sub(integer(accepts), integer(handled))
	if drop.Sign() < 0 {
		drop.SetInt64(0)
	}
	dropped := jnumber(drop)
	out := Object{}
	for k, v := range status {
		out[k] = v
	}
	for k, v := range (Object{"seconds": seconds, "busy": busy, "dropped": dropped, "busy_percent": ratio(busy, active, 100), "reading_percent": ratio(reading, active, 100), "writing_percent": ratio(writing, active, 100), "waiting_percent": ratio(waiting, active, 100), "dropped_percent": ratio(dropped, accepts, 100), "requests_per_connection": ratio(total, handled, 1), "requests_rate": nil, "accepts_rate": nil, "handled_rate": nil, "sample_seconds": nil}) {
		out[k] = v
	}
	if m.Last != nil {
		elapsed := now - m.Last.Timestamp
		if elapsed > 0 {
			out["sample_seconds"] = elapsed
			out["requests_rate"] = delta(total, m.Last.Requests, elapsed)
			out["accepts_rate"] = delta(accepts, m.Last.Accepts, elapsed)
			out["handled_rate"] = delta(handled, m.Last.Handled, elapsed)
		}
	}
	for _, v := range out {
		if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
			return nil, errors.New("integer division result too large for a float")
		}
	}
	m.Last = &Sample{now, jnumber(integer(accepts)), jnumber(integer(handled)), jnumber(integer(total))}
	return out, nil
}
func header(h map[string]string, k string) string {
	for key, v := range h {
		if strings.EqualFold(key, k) {
			return v
		}
	}
	return ""
}
func dash(v any) any {
	if truth(v) {
		return v
	}
	return "-"
}
func (m *Machine) Probe(response *Response, seconds float64, requestError string) Object {
	host, _ := url.Parse(m.URL)
	out := Object{"url": m.URL, "host": dash(host.Hostname()), "status_code": nil, "ok": false, "seconds": seconds, "protocol": "-", "server": "-", "date": "-", "content_type": "-", "content_bytes": 0, "error": requestError}
	if response == nil {
		return out
	}
	out["status_code"] = response.Status
	out["ok"] = response.Status >= 200 && response.Status < 400 && requestError == ""
	out["protocol"] = HTTPVersion(response.Protocol)
	out["server"] = dash(header(response.Headers, "Server"))
	out["date"] = ResponseDate(header(response.Headers, "Date"))
	out["content_type"] = dash(header(response.Headers, "Content-Type"))
	n := len(response.Content)
	if n == 0 {
		n = len(response.Text)
	}
	out["content_bytes"] = n
	return out
}
func (m *Machine) hostPort() (string, int, error) {
	p, err := url.Parse(m.URL)
	if err != nil {
		return "", 0, err
	}
	port := 80
	if p.Scheme == "https" {
		port = 443
	}
	if p.Port() != "" {
		port, err = strconv.Atoi(p.Port())
	}
	return p.Hostname(), port, err
}
func (m *Machine) DNS(ctx context.Context) Object {
	now := m.Now()
	if m.DNSCache != nil && now-m.DNSCache.At < 60 {
		return m.DNSCache.Info
	}
	host, port, err := m.hostPort()
	start := m.Now()
	ips := []string{}
	if err == nil {
		ips, err = m.Network.DNS(ctx, host, port)
	}
	unique := []string{}
	for _, ip := range ips {
		found := false
		for _, old := range unique {
			if old == ip {
				found = true
				break
			}
		}
		if !found {
			unique = append(unique, ip)
		}
	}
	message := ""
	if err != nil {
		unique = []string{}
		message = err.Error()
	}
	info := Object{"host": host, "port": port, "ips": unique, "seconds": m.Now() - start, "error": message}
	m.DNSCache = &cache{now, info}
	return info
}
func CertificateName(items [][][2]string) string {
	for _, g := range items {
		for _, p := range g {
			if p[0] == "commonName" {
				return p[1]
			}
		}
	}
	for _, g := range items {
		for _, p := range g {
			if p[1] != "" {
				return p[1]
			}
		}
	}
	return "-"
}
func (m *Machine) TLS(ctx context.Context) Object {
	p, _ := url.Parse(m.URL)
	if p.Scheme != "https" {
		return Object{"enabled": false, "error": ""}
	}
	now := m.Now()
	if m.TLSCache != nil && now-m.TLSCache.At < 3600 {
		return m.TLSCache.Info
	}
	host, port, err := m.hostPort()
	start := m.Now()
	var cert Certificate
	if err == nil {
		cert, err = m.Network.TLS(ctx, host, port, 5*time.Second)
	}
	info := Object{"enabled": true, "version": "-", "issuer": "-", "subject": "-", "expires_at": "-", "days_left": nil, "seconds": 0, "error": ""}
	if err == nil {
		var expires time.Time
		expires, err = time.Parse("Jan _2 15:04:05 2006 MST", cert.NotAfter)
		if err == nil {
			info["version"] = dash(cert.Version)
			info["issuer"] = CertificateName(cert.Issuer)
			info["subject"] = CertificateName(cert.Subject)
			info["expires_at"] = expires.UTC().Format("2006-01-02 15:04 UTC")
			info["days_left"] = int(math.Floor((float64(expires.Unix()) - m.Now()) / 86400))
		}
	}
	info["seconds"] = m.Now() - start
	if err != nil {
		info["error"] = err.Error()
	}
	m.TLSCache = &cache{now, info}
	return info
}

type Snapshot struct {
	Metrics, Probe, DNS, TLS Object
	Error                    string
}

func (m *Machine) Load(ctx context.Context, networkDetails bool) Snapshot {
	start := m.Now()
	response, err := m.Network.Status(ctx, m.URL, 20*time.Second)
	var signature LegacyStatusTypeError
	if errors.As(err, &signature) {
		response, err = m.Network.Status(ctx, m.URL, 0)
	}
	seconds := m.Now() - start
	requestError := ""
	if err != nil {
		requestError = err.Error()
	} else if response == nil {
		requestError = "网络错误"
	}
	out := Snapshot{Probe: m.Probe(response, seconds, requestError), DNS: Object{}, TLS: Object{}}
	if networkDetails {
		out.DNS = m.DNS(ctx)
		out.TLS = m.TLS(ctx)
	}
	if requestError != "" || response == nil || (response.Status >= 400 && response.Status < 600) {
		out.Error = requestError
		if out.Error == "" {
			out.Error = "无法连接服务器"
		}
		return out
	}
	status, err := Parse(response.Text)
	if err == nil {
		out.Metrics, err = m.Metrics(status, seconds)
	}
	if err != nil {
		out.Error = "服务器状态格式异常：" + err.Error()
	}
	return out
}
func (m *Machine) Page(ctx context.Context) []any {
	if !m.Enable {
		return []any{Object{"component": "div", "text": "插件未启用", "props": Object{"class": "text-center"}}}
	}
	s := m.Load(ctx, true)
	return Elements(s, false)
}
func (m *Machine) Dashboard(ctx context.Context) Object {
	s := m.Load(ctx, false)
	return Object{"cols": Object{"cols": 12, "md": 8}, "attrs": Object{"refresh": 10}, "elements": Elements(s, true)}
}

func HTTPVersion(v int) string {
	switch v {
	case 10:
		return "HTTP/1.0"
	case 11:
		return "HTTP/1.1"
	case 20:
		return "HTTP/2"
	}
	return "-"
}
func ResponseDate(v string) string {
	if v == "" {
		return "-"
	}
	for _, format := range []string{time.RFC1123, time.RFC1123Z, time.RFC822, time.RFC822Z, time.ANSIC} {
		if d, e := time.Parse(format, v); e == nil {
			_, offset := d.Zone()
			zone := "UTC"
			if offset != 0 {
				sign := "+"
				if offset < 0 {
					sign = "-"
					offset = -offset
				}
				zone = fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, offset%3600/60)
			}
			return d.Format("2006-01-02 15:04:05 ") + zone
		}
	}
	return v
}
func FormatInteger(v any) string {
	if v == nil {
		return "-"
	}
	s := integer(v).String()
	start := 0
	if strings.HasPrefix(s, "-") {
		start = 1
	}
	for i := len(s) - 3; i > start; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func FormatFloat(v any, precision int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.*f", precision, number(v))
}
func FormatPercent(v any) string {
	if v == nil {
		return "-"
	}
	return FormatFloat(v, 1) + "%"
}
func FormatSeconds(v any) string {
	if v == nil {
		return "-"
	}
	if number(v) < 1 {
		return fmt.Sprintf("%.0f ms", number(v)*1000)
	}
	return FormatFloat(v, 2) + " 秒"
}
func FormatRate(v any, unit string) string {
	if v == nil {
		return "待刷新"
	}
	return FormatFloat(v, 2) + " " + unit
}
func FormatSample(v any) string {
	if v == nil {
		return "等待下一次采样"
	}
	return FormatFloat(v, 1) + " 秒窗口"
}
func FormatSize(v any) string {
	n := number(v)
	if n < 1024 {
		return fmt.Sprint(v) + " B"
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", n/1024)
	}
	return fmt.Sprintf("%.1f MB", n/1024/1024)
}
func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := []string{}
		for _, v := range x {
			out = append(out, fmt.Sprint(v))
		}
		return out
	}
	return nil
}
func FormatIPs(v any, err any) string {
	if truth(err) {
		return fmt.Sprint(err)
	}
	ips := stringList(v)
	if len(ips) == 0 {
		return "-"
	}
	suffix := ""
	if len(ips) > 3 {
		suffix = " ..."
		ips = ips[:3]
	}
	return strings.Join(ips, ", ") + suffix
}
func FormatTLSDays(t Object) string {
	if !truth(t["enabled"]) {
		return "未启用"
	}
	if truth(t["error"]) {
		return "异常"
	}
	if t["days_left"] == nil {
		return "-"
	}
	return fmt.Sprint(t["days_left"]) + " 天"
}
func Severity(v, w, e float64) string {
	if e >= w {
		if v >= e {
			return "error"
		}
		if v >= w {
			return "warning"
		}
		return "success"
	}
	if v <= e {
		return "error"
	}
	if v <= w {
		return "warning"
	}
	return "success"
}
func Latency(v any, ok bool) string {
	if !ok {
		return "error"
	}
	if v == nil {
		return "secondary"
	}
	if number(v) < 1 {
		return "success"
	}
	if number(v) < 3 {
		return "warning"
	}
	return "error"
}
func TLSColor(t Object) string {
	if !truth(t["enabled"]) {
		return "secondary"
	}
	if truth(t["error"]) {
		return "error"
	}
	if t["days_left"] == nil || number(t["days_left"]) < 7 {
		return "error"
	}
	if number(t["days_left"]) < 30 {
		return "warning"
	}
	return "success"
}
