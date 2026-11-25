package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

// This is an actual independent SDK process with the production handler.
// Only the host resources and private due-clock are synthetic protocol peers.
func TestRuntimeWorkerProcess(t *testing.T) {
	fixture := false
	for _, arg := range os.Args {
		if arg == "autosignin-sdk-fixture" {
			fixture = true
		}
	}
	if !fixture {
		return
	}
	p := NewPlugin()
	p.wait = func(ctx context.Context, stop <-chan struct{}, _ time.Time) error {
		select {
		case <-stop:
			return errors.New("removed")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := sdk.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, Manifest(), p.Handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type runtimePeer struct {
	t              *testing.T
	ctx            context.Context
	cancel         context.CancelFunc
	client         *sdk.Client
	mu             sync.Mutex
	config         any
	data           map[string]json.RawMessage
	n              int
	rpc            int
	completed      []sdk.RunResult
	failures       []error
	logs           []sdk.LogEntryV1
	methods        []string
	block, entered chan struct{}
	blocked        bool
	omitTimeout    bool
	siteURL        string
	render         bool
	renderHTML     string
	emptyImage     bool
	imageRequests  int
	loopReached    chan struct{}
}

func startRuntimePeer(t *testing.T, n int) *runtimePeer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest()
	client, err := (sdk.Runner{Timeout: time.Second * 5, ConcurrentCalls: true, RequireConcurrent: true}).Start(ctx, sdk.Command{Path: exe, Args: []string{"-test.run=^TestRuntimeWorkerProcess$", "--", "autosignin-sdk-fixture"}}, m, sdk.SessionBinding{Identity: sdk.Identity{PluginID: m.PluginID, InstanceID: "AutoSignInFixture", Version: m.Version}, Generation: 1})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	p := &runtimePeer{t: t, ctx: ctx, cancel: cancel, client: client, data: map[string]json.RawMessage{}, n: n, entered: make(chan struct{})}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.Stop(stop)
		p.cancel()
	})
	return p
}
func (p *runtimePeer) call(method string, value any) (sdk.RunResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return sdk.RunResult{}, err
	}
	return p.client.CallWithOptions(p.ctx, method, raw, p.host, sdk.CallOptions{CallerDeadlineOnly: true})
}
func (p *runtimePeer) must(method string, value any) sdk.RunResult {
	p.t.Helper()
	out, err := p.call(method, value)
	if err != nil {
		p.t.Fatalf("%s: %v", method, err)
	}
	return out
}
func (p *runtimePeer) host(ctx context.Context, _ sdk.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	p.rpc++
	p.methods = append(p.methods, method)
	p.mu.Unlock()
	if method == sdk.BackgroundSubmitCapability {
		var in struct {
			Method  string
			Payload json.RawMessage
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		pending, err := p.client.StartCallWithAdmission(ctx, p.ctx, in.Method, in.Payload, p.host, sdk.CallOptions{CallerDeadlineOnly: true})
		if err != nil {
			return nil, err
		}
		go func() {
			out, err := pending.Wait()
			p.mu.Lock()
			p.completed = append(p.completed, out)
			p.failures = append(p.failures, err)
			p.mu.Unlock()
		}()
		return json.RawMessage(`{"accepted":true}`), nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	encode := func(value any) (json.RawMessage, error) { return json.Marshal(value) }
	switch method {
	case sdk.SitesSourceCustomConfigCapability:
		return encode(sdk.SiteSourceCustomConfigV1{SchemaVersion: 1, Config: json.RawMessage(`{}`), Snapshot: strings.Repeat("a", 64)})
	case sdk.SitesSourceCredentialsCapability:
		if p.omitTimeout {
			return nil, fmt.Errorf("owned source credential snapshot unavailable")
		}
		var req sdk.SiteSourceReferenceRequestV1
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		req.Reference.Snapshot = strings.Repeat("b", 64)
		fields, _ := json.Marshal(Object{"cookie": "owned_fixture=1", "ua": "OwnedSyntheticUA"})
		return encode(sdk.SiteSourceCredentialsV1{SchemaVersion: 1, Reference: req.Reference, Fields: fields})
	case sdk.BaseGetConfig:
		var in struct {
			PluginID string `json:"plugin_id"`
		}
		_ = json.Unmarshal(raw, &in)
		if in.PluginID == "CustomSites" {
			return json.RawMessage(`{}`), nil
		}
		return encode(p.config)
	case sdk.BaseUpdateConfig:
		var in struct{ Config any }
		if err := Decode(raw, &in); err != nil {
			return nil, err
		}
		p.config = in.Config
		return json.RawMessage(`true`), nil
	case sdk.BaseGetData:
		var in struct{ Key *string }
		_ = json.Unmarshal(raw, &in)
		if in.Key != nil && *in.Key != "" {
			if value, ok := p.data[*in.Key]; ok {
				return value, nil
			}
			return json.RawMessage(`null`), nil
		}
		rows := []sdk.BaseDataRow{}
		for key, value := range p.data {
			rows = append(rows, sdk.BaseDataRow{Key: key, Value: value})
		}
		return encode(rows)
	case sdk.BaseSaveData:
		var in struct {
			Key   string
			Value json.RawMessage
		}
		_ = json.Unmarshal(raw, &in)
		p.data[in.Key] = append(json.RawMessage(nil), in.Value...)
		return json.RawMessage(`null`), nil
	case sdk.BaseDelData:
		var in struct{ Key string }
		_ = json.Unmarshal(raw, &in)
		delete(p.data, in.Key)
		return json.RawMessage(`null`), nil
	case sdk.SitesReadCapability:
		rows := []sdk.SiteHostRowV1{}
		address := p.siteURL
		if address == "" {
			address = "https://owned.invalid/"
		}
		for i := 0; i < p.n; i++ {
			rows = append(rows, sdk.SiteHostRowV1{ID: fmt.Sprint(i + 1), SourceID: int64(i + 1), Name: fmt.Sprintf("Owned%03d", i), URL: address, Enabled: true, Render: p.render})
		}
		return encode(sdk.SiteHostResultV1{SchemaVersion: 1, Items: rows})
	case sdk.SitesSourceIndexersCapability:
		rows := []sdk.SiteSourceIndexerV1{}
		address := p.siteURL
		if address == "" {
			address = "https://owned.invalid/"
		}
		for i := 0; i < p.n; i++ {
			id := fmt.Sprint(i + 1)
			fieldValues := Object{"id": i + 1, "name": fmt.Sprintf("Owned%03d", i), "url": address, "timeout": 20, "public": false}
			if p.omitTimeout {
				delete(fieldValues, "timeout")
			}
			fields, _ := json.Marshal(fieldValues)
			rows = append(rows, sdk.SiteSourceIndexerV1{SiteID: &id, Fields: fields})
		}
		return encode(sdk.SiteSourceIndexersResultV1{SchemaVersion: 1, Items: rows})
	case sdk.SitesCredentialsCapability:
		var in struct{ ID string }
		_ = json.Unmarshal(raw, &in)
		return encode(sdk.SiteCredentialsResultV1{SchemaVersion: 1, ID: in.ID, Cookie: "owned_fixture=1", UserAgent: "OwnedSyntheticUA"})
	case sdk.SiteRenderCapability:
		return encode(sdk.SiteRenderResult{HTML: &p.renderHTML})
	case sdk.SitesSourceRequestCapability:
		var sourceReq sdk.SiteSourceHTTPRequestV1
		if err := json.Unmarshal(raw, &sourceReq); err != nil {
			return nil, err
		}
		raw, _ = json.Marshal(sourceReq.Request)
		if p.emptyImage {
			var request sdk.SiteHTTPRequestV1
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if request.Path == "/image_code_ajax.php" {
				p.imageRequests++
				if p.imageRequests == 300 && p.loopReached != nil {
					close(p.loopReached)
				}
				return encode(sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "unknown", Response: nil})
			}
		}
		if p.block != nil && !p.blocked {
			p.blocked = true
			close(p.entered)
			block := p.block
			p.mu.Unlock()
			select {
			case <-block:
			case <-ctx.Done():
			}
			p.mu.Lock()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return encode(sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed", Response: &sdk.SiteHTTPResponseV1{StatusCode: 200, Content: []byte(`<a href="logout.php">out</a><div>签到成功</div>`), ContentType: "text/html; charset=utf-8", Cookies: []sdk.SiteHTTPPairV1{}}})
	case sdk.SitesSourceResultWriteCapability:
		return encode(sdk.SiteHostAccessV1{SchemaVersion: 1})
	case sdk.LogWriteCapability:
		var in sdk.LogEntryV1
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		p.logs = append(p.logs, in)
		return json.RawMessage(`null`), nil
	case sdk.EventSendCapability:
		return json.RawMessage(`null`), nil
	case sdk.PostMessageCapability:
		return json.RawMessage(`{"queued":0,"attempted":0,"failed":0,"errors":[]}`), nil
	}
	return nil, fmt.Errorf("unexpected protocol peer operation %s", method)
}
func runtimeConfig() Object {
	return Object{"enabled": false, "onlyonce": false, "notify": false, "cron": "0 0 * * *", "queue_cnt": 5, "sign_sites": []any{"all"}, "login_sites": []any{}, "retry_keyword": "失败", "auto_cf": 0, "clean": false}
}
func TestRuntimeSDKContinuationBudgetAndWholePass(t *testing.T) {
	p := startRuntimePeer(t, 120)
	p.must("init", runtimeConfig())
	parent := p.must("sign_in", nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rpc <= 256 {
		t.Fatalf("synthetic resource peer did not exceed256 RPC: %d", p.rpc)
	}
	if len(p.completed) < 3 {
		t.Fatalf("no continuation chain: %d", len(p.completed))
	}
	for i, out := range p.completed {
		if p.failures[i] != nil {
			t.Fatal(p.failures[i])
		}
		if out.HostCalls > 64 {
			t.Fatalf("callback budget: %d", out.HostCalls)
		}
	}
	if parent.HostCalls > 64 {
		t.Fatal("parent budget")
	}
	maxRPC := 0
	for _, out := range p.completed {
		if out.HostCalls > maxRPC {
			maxRPC = out.HostCalls
		}
	}
	t.Logf("classification=actual SDK98 production handler, synthetic source resource peer; sites=120 totalRPC=%d childCallbacks=%d maxCallbackRPC=%d parentRPC=%d", p.rpc, len(p.completed), maxRPC, parent.HostCalls)
	var history Object
	for key, raw := range p.data {
		if strings.HasPrefix(key, "签到-") {
			if err := Decode(raw, &history); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(asList(history["do"])) != 120 || len(asObject(history["results"])) != 120 {
		t.Fatalf("public owner returned before complete source history: %#v", history)
	}
}
func TestRuntimeSDKSourceCredentialFailureIsTerminal(t *testing.T) {
	p := startRuntimePeer(t, 1)
	p.omitTimeout = true
	p.must("init", runtimeConfig())
	if _, err := p.call("sign_in", nil); err == nil {
		t.Fatal("source credential guard failure became business success")
	}
	p.must("get_state", nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, method := range p.methods {
		if method == sdk.SitesSourceRequestCapability || method == sdk.SitesSourceResultWriteCapability || method == sdk.BaseSaveData {
			t.Fatalf("missing-capability failure performed later business effect %s", method)
		}
	}
}
func TestRuntimeSDKRawConfigOnceResetAndStop(t *testing.T) {
	p := startRuntimePeer(t, 0)
	config := runtimeConfig()
	config["enabled"] = "false"
	config["onlyonce"] = true
	config["unknown"] = "drop"
	p.must("init", config)
	state := p.must("get_state", nil)
	if string(state.Result) != `"false"` {
		t.Fatalf("raw state changed: %s", state.Result)
	}
	p.mu.Lock()
	saved := p.config.(map[string]any)
	if saved["onlyonce"] != false || len(saved) != 10 {
		p.mu.Unlock()
		t.Fatalf("source exact10 config/reset: %#v", saved)
	}
	p.mu.Unlock()
	p.must("stop_service", nil)
	p.must("init", Object{})
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, method := range p.methods {
		if method == sdk.SitesSourceRequestCapability {
			t.Fatal("removed once replayed a network operation")
		}
	}
}
func TestRuntimeSDKPublicOwnerWaitsForFinalSite(t *testing.T) {
	p := startRuntimePeer(t, 1)
	p.block = make(chan struct{})
	p.must("init", runtimeConfig())
	done := make(chan error, 1)
	go func() { _, err := p.call("sign_in", nil); done <- err }()
	select {
	case <-p.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("source request never reached owned peer")
	}
	select {
	case err := <-done:
		t.Fatalf("public owner completed while source request live: %v", err)
	default:
	}
	close(p.block)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not join final site")
	}
}
func TestRuntimeLazyResponseTextSourceReadPoints(t *testing.T) {
	calls := 0
	r := &PageResponse{Status: 503, getText: func() (string, error) { calls++; return "normal", nil }}
	hooks := SiteVisitHooks{HTTP: pageTransportFunc(func(context.Context, PageRequest) (*PageResponse, error) { return r, nil })}
	_ = GenericSignin(context.Background(), Object{"name": "Owned", "url": "https://owned.invalid", "cookie": "owned"}, hooks)
	if calls != 1 {
		t.Fatalf("source challenge before non200 must read text once: %d", calls)
	}
	r.Status = 200
	calls = 0
	_ = GenericSignin(context.Background(), Object{"name": "Owned", "url": "https://owned.invalid", "cookie": "owned"}, hooks)
	if calls != 2 {
		t.Fatalf("source challenge then fresh parse read: %d", calls)
	}
	r.getText = func() (string, error) { return "", errors.New("source codec failure") }
	result := GenericSignin(context.Background(), Object{"name": "Owned", "url": "https://owned.invalid", "cookie": "owned"}, hooks)
	if result.Message != "签到失败：source codec failure！" {
		t.Fatal(result)
	}
}

type pageTransportFunc func(context.Context, PageRequest) (*PageResponse, error)

func (f pageTransportFunc) Do(ctx context.Context, r PageRequest) (*PageResponse, error) {
	return f(ctx, r)
}

// This exercises the unmodified production HDSky loop and continuation chain.
// The peer's native None outcomes and rendered HTML are synthetic; no real
// HDSky/browser/HTTP/OCR service or credentials are contacted.
func TestRuntimeSDKHDSkyUnboundedHashLoopCallerCancel(t *testing.T) {
	p := startRuntimePeer(t, 1)
	p.siteURL = "https://hdsky.me/"
	p.render = true
	p.renderHTML = "<html><body>Owned unconfirmed</body></html>"
	p.emptyImage = true
	p.loopReached = make(chan struct{})
	config := runtimeConfig()
	config["enabled"] = true
	p.must("init", config)
	owner, cancel := context.WithCancel(p.ctx)
	done := make(chan error, 1)
	go func() {
		_, err := p.client.CallWithOptions(owner, "sign_in", json.RawMessage(`null`), p.host, sdk.CallOptions{CallerDeadlineOnly: true})
		done <- err
	}()
	select {
	case <-p.loopReached:
	case err := <-done:
		t.Fatalf("source loop ended before300: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("source infinite branch did not rotate beyond256")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("caller cancel returned business success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not join active continuation requests")
	}
	p.must("get_state", nil)
	p.mu.Lock()
	requests := p.imageRequests
	for key := range p.data {
		if strings.HasPrefix(key, "签到-") {
			p.mu.Unlock()
			t.Fatal("cancel wrote source pass history")
		}
	}
	p.emptyImage = false
	p.renderHTML = "<div>已签到</div>"
	p.mu.Unlock()
	p.must("sign_in", nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.imageRequests != requests {
		t.Fatal("cancelled pass retained old caller or loop")
	}
	var history Object
	for key, raw := range p.data {
		if strings.HasPrefix(key, "签到-") {
			if err := Decode(raw, &history); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(asObject(history["results"])) != 1 {
		t.Fatal("same SDK process did not recover with fresh source invocation")
	}
	t.Logf("classification=actual SDK98 production HDSky loop/synthetic native None and render; hashPOSTs=%d totalRPC=%d fresh-pass-results=1", requests, p.rpc)
}
