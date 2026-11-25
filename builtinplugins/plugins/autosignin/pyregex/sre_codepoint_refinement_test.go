package pyregex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"light-media/builtinplugins/plugins/autosignin/regexengine"
)

type cpError struct {
	Class          string `json:"class"`
	Message        []rune `json:"message_codepoints"`
	Msg            []rune `json:"msg_codepoints"`
	Pos, Line, Col any
}
type cpMatch struct {
	Span   [2]int            `json:"span"`
	Whole  []rune            `json:"whole"`
	Groups [][]rune          `json:"groups"`
	Named  map[string][]rune `json:"groupdict"`
}
type cpOracle struct {
	Texts [][]rune `json:"text_codepoints"`
	Cases []struct {
		Pattern []rune          `json:"pattern_codepoints"`
		Flags   int             `json:"flags"`
		Error   json.RawMessage `json:"error"`
		Matches []*cpMatch      `json:"matches"`
	} `json:"cases"`
	JSON []struct {
		Raw     string     `json:"raw"`
		CP      []rune     `json:"codepoints"`
		Matches []*cpMatch `json:"matches"`
	} `json:"json_loads_cases"`
	Warnings []struct {
		ID    string `json:"id"`
		Steps []struct {
			Kind    string          `json:"kind"`
			Pattern []rune          `json:"pattern_codepoints"`
			Stderr  string          `json:"stderr"`
			Error   json.RawMessage `json:"error"`
		} `json:"steps"`
	} `json:"warning_scenarios"`
}

func readCPOracle(t *testing.T) cpOracle {
	t.Helper()
	raw, e := os.ReadFile("../testdata/source-oracles/autosignin-regex-codepoints-20261106/source-r3.json")
	if e != nil {
		t.Fatal(e)
	}
	var x cpOracle
	if e = json.Unmarshal(raw, &x); e != nil {
		t.Fatal(e)
	}
	return x
}
func errorCP(err error) any {
	if err == nil {
		return nil
	}
	e, ok := err.(*Error)
	if !ok {
		return map[string]any{"unexpected": err.Error()}
	}
	var msg any
	if s, ok := e.Msg.(string); ok {
		msg = sourceRunes(s)
	}
	return map[string]any{"class": e.Class, "message_codepoints": sourceRunes(e.Message), "msg_codepoints": msg, "pos": e.Pos, "lineno": e.Line, "colno": e.Column}
}
func cpEqual(t *testing.T, label string, a, b any) {
	t.Helper()
	aRaw, _ := json.Marshal(a)
	bRaw, _ := json.Marshal(b)
	var aa, bb any
	json.Unmarshal(aRaw, &aa)
	json.Unmarshal(bRaw, &bb)
	if !reflect.DeepEqual(aa, bb) {
		t.Fatalf("%s got%s want%s", label, aRaw, bRaw)
	}
}
func observedCP(t *testing.T, re *regexp2.Regexp, text []rune) *cpMatch {
	t.Helper()
	m, e := re.FindStringMatch(sourceText(text))
	if e != nil {
		t.Fatal(e)
	}
	quick, e := re.MatchString(sourceText(text))
	if e != nil || quick != (m != nil) {
		t.Fatalf("quick mismatch %v %v", quick, e)
	}
	direct, e := re.FindRunesMatch(text)
	if e != nil {
		t.Fatal(e)
	}
	if (m == nil) != (direct == nil) {
		t.Fatal("string/runes presence differs")
	}
	if m == nil {
		return nil
	}
	if !reflect.DeepEqual(sourceRunes(m.String()), m.Runes()) {
		t.Fatal("capture String dropped raw codepoints")
	}
	if !reflect.DeepEqual(sourceRunes(direct.String()), direct.Runes()) {
		t.Fatal("FindRunes capture String dropped raw codepoints")
	}
	out := &cpMatch{Span: [2]int{m.RuneIndex, m.RuneIndex + m.RuneLength}, Whole: append([]rune{}, m.Runes()...), Groups: [][]rune{}, Named: map[string][]rune{}}
	for _, id := range re.GetGroupNumbers()[1:] {
		g := m.GroupByNumber(id)
		var cp []rune
		if g != nil && len(g.Captures) > 0 {
			cp = append([]rune{}, g.Runes()...)
		}
		out.Groups = append(out.Groups, cp)
	}
	for _, name := range re.GetGroupNames() {
		if name != "0" && re.GroupNumberFromName(name) > 0 {
			id := re.GroupNumberFromName(name)
			if name != fmt.Sprint(id) {
				out.Named[name] = out.Groups[id-1]
			}
		}
	}
	return out
}
func TestSourceRawCodepointPatternsAndCaptures(t *testing.T) {
	x := readCPOracle(t)
	if len(x.Cases) != 44 || len(x.Texts) != 15 {
		t.Fatal("oracle counts")
	}
	for i, c := range x.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			re, e := CompileCodepoints(c.Pattern, c.Flags == IgnoreCase)
			cpEqual(t, "error", errorCP(e), c.Error)
			if e != nil {
				return
			}
			for k, txt := range x.Texts {
				cpEqual(t, fmt.Sprint("subject", k), observedCP(t, re, txt), c.Matches[k])
			}
		})
	}
}
func TestSourceJSONStringPairingAndLoneSurrogates(t *testing.T) {
	x := readCPOracle(t)
	for i, c := range x.JSON {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, e := DecodeSourceJSONString([]byte(c.Raw))
			if e != nil {
				t.Fatal(e)
			}
			cpEqual(t, "json loads codepoints", sourceRunes(s), c.CP)
			re, e := Compile(s, false)
			if e != nil {
				t.Fatal(e)
			}
			for k, txt := range x.Texts {
				cpEqual(t, fmt.Sprint(k), observedCP(t, re, txt), c.Matches[k])
			}
		})
	}
	for _, s := range []string{`NaN`, `"unterminated`, `{"a":1}`, "\"\xed\xa0\x80\""} {
		if _, e := DecodeSourceJSONString([]byte(s)); e == nil {
			t.Fatalf("accepted non-strict string %q", s)
		}
	}
}
func TestSourceDefaultWarningsAndTwoCaches(t *testing.T) {
	x := readCPOracle(t)
	for _, scenario := range x.Warnings {
		t.Run(scenario.ID, func(t *testing.T) {
			var output bytes.Buffer
			r := newSourceCompileRuntime(&output)
			for _, s := range scenario.Steps {
				if s.Kind == "evict" || s.Kind == "fifo-evict" {
					n := 513
					if s.Kind == "fifo-evict" {
						n = 256
					}
					for k := 0; k < n; k++ {
						if _, e := r.compile(fmt.Sprintf("owned_%d", k), 0, SourceOrigin{}); e != nil {
							t.Fatal(e)
						}
					}
					continue
				}
				origin := RetryOrigin
				if s.Kind == "base" {
					origin = SignInResultOrigin
				}
				output.Reset()
				_, e := r.compile(sourceText(s.Pattern), 0, origin)
				cpEqual(t, "error", errorCP(e), s.Error)
				if output.String() != s.Stderr {
					t.Fatalf("warning text/location/filter got%q want%q", output.String(), s.Stderr)
				}
			}
		})
	}
}
func TestSourceCompileRuntimeConcurrentCaptureIsolation(t *testing.T) {
	var out bytes.Buffer
	r := newSourceCompileRuntime(&out)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pattern := sourceText([]rune{'(', 0xd800 + rune(i%2), ')'})
			re, e := r.compile(pattern, 0, RetryOrigin)
			if e != nil {
				t.Error(e)
				return
			}
			for k := 0; k < 30; k++ {
				m, e := re.FindStringMatch(sourceText([]rune{0xd800 + rune(i%2)}))
				if e != nil || m == nil || !reflect.DeepEqual(sourceRunes(m.String()), []rune{0xd800 + rune(i%2)}) {
					t.Errorf("capture isolation %v %v", m, e)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// Source Error fields can themselves contain a surrogate. Compare codepoints
// before any ordinary Go JSON encoder can replace that private representation.
func compareSourceScalarError(t *testing.T, label string, err error, want json.RawMessage) {
	t.Helper()
	if bytes.Equal(bytes.TrimSpace(want), []byte("null")) {
		if err != nil {
			t.Fatalf("%s unexpected error %v", label, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("%s missing source error %s", label, want)
	}
	var raw struct {
		Class          string          `json:"class"`
		Message        json.RawMessage `json:"message"`
		Msg            json.RawMessage `json:"msg"`
		Pos, Line, Col any
	}
	// The fixture names for line/column differ from the field names.
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(want, &obj); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(want, &raw); e != nil {
		t.Fatal(e)
	}
	message, e := DecodeSourceJSONString(raw.Message)
	if e != nil {
		t.Fatal(e)
	}
	var msg any
	if string(raw.Msg) != "null" {
		s, e := DecodeSourceJSONString(raw.Msg)
		if e != nil {
			t.Fatal(e)
		}
		msg = sourceRunes(s)
	}
	var pos, line, col any
	json.Unmarshal(obj["pos"], &pos)
	json.Unmarshal(obj["lineno"], &line)
	json.Unmarshal(obj["colno"], &col)
	cpEqual(t, label, errorCP(err), map[string]any{"class": raw.Class, "message_codepoints": sourceRunes(message), "msg_codepoints": msg, "pos": pos, "lineno": line, "colno": col})
}
