package pyregex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func boundaryManifest() sdk.Manifest {
	return sdk.Manifest{SchemaVersion: 1, PluginID: "ASIRegexBoundaryOwned", Name: "Owned regex JSON boundary", Version: "1", Runtime: "go-executable", Entry: "owned-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"boundary", "stop"}}
}
func TestSourceWorkerInputDecodeProcess(t *testing.T) {
	fixture := false
	for _, arg := range os.Args {
		if arg == "asi-regex-boundary-fixture" {
			fixture = true
		}
	}
	if !fixture {
		return
	}
	handle := func(_ context.Context, method string, raw json.RawMessage, _ sdk.HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			return json.RawMessage(`null`), nil
		}
		var in struct {
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Raw        json.RawMessage `json:"raw"`
			Codepoints []rune          `json:"decoded_pattern_codepoints"`
		}{raw, []rune(in.Pattern)})
	}
	if err := sdk.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, boundaryManifest(), handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
func TestActualSDKRawJSONSurrogateBoundary(t *testing.T) {
	raw, err := os.ReadFile("../testdata/source-oracles/autosignin-regex-frontend-20261106/scalar-boundary-source-r1.stdout")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			PatternJSON string `json:"pattern_json"`
			PatternCP   []rune `json:"pattern_codepoints"`
			Flags       int    `json:"flags"`
		}
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := boundaryManifest()
	client, err := (sdk.Runner{ConcurrentCalls: true, RequireConcurrent: true, Timeout: 5 * time.Second}).Start(ctx, sdk.Command{Path: exe, Args: []string{"-test.run=^TestSourceWorkerInputDecodeProcess$", "--", "asi-regex-boundary-fixture"}}, m, sdk.SessionBinding{Identity: sdk.Identity{PluginID: m.PluginID, InstanceID: m.PluginID, Version: m.Version}, Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		if err := client.Stop(stop); err != nil {
			t.Error(err)
		}
	}()
	unsupported, scalar := 0, 0
	for _, c := range source.Cases {
		if c.Flags != 0 {
			continue
		}
		if !hasSurrogates(c.PatternCP) && !(len(c.PatternCP) == 1 && c.PatternCP[0] >= 0x10000) {
			continue
		}
		input := json.RawMessage(`{"pattern":` + c.PatternJSON + `}`)
		out, err := client.CallWithOptions(ctx, "boundary", input, nil, sdk.CallOptions{CallerDeadlineOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		if out.HostCalls != 0 {
			t.Fatal("boundary probe used host resources")
		}
		var result struct {
			Raw        json.RawMessage `json:"raw"`
			Codepoints []rune          `json:"decoded_pattern_codepoints"`
		}
		if err = json.Unmarshal(out.Result, &result); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(canonical(result.Raw), canonical(input)) {
			t.Fatal("raw JSON changed before plugin decoder")
		}
		want := append([]rune(nil), c.PatternCP...)
		for i, r := range want {
			if r >= 0xd800 && r <= 0xdfff {
				want[i] = 0xfffd
			}
		}
		if !reflect.DeepEqual(want, result.Codepoints) {
			t.Fatalf("unexpected actual SDK+Go decode result source%v got%v want%v", c.PatternCP, result.Codepoints, want)
		}
		if hasSurrogates(c.PatternCP) {
			unsupported++
		} else {
			scalar++
		}
		observation, _ := json.Marshal(map[string]any{"classification": "actual SDK98 separate process + exact ordinary Go string decode observation; unsupported raw surrogate input is not source-equivalent", "source_pattern_codepoints": c.PatternCP, "original_pattern_raw_json": json.RawMessage(c.PatternJSON), "worker_input_raw_json": result.Raw, "worker_decoded_pattern_codepoints": result.Codepoints, "source_equivalent": !hasSurrogates(c.PatternCP)})
		t.Log(string(observation))
	}
	if unsupported != 2 || scalar != 2 {
		t.Fatalf("boundary controls drift unsupported%d scalar%d", unsupported, scalar)
	}
}
