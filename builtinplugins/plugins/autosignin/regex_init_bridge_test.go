package autosignin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/autosignin/pyregex"
)

func regexInitManifest() sdk.Manifest {
	m := Manifest()
	m.Methods = append(m.Methods, "owned_regex_inspect", "owned_regex_html")
	return m
}
func TestRegexInitOwnedWorkerProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "asi-regex-init-owned" {
		return
	}
	p := NewPlugin()
	h := func(ctx context.Context, m string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
		if m == "owned_regex_html" {
			var in struct {
				HTML, Pattern string
				Prefill       int
				Preload       bool
			}
			if e := json.Unmarshal(raw, &in); e != nil {
				return nil, e
			}
			logFile, e := os.CreateTemp("", "owned-regex-stderr-")
			if e != nil {
				return nil, e
			}
			defer os.Remove(logFile.Name())
			previous := os.Stderr
			os.Stderr = logFile
			defer func() { os.Stderr = previous; logFile.Close() }()
			if in.Preload {
				if _, e = VerifiedRetryMatch(in.Pattern, "a"); e != nil {
					return nil, e
				}
			}
			for i := 0; i < in.Prefill; i++ {
				if _, e = pyregex.Compile(fmt.Sprintf("OWNED_CACHE_%d", i), false); e != nil {
					return nil, e
				}
			}
			// Test-only synthetic installed pattern catalogue, not a production
			// API or permissive worker dispatcher. The actual HTML path runs.
			htmlData.Regexes["owned-codepoint-test"] = []string{in.Pattern}
			matched, e := SignInResult(in.HTML, []string{in.Pattern})
			if e != nil {
				return nil, e
			}
			if e = logFile.Sync(); e != nil {
				return nil, e
			}
			stderr, e := os.ReadFile(logFile.Name())
			if e != nil {
				return nil, e
			}
			return json.Marshal(Object{"matched": matched, "stderr": string(stderr)})
		}
		if m != "owned_regex_inspect" {
			return p.Handle(ctx, m, raw, host)
		}
		var textCP [][]rune
		if e := json.Unmarshal(raw, &textCP); e != nil {
			return nil, e
		}
		e, _ := p.frame(ctx, host, false)
		pat := e.field("RetryKeyword").(string)
		matches := []bool{}
		for _, cp := range textCP {
			value := ""
			for _, r := range cp {
				var b strings.Builder
				sourceWriteCodepoint(&b, r)
				value += b.String()
			}
			ok, err := VerifiedRetryMatch(pat, value)
			if err != nil {
				return nil, err
			}
			matches = append(matches, ok)
		}
		return json.Marshal(Object{"runtime_codepoints": sourceCodepoints(pat), "matches": matches})
	}
	if err := sdk.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, regexInitManifest(), h); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
func TestActualSDKInitRetryRawCodepointRestoration(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-oracles/autosignin-regex-codepoints-20261106/source-init-r1.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Subjects [][]rune `json:"subjects_codepoints"`
		Rows     []struct {
			Raw       string `json:"raw_pattern"`
			Runtime   []rune `json:"runtime_codepoints"`
			Saved     []rune `json:"saved_codepoints"`
			SavedJSON string `json:"saved_json"`
			Matches   []bool `json:"matches"`
		} `json:"rows"`
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	for i, row := range source.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			m := regexInitManifest()
			client, err := (sdk.Runner{Timeout: 5 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}).Start(ctx, sdk.Command{Path: exe, Args: []string{"-test.run=^TestRegexInitOwnedWorkerProcess$", "--", "asi-regex-init-owned"}}, m, sdk.SessionBinding{Identity: sdk.Identity{PluginID: m.PluginID, InstanceID: "OwnedInitCP", Version: m.Version}, Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Stop(context.Background())
			var saved json.RawMessage
			host := func(_ context.Context, _ sdk.Identity, method string, in json.RawMessage) (json.RawMessage, error) {
				switch method {
				case sdk.SitesReadCapability:
					return json.RawMessage(`{"schema_version":1,"items":[]}`), nil
				case sdk.SitesSourceCustomConfigCapability:
					return json.RawMessage(`{"schema_version":1,"config":{},"snapshot":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil
				case sdk.BaseGetConfig:
					return json.RawMessage(`{}`), nil
				case sdk.LogWriteCapability:
					return json.RawMessage(`null`), nil
				case sdk.BaseUpdateConfig:
					var input struct{ Config json.RawMessage }
					if e := json.Unmarshal(in, &input); e != nil {
						return nil, e
					}
					saved = append(json.RawMessage(nil), input.Config...)
					return json.RawMessage(`true`), nil
				default:
					return nil, fmt.Errorf("unexpected host method %s", method)
				}
			}
			if _, err = client.Call(ctx, "init", json.RawMessage(`{"enabled":false,"onlyonce":false,"retry_keyword":`+row.Raw+`}`), host); err != nil {
				t.Fatal(err)
			}
			if !json.Valid(saved) {
				t.Fatalf("invalid saved config %q", saved)
			}
			restored, err := sourceRestoreSavedJSON(saved)
			if err != nil {
				t.Fatal(err)
			}
			o := restored.(Object)
			if !reflect.DeepEqual(sourceCodepoints(o["retry_keyword"].(string)), row.Saved) {
				t.Fatalf("saved config codepoints %v want%v", sourceCodepoints(o["retry_keyword"].(string)), row.Saved)
			}
			want, e := sourceRestoreSavedJSON([]byte(row.SavedJSON))
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(restored, want) {
				t.Fatalf("saved semantic config got%#v want%#v", restored, want)
			}
			subjects, _ := json.Marshal(source.Subjects)
			out, err := client.Call(ctx, "owned_regex_inspect", subjects, host)
			if err != nil {
				t.Fatal(err)
			}
			var observed struct {
				Runtime []rune `json:"runtime_codepoints"`
				Matches []bool `json:"matches"`
			}
			if err = json.Unmarshal(out.Result, &observed); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(observed.Runtime, row.Runtime) || !reflect.DeepEqual(observed.Matches, row.Matches) {
				t.Fatalf("runtime/VM got%+v want%v %v", observed, row.Runtime, row.Matches)
			}
		})
	}
}
func TestInitBridgeDefaultDecodeControl(t *testing.T) {
	// No surrogate escape: the existing decoder's number/object behavior remains.
	for _, raw := range []string{`{"enabled":false,"retry_keyword":"ordinary","n":1e400}`, `null`, `{}`, `{"retry_keyword":"\\ud800"}`} {
		var want any
		if e := Decode([]byte(raw), &want); e != nil {
			t.Fatal(e)
		}
		got, e := sourceRestoreSavedJSON([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("normal decode differs %s", raw)
		}
	}
	if _, e := pyregex.DecodeSourceJSONString([]byte(`"\ud800"`)); e != nil {
		t.Fatal(e)
	}
}

func TestActualSDKSourceHTMLFourRegexesAndWarningEviction(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-oracles/autosignin-regex-codepoints-20261106/source-html-r1.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Rows []struct {
			ID, HTML, Pattern string
			Prefill           int
			Preload, Result   bool
			Stderr            string
		} `json:"rows"`
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	for _, row := range source.Rows {
		t.Run(row.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			m := regexInitManifest()
			client, err := (sdk.Runner{Timeout: 5 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}).Start(ctx, sdk.Command{Path: exe, Args: []string{"-test.run=^TestRegexInitOwnedWorkerProcess$", "--", "asi-regex-init-owned"}}, m, sdk.SessionBinding{Identity: sdk.Identity{PluginID: m.PluginID, InstanceID: "OwnedHTML", Version: m.Version}, Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Stop(context.Background())
			payload, _ := json.Marshal(Object{"HTML": row.HTML, "Pattern": row.Pattern, "Prefill": row.Prefill, "Preload": row.Preload})
			result, err := client.Call(ctx, "owned_regex_html", payload, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Matched bool
				Stderr  string
			}
			if err = json.Unmarshal(result.Result, &got); err != nil {
				t.Fatal(err)
			}
			if got.Matched != row.Result || got.Stderr != row.Stderr {
				t.Fatalf("actual HTML/method stderr got%v %q want%v %q", got.Matched, got.Stderr, row.Result, row.Stderr)
			}
		})
	}
}
