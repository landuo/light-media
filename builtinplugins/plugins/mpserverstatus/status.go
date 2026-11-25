package mpserverstatus

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
	"sync"
	"time"
	"unicode/utf8"
)

const ServerURL = "https://movie-pilot.org/status"
const Version = "1.0.0"

type Object = map[string]any
type Clock func() float64

type Response struct {
	Status     int                    `json:"status"`
	Text       string                 `json:"text"`
	TextError  error                  `json:"-"`
	TextDecode func() (string, error) `json:"-"`
	Content    []byte                 `json:"content"`
	Headers    map[string]string      `json:"headers"`
	Protocol   int                    `json:"protocol"`
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
	mu                 sync.RWMutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Enable = truth(c["enable"])
	m.Last = nil
	m.DNSCache = nil
	m.TLSCache = nil
	return nil
}
func (m *Machine) State() bool { m.mu.RLock(); defer m.mu.RUnlock(); return m.Enable }
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
	if n := utf8.RuneCountInString(s); n > 4300 {
		panic(fmt.Errorf("Exceeds the limit (4300 digits) for integer string conversion: value has %d digits; use sys.set_int_max_str_digits() to increase the limit", n))
	}
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
func Parse(text string) (result Object, err error) {
	defer sourcePanic(&err)
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
	var counterValues []json.Number
	if counters != nil {
		for _, v := range counters {
			counterValues = append(counterValues, decimal(v))
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
	return Object{"active_connections": decimal(active[1]), "accepts": counterValues[0], "handled": counterValues[1], "requests": counterValues[2], "reading": decimal(rw[1]), "writing": decimal(rw[2]), "waiting": decimal(rw[3])}, nil
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
		if math.IsNaN(x) {
			panic(errors.New("cannot convert float NaN to integer"))
		}
		if math.IsInf(x, 0) {
			panic(errors.New("cannot convert float infinity to integer"))
		}
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
	if math.IsInf(f, 0) {
		panic(errors.New("integer division result too large for a float"))
	}
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
	v := number(jnumber(new(big.Int).Sub(integer(cur), integer(prev))))
	if math.IsInf(v, 0) {
		panic(errors.New("int too large to convert to float"))
	}
	return v / seconds
}
func (m *Machine) Metrics(status Object, seconds float64) (result Object, err error) {
	defer sourcePanic(&err)
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
	if m.last() != nil {
		elapsed := now - m.requiredLast().Timestamp
		if elapsed > 0 {
			out["sample_seconds"] = elapsed
			out["requests_rate"] = delta(total, m.requiredLast().Requests, elapsed)
			out["accepts_rate"] = delta(accepts, m.requiredLast().Accepts, elapsed)
			out["handled_rate"] = delta(handled, m.requiredLast().Handled, elapsed)
		}
	}
	m.mu.Lock()
	m.Last = &Sample{now, jnumber(integer(accepts)), jnumber(integer(handled)), jnumber(integer(total))}
	m.mu.Unlock()
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
	text := ""
	if response != nil {
		var e error
		text, e = response.sourceText()
		if e != nil {
			panic(e)
		}
	}
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
		n = len(text)
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
func (m *Machine) DNS(ctx context.Context) Object { return m.dns(ctx, m.Network) }
func (m *Machine) dns(ctx context.Context, network Network) Object {
	now := m.Now()
	if m.cached(false) != nil && now-m.requiredCache(false).At < 60 {
		return m.requiredCache(false).Info
	}
	host, port, err := m.hostPort()
	start := m.Now()
	ips := []string{}
	if err == nil {
		ips, err = network.DNS(ctx, host, port)
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
	m.mu.Lock()
	if ctx.Err() == nil {
		m.DNSCache = &cache{now, info}
	}
	m.mu.Unlock()
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
func (m *Machine) TLS(ctx context.Context) Object { return m.tls(ctx, m.Network) }
func (m *Machine) tls(ctx context.Context, network Network) Object {
	p, _ := url.Parse(m.URL)
	if p.Scheme != "https" {
		return Object{"enabled": false, "error": ""}
	}
	now := m.Now()
	if m.cached(true) != nil && now-m.requiredCache(true).At < 3600 {
		return m.requiredCache(true).Info
	}
	host, port, err := m.hostPort()
	start := m.Now()
	var cert Certificate
	if err == nil {
		cert, err = network.TLS(ctx, host, port, 5*time.Second)
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
	m.mu.Lock()
	if ctx.Err() == nil {
		m.TLSCache = &cache{now, info}
	}
	m.mu.Unlock()
	return info
}

type Snapshot struct {
	Metrics, Probe, DNS, TLS Object
	Error                    string
}

type Log func(context.Context, string, string) error

// LoadWith keeps network and logger scoped to this one admitted call. Ordinary
// source network errors remain displayable; revoked host scopes stop all work.
func (m *Machine) LoadWith(ctx context.Context, details bool, network Network, log Log) (out Snapshot, err error) {
	defer sourcePanic(&err)
	write := func(level, message string) error {
		if log != nil {
			return log(ctx, level, message)
		}
		return nil
	}
	start := m.Now()
	if err = write("INFO", "请求服务器状态 "+m.URL+"..."); err != nil {
		return out, err
	}
	response, requestErr := network.Status(ctx, m.URL, 20*time.Second)
	var signature LegacyStatusTypeError
	if errors.As(requestErr, &signature) {
		response, requestErr = network.Status(ctx, m.URL, 0)
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var boundary HostBoundaryError
	if errors.As(requestErr, &boundary) {
		return out, requestErr
	}
	seconds := m.Now() - start
	requestError := ""
	if requestErr != nil {
		requestError = requestErr.Error()
		if err = write("WARN", "请求服务器状态异常："+requestError); err != nil {
			return out, err
		}
	} else {
		if err = write("INFO", fmt.Sprintf("请求耗时：%.3f秒", seconds)); err != nil {
			return out, err
		}
		if response == nil {
			requestError = "网络错误"
			if err = write("WARN", "请求服务器状态失败：网络错误"); err != nil {
				return out, err
			}
		}
	}
	out = Snapshot{Probe: m.Probe(response, seconds, requestError), DNS: Object{}, TLS: Object{}}
	if details {
		out.DNS = m.dns(ctx, network)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.TLS = m.tls(ctx, network)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
	}
	if requestError != "" || response == nil || (response.Status >= 400 && response.Status < 600) {
		out.Error = requestError
		if out.Error == "" {
			out.Error = "无法连接服务器"
		}
		return out, nil
	}
	text, e := response.sourceText()
	if e == nil {
		var status Object
		status, e = Parse(text)
		if e == nil {
			out.Metrics, e = m.Metrics(status, seconds)
		}
	}
	if e != nil {
		if err = write("WARN", "解析服务器状态失败："+e.Error()); err != nil {
			return out, err
		}
		out.Error = "服务器状态格式异常：" + e.Error()
	}
	return out, nil
}
func (m *Machine) Load(ctx context.Context, details bool) Snapshot {
	out, _ := m.LoadWith(ctx, details, m.Network, nil)
	return out
}
func (m *Machine) Page(ctx context.Context) []any {
	if !m.State() {
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
	f := number(v)
	if math.IsNaN(f) {
		return "nan"
	}
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	return fmt.Sprintf("%.*f", precision, f)
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
		return FormatFloat(number(v)*1000, 0) + " ms"
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

func sourcePanic(err *error) {
	if p := recover(); p != nil {
		if e, ok := p.(error); ok {
			*err = e
		} else {
			*err = fmt.Errorf("%v", p)
		}
	}
}

// These are individual source member reads, not a whole snapshot/job lock.
func (m *Machine) last() *Sample { m.mu.RLock(); defer m.mu.RUnlock(); return m.Last }
func (m *Machine) requiredLast() *Sample {
	v := m.last()
	if v == nil {
		panic(errors.New("'NoneType' object has no attribute 'get'"))
	}
	return v
}
func (m *Machine) cached(tls bool) *cache {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tls {
		return m.TLSCache
	}
	return m.DNSCache
}
func (m *Machine) requiredCache(tls bool) *cache {
	v := m.cached(tls)
	if v == nil {
		panic(errors.New("'NoneType' object is not subscriptable"))
	}
	return v
}

type HostBoundaryError struct{ Err error }

func (e HostBoundaryError) Error() string { return e.Err.Error() }
func (e HostBoundaryError) Unwrap() error { return e.Err }

func (r *Response) sourceText() (string, error) {
	if r.TextDecode != nil {
		return r.TextDecode()
	}
	return r.Text, r.TextError
}
