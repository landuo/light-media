package downloadercommands

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

//go:embed testdata/source-qb-cookie01.json
var qbCookieOriginalRaw []byte

type qbCookieOracleStepInput struct {
	Args map[string]any `json:"args"`
	Protocol map[string]any `json:"protocol"`
}
type qbCookieOracleStep struct {
	Index int `json:"index"`
	Input qbCookieOracleStepInput `json:"input"`
	Before any `json:"cookie_store_before"`
	After any `json:"cookie_store_after"`
	Outcome map[string]any `json:"outcome"`
	Calls int `json:"protocol_calls"`
	Trace []any `json:"trace"`
}
type qbCookieOracleCase struct {
	ID string `json:"id"`
	Input struct {
		ID string `json:"id"`
		Store any `json:"initial_cookie_store"`
		Instance map[string]any `json:"instance"`
		Steps []qbCookieOracleStepInput `json:"steps"`
	} `json:"input"`
	Steps []qbCookieOracleStep `json:"steps"`
	Final any `json:"final_cookie_store"`
}

func qbCookieOracleClone(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil { t.Fatal(err) }
	var clone any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&clone); err != nil { t.Fatal(err) }
	return clone
}
func qbCookieOracleSame(t *testing.T, label string, got, want any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil { t.Fatal(err) }
	w, err := json.Marshal(want)
	if err != nil { t.Fatal(err) }
	// JSON object key order is not part of this ordinary-map contract. Array
	// order, scalar values and null versus [] remain exact; no log filtering.
	if !bytes.Equal(g, w) { t.Fatalf("%s differs\ngot  %s\nwant %s", label, g, w) }
}

type qbCookieOracleContextKey struct{}
type qbCookieOraclePort struct {
	t *testing.T
	parent context.Context
	token *int
	active bool
	scopes int
	protocol map[string]any
	store any
	calls int
	trace []any
}

func (p *qbCookieOraclePort) requireContext(ctx context.Context, primitive bool) {
	p.t.Helper()
	if ctx == nil || ctx.Err() != nil { p.t.Fatal("unexpected absent/canceled oracle context") }
	if p.active {
		if ctx.Value(qbCookieOracleContextKey{}) != p.token { p.t.Fatal("aggregate context was not passed to primitive/log") }
	} else if primitive || ctx != p.parent {
		p.t.Fatal("primitive escaped aggregate or direct Add lost caller context")
	}
}
func (p *qbCookieOraclePort) WithAddCookieScope(ctx context.Context, callback func(context.Context, QBAddCookiePrimitives) (any, error)) (any, error) {
	p.t.Helper()
	if ctx != p.parent || p.active || callback == nil { p.t.Fatal("unexpected repeated/nested aggregate scope") }
	p.scopes++
	p.active = true
	defer func() { p.active = false }()
	return callback(context.WithValue(ctx, qbCookieOracleContextKey{}, p.token), p)
}
func (p *qbCookieOraclePort) invoke(ctx context.Context, method, prefix string, kwargs map[string]any, reply any, commit bool, primitive bool) (any, error) {
	p.t.Helper()
	p.requireContext(ctx, primitive)
	p.calls++
	if p.calls > 4 { p.t.Fatal("more than original maximum four outer protocol calls") }
	p.trace = append(p.trace, map[string]any{"event": "protocol_call", "method": method, "kwargs": qbCookieOracleClone(p.t, kwargs)})
	if failure, present := p.protocol[prefix+"_raise"]; present && failure != nil {
		message, ok := failure.(string)
		if !ok { p.t.Fatal("non-string original recording exception control") }
		p.trace = append(p.trace, map[string]any{"event": "protocol_exception", "method": method, "class": "RuntimeError", "message": message, "effect_model": "owned pre-effect exception"})
		// This exactly models the original recorder's ordinary RuntimeError.
		// No unknown native/authorization/transport error is relabeled as source.
		return nil, &SourceException{Class: "RuntimeError", Message: message}
	}
	if commit {
		p.store = qbCookieOracleClone(p.t, kwargs["cookies"])
		p.trace = append(p.trace, map[string]any{"event": "owned_cookie_store_commit", "value": qbCookieOracleClone(p.t, p.store)})
	}
	value := qbCookieOracleClone(p.t, reply)
	p.trace = append(p.trace, map[string]any{"event": "protocol_return", "method": method, "value": qbCookieOracleClone(p.t, value)})
	return value, nil
}
func (p *qbCookieOraclePort) WebAPIVersion(ctx context.Context) (any, error) {
	return p.invoke(ctx, "app_web_api_version", "version", map[string]any{}, p.protocol["version_reply"], false, true)
}
func (p *qbCookieOraclePort) Cookies(ctx context.Context) ([]any, error) {
	value, err := p.invoke(ctx, "app_cookies", "cookies", map[string]any{}, p.store, false, true)
	if err != nil || value == nil { return nil, err }
	list, ok := value.([]any)
	if !ok { p.t.Fatal("original store is outside bound null/list domain") }
	return list, nil
}
func (p *qbCookieOraclePort) SetCookies(ctx context.Context, cookies []map[string]any) (any, error) {
	return p.invoke(ctx, "app_set_cookies", "set", map[string]any{"cookies": cookies}, p.protocol["set_reply"], true, true)
}
func (p *qbCookieOraclePort) Add(ctx context.Context, call QBAddCall) (any, error) {
	kwargs := map[string]any{"urls": call.URLs, "torrent_files": call.TorrentFiles, "save_path": call.SavePath, "is_paused": call.Paused, "tags": call.Tags, "use_auto_torrent_management": call.AutoManagement, "is_sequential_download": call.Sequential, "is_first_last_piece_priority": call.FirstLastPiece, "cookie": call.Cookie, "category": call.Category}
	for key, value := range call.Extra {
		if _, exists := kwargs[key]; exists { p.t.Fatal("duplicate add keyword reached protocol port", key) }
		kwargs[key] = value
	}
	return p.invoke(ctx, "torrents_add", "add", kwargs, p.protocol["add_reply"], false, false)
}
func (p *qbCookieOraclePort) Start(context.Context, any) (any, error) {
	p.t.Fatal("Start is outside original Cookie oracle")
	return nil, nil
}
func (p *qbCookieOraclePort) Recheck(context.Context, any) (any, error) {
	p.t.Fatal("Recheck is outside original Cookie oracle")
	return nil, nil
}
func (p *qbCookieOraclePort) DeleteKeepFiles(context.Context, any) (any, error) {
	p.t.Fatal("Delete is outside original Cookie oracle")
	return nil, nil
}
func (p *qbCookieOraclePort) log(ctx context.Context, level, message string) {
	p.requireContext(ctx, false)
	p.trace = append(p.trace, map[string]any{"event": "log", "level": level, "message": message})
}

func TestQBCookieOriginalSource17(t *testing.T) {
	digest := sha256.Sum256(qbCookieOriginalRaw)
	if hex.EncodeToString(digest[:]) != "758df1e970dad525d564dc6732e2153f545dc8a0314fbbfd0b3e846156f02b6a" { t.Fatal("not exact actual original Cookie raw") }
	var oracle struct {
		Status string `json:"status"`
		Commit string `json:"source_commit"`
		Count int `json:"case_count"`
		Adds int `json:"add_call_count"`
		Cases []qbCookieOracleCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(qbCookieOriginalRaw))
	decoder.UseNumber()
	if err := decoder.Decode(&oracle); err != nil { t.Fatal(err) }
	if oracle.Status != "ORIGINAL_QB_URL_COOKIE_RECORDING_OBSERVATIONS_COMPLETE" || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.Count != 17 || len(oracle.Cases) != 17 || oracle.Adds != 18 {
		t.Fatal("wrong original observation set")
	}
	adds, calls, scopes, sourceFalse := 0, 0, 0, 0
	wantCalls := 0
	for _, scenario := range oracle.Cases {
		for _, step := range scenario.Steps { wantCalls += step.Calls }
		t.Run(scenario.ID, func(t *testing.T) {
			if scenario.Input.ID != scenario.ID || len(scenario.Steps) != len(scenario.Input.Steps) { t.Fatal("inconsistent original scenario input") }
			port := &qbCookieOraclePort{t: t, parent: context.Background(), token: new(int), store: qbCookieOracleClone(t, scenario.Input.Store)}
			q := QBittorrent{Client: port, Config: QBConfig{Category: scenario.Input.Instance["category"], Sequential: scenario.Input.Instance["sequential"], FirstLastPiece: scenario.Input.Instance["first_last_piece"]}, Log: port.log}
			// One q and one recording store per scenario. In particular, the final
			// scenario's second Add consumes the first Add's actual stored result.
			for index, step := range scenario.Steps {
				t.Run(strconv.Itoa(step.Index), func(t *testing.T) {
					port.t = t
					if step.Index != index { t.Fatal("original step order changed") }
					qbCookieOracleSame(t, "original step input", step.Input, scenario.Input.Steps[index])
					qbCookieOracleSame(t, "store before", port.store, step.Before)
					port.protocol = qbCookieOracleClone(t, step.Input.Protocol).(map[string]any)
					port.calls = 0
					port.trace = []any{}
					beforeScopes := port.scopes
					args := Arguments(qbCookieOracleClone(t, step.Input.Args).(map[string]any))
					result, err := q.AddTorrent(port.parent, args)
					adds++
					if err != nil { t.Fatalf("unexpected host/boundary error on original recording domain: %v", err) }
					outcome := map[string]any{"kind": "return", "value": map[string]any{"kind": "tuple", "items": []any{result.Success, result.IDs}}, "python_type": "tuple", "python_truthy": result.SourceTruthy()}
					qbCookieOracleSame(t, "source tuple/type/truthiness", outcome, step.Outcome)
					qbCookieOracleSame(t, "full ordered primitive/log/state trace", port.trace, step.Trace)
					qbCookieOracleSame(t, "store after", port.store, step.After)
					if port.calls != step.Calls { t.Fatalf("outer protocol calls: got %d want %d", port.calls, step.Calls) }
					wantScopes := 1
					if strings.HasPrefix(args["content"].(string), "magnet:") { wantScopes = 0 }
					if port.active || port.scopes-beforeScopes != wantScopes { t.Fatal("wrong aggregate entry/release or magnet path") }
					calls += port.calls
					scopes += port.scopes-beforeScopes
					if !result.Success { sourceFalse++ }
				})
			}
			qbCookieOracleSame(t, "final same-receiver store", port.store, scenario.Final)
		})
	}
	if adds != oracle.Adds || calls != wantCalls || scopes != 17 || sourceFalse != 2 {
		t.Fatalf("finite coverage: adds=%d calls=%d/%d scopes=%d sourceFalse=%d", adds, calls, wantCalls, scopes, sourceFalse)
	}
	t.Logf("exact original projection: 17 scenarios / %d adds / %d outer protocol calls / %d false-but-truthy source tuples; no daemon or HTTP", adds, calls, sourceFalse)
}
