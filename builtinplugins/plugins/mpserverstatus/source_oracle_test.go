package mpserverstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"

	"time"
)

type fakeNetwork struct {
	now   *float64
	step  Object
	calls []any
}

func (f *fakeNetwork) advance(key string, fallback float64) {
	v, ok := f.step[key]
	if !ok {
		v = fallback
	}
	*f.now += number(v)
}
func (f *fakeNetwork) Status(_ context.Context, target string, timeout time.Duration) (*Response, error) {
	var timeoutValue any = nil
	if timeout != 0 {
		timeoutValue = int(timeout / time.Second)
	}
	f.calls = append(f.calls, Object{"op": "status", "url": target, "timeout": timeoutValue})
	if truth(f.step["type_error_once"]) && timeout != 0 {
		return nil, LegacyStatusTypeError{}
	}
	f.advance("duration", .125)
	if truth(f.step["http_error"]) {
		return nil, errors.New(fmt.Sprint(f.step["http_error"]))
	}
	value, ok := f.step["response"].(map[string]any)
	if !ok {
		return nil, nil
	}
	r := &Response{Status: 200, Protocol: 11, Headers: map[string]string{}}
	if value["status"] != nil {
		r.Status = int(number(value["status"]))
	}
	r.Text, _ = value["text"].(string)
	r.Content = []byte(r.Text)
	if content, ok := value["content"].(string); ok {
		r.Content = []byte(content)
	}
	if value["protocol"] != nil {
		r.Protocol = int(number(value["protocol"]))
	}
	if h, ok := value["headers"].(map[string]any); ok {
		for k, v := range h {
			r.Headers[k] = fmt.Sprint(v)
		}
	}
	return r, nil
}
func (f *fakeNetwork) DNS(_ context.Context, host string, port int) ([]string, error) {
	f.calls = append(f.calls, Object{"op": "dns", "host": host, "port": port})
	f.advance("dns_seconds", .02)
	if truth(f.step["dns_error"]) {
		return nil, errors.New(fmt.Sprint(f.step["dns_error"]))
	}
	ips, ok := f.step["dns"]
	if !ok {
		ips = []string{"192.0.2.1", "2001:db8::1", "192.0.2.1"}
	}
	return stringList(ips), nil
}
func certNames(value any, fallback [][][2]string) [][][2]string {
	if value == nil {
		return fallback
	}
	out := [][][2]string{}
	for _, group := range value.([]any) {
		g := [][2]string{}
		for _, item := range group.([]any) {
			pair := item.([]any)
			g = append(g, [2]string{fmt.Sprint(pair[0]), fmt.Sprint(pair[1])})
		}
		out = append(out, g)
	}
	return out
}
func (f *fakeNetwork) TLS(_ context.Context, host string, port int, timeout time.Duration) (Certificate, error) {
	f.calls = append(f.calls, Object{"op": "tls", "host": host, "port": port, "timeout": int(timeout / time.Second)})
	f.advance("tls_seconds", .03)
	f.calls = append(f.calls, Object{"op": "tls_wrap", "host": host})
	if truth(f.step["tls_error"]) {
		return Certificate{}, errors.New(fmt.Sprint(f.step["tls_error"]))
	}
	data, _ := f.step["tls"].(map[string]any)
	cert := Certificate{Version: "TLSv1.3", NotAfter: "Nov 30 00:00:00 2026 GMT", Issuer: certNames(data["issuer"], [][][2]string{{{"organizationName", "Fallback CA"}, {"commonName", "Synthetic CA"}}}), Subject: certNames(data["subject"], [][][2]string{{{"commonName", "movie-pilot.org"}}})}
	if v, ok := data["version"].(string); ok {
		cert.Version = v
	}
	if v, ok := data["not_after"].(string); ok {
		cert.NotAfter = v
	}
	return cert, nil
}

func canonical(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out any
	if err = decode(raw, &out); err != nil {
		panic(err)
	}
	return out
}
func equivalent(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		p, ok := new(big.Rat).SetString(string(x))
		if !ok {
			return false
		}
		q, ok := new(big.Rat).SetString(string(y))
		return ok && p.Cmp(q) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equivalent(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			other, ok := y[k]
			if !ok || !equivalent(v, other) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}
func callFormat(method string, a []any) any {
	switch method {
	case "_format_http_version":
		return HTTPVersion(int(number(a[0])))
	case "_format_response_date":
		if a[0] == nil {
			return ResponseDate("")
		}
		return ResponseDate(fmt.Sprint(a[0]))
	case "_format_integer":
		return FormatInteger(a[0])
	case "_format_float":
		precision := 2
		if len(a) > 1 {
			precision = int(number(a[1]))
		}
		return FormatFloat(a[0], precision)
	case "_format_percent":
		return FormatPercent(a[0])
	case "_format_seconds":
		return FormatSeconds(a[0])
	case "_format_rate":
		return FormatRate(a[0], fmt.Sprint(a[1]))
	case "_format_sample_caption":
		return FormatSample(a[0])
	case "_format_size":
		return FormatSize(a[0])
	case "_format_ips":
		var err any
		if len(a) > 1 {
			err = a[1]
		}
		return FormatIPs(a[0], err)
	case "_safe_percent":
		return ratio(a[0], a[1], 100)
	case "_safe_ratio":
		return ratio(a[0], a[1], 1)
	case "_safe_delta_rate":
		return delta(a[0], a[1], number(a[2]))
	case "_severity_color":
		return Severity(number(a[0]), number(a[1]), number(a[2]))
	case "_latency_color":
		return Latency(a[0], truth(a[1]))
	case "_extract_certificate_name":
		return CertificateName(certNames(a[0], nil))
	case "_format_connection_detail":
		return FormatInteger(a[0]) + " / " + FormatPercent(a[1])
	case "_format_tls_days":
		return FormatTLSDays(a[0].(map[string]any))
	case "_tls_color":
		return TLSColor(a[0].(map[string]any))
	}
	panic("unsupported format " + method)
}
func oracleRuntime(data Object) (any, error) {
	now := 1000.0
	network := &fakeNetwork{now: &now}
	machine := New(network, func() float64 { return now })
	config, ok := data["config"]
	if !ok {
		config = Object{"enable": true}
	}
	raw, _ := json.Marshal(config)
	if err := machine.Init(raw); err != nil {
		return nil, err
	}
	if target, ok := data["url"].(string); ok {
		machine.URL = target
	}
	out := []any{}
	for _, item := range data["steps"].([]any) {
		step := item.(map[string]any)
		network.step = step
		network.calls = []any{}
		now = 1792152000
		if at, ok := step["at"]; ok {
			now = number(at)
		}
		var result any
		switch step["action"] {
		case "init":
			raw, _ := json.Marshal(step["config"])
			if err := machine.Init(raw); err != nil {
				return nil, err
			}
		case "page":
			result = machine.Page(context.Background())
		case "dashboard":
			result = machine.Dashboard(context.Background())
		default:
			result = machine.Load(context.Background(), step["action"] == "load_details")
		}
		var last any
		if machine.Last != nil {
			copy := *machine.Last
			last = copy
		}
		out = append(out, Object{"result": result, "calls": append([]any{}, network.calls...), "last_sample": last, "dns_cached": machine.DNSCache != nil, "tls_cached": machine.TLSCache != nil})
	}
	return out, nil
}
func runCase(kind string, input any) (any, error) {
	switch kind {
	case "parse":
		return Parse(input.(string))
	case "form":
		raw, _ := json.Marshal(input)
		m := New(nil, nil)
		if err := m.Init(raw); err != nil {
			return nil, err
		}
		form := Form(m.Enable)
		form["state"] = m.State()
		form["command"] = []any{}
		form["api"] = []any{}
		return form, nil
	case "format":
		data := input.(map[string]any)
		return callFormat(data["method"].(string), data["args"].([]any)), nil
	case "metrics":
		now := 0.0
		m := New(nil, func() float64 { return now })
		out := []any{}
		for _, item := range input.([]any) {
			sample := item.(map[string]any)
			now = number(sample["at"])
			result, err := m.Metrics(sample["status"].(map[string]any), number(sample["seconds"]))
			if err != nil {
				return nil, err
			}
			out = append(out, result)
		}
		return out, nil
	case "runtime":
		return oracleRuntime(input.(map[string]any))
	}
	return nil, errors.New("unsupported oracle kind")
}

func TestInstalledASTOracle(t *testing.T) {
	raw, err := os.ReadFile("reference/source-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Kind      string
			Input, Expected any
			Error           string
		}
	}
	if err := decode(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			actual, err := runCase(c.Kind, c.Input)
			if c.Error != "" {
				if err == nil || err.Error() != c.Error {
					t.Fatalf("expected error %q got %v", c.Error, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := canonical(actual)
			if !equivalent(c.Expected, got) {
				wantRaw, _ := json.Marshal(c.Expected)
				gotRaw, _ := json.Marshal(got)
				t.Fatalf("original AST differs\nwant %s\ngot  %s", wantRaw, gotRaw)
			}
		})
	}
}
