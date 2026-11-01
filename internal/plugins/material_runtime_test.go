package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func materialProcessManifest() Manifest {
	return Manifest{SchemaVersion: 1, PluginID: "OwnedMaterialProcess", Name: "Owned material process", Version: "1.0.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke", "stop"}, Capabilities: []string{DownloadTorrentCapability, QBittorrentAddCapability}}
}
func materialProcessBinding() SessionBinding {
	return SessionBinding{Identity: Identity{PluginID: "OwnedMaterialProcess", InstanceID: "OwnedMaterialProcess.clone", Version: "1.0.0"}, Generation: 17}
}
func materialProcessCommand(t *testing.T, mode string) Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Path: path, Args: []string{"-test.run=^TestMaterialOwnedProcessFixture$", "--", mode}}
}
func materialProcessStart(t *testing.T, mode string, feature bool, small bool) *Client {
	t.Helper()
	runner := Runner{MaterialContinuation: feature, ConcurrentCalls: mode == "concurrent", RequireConcurrent: mode == "concurrent", Timeout: 20 * time.Second, MaxHostCalls: 2}
	if small {
		runner.MaxFrameBytes = 4096
		runner.MaxChunkBytes = 512
	}
	c, err := runner.Start(context.Background(), materialProcessCommand(t, mode), materialProcessManifest(), materialProcessBinding())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer func() {
			if n, exceeded := c.stderr.snapshot(); n != 0 || exceeded {
				t.Errorf("child stderr/race diagnostics: %d %v", n, exceeded)
			}
		}()
		if c.life.Err() != nil {
			select {
			case <-c.done:
			case <-time.After(3 * time.Second):
				t.Error("canceled child not reaped")
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := c.Stop(ctx); err != nil {
			t.Error("child shutdown", err)
		}
	})
	return c
}

type materialProcessInput struct {
	Op    string `json:"op"`
	Total int64  `json:"total"`
	Byte  byte   `json:"byte"`
}
type materialProcessOutput struct {
	PID       int    `json:"pid"`
	Bytes     int64  `json:"bytes"`
	Pages     int    `json:"pages"`
	SHA256    string `json:"sha256"`
	Finished  bool   `json:"finished"`
	Bridge    bool   `json:"bridge"`
	StaleCode string `json:"stale_code"`
}

func materialProcessPayload(op string, total int64, v byte) json.RawMessage {
	raw, _ := json.Marshal(materialProcessInput{op, total, v})
	return raw
}
func materialPatternSHA(total int64, v byte) string {
	h := sha256.New()
	page := bytes.Repeat([]byte{v}, MaterialFrameBytes)
	for total > 0 {
		n := min(total, int64(len(page)))
		_, _ = h.Write(page[:n])
		total -= n
	}
	return hex.EncodeToString(h.Sum(nil))
}

// This is an owned child process running this exact built test binary. It has
// stdin/stdout only: no sockets, HTTP, daemon, real plugin or downloader.
func TestMaterialOwnedProcessFixture(t *testing.T) {
	mode := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
			break
		}
	}
	if mode == "" {
		return
	}
	var mu sync.Mutex
	var stale context.Context
	var prior MaterialGrant
	handler := func(ctx context.Context, method string, raw json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			return json.RawMessage(`null`), nil
		}
		var in materialProcessInput
		if json.Unmarshal(raw, &in) != nil {
			return nil, errors.New("fixture input")
		}
		_, bridge := ctx.Value(materialPeerKey{}).(materialPeerBridge)
		out := materialProcessOutput{PID: os.Getpid(), Bridge: bridge}
		if in.Op == "probe" {
			return json.Marshal(out)
		}
		if in.Op == "stale" {
			mu.Lock()
			old, g := stale, prior
			mu.Unlock()
			_, err := materialExchange(old, g, "finish", g.Total, nil)
			out.StaleCode = codeOf(err)
			return json.Marshal(out)
		}
		capability := DownloadTorrentCapability
		if in.Op == "write" {
			capability = QBittorrentAddCapability
		}
		rawGrant, err := host(ctx, capability, raw)
		if err != nil {
			return nil, err
		}
		var grant MaterialGrant
		if strictJSON(rawGrant, &grant) != nil {
			return nil, errors.New("fixture grant")
		}
		mu.Lock()
		stale, prior = ctx, grant
		mu.Unlock()
		if in.Op == "forge" {
			if grant.Token != strings.Repeat("0", 64) {
				grant.Token = strings.Repeat("0", 64)
			} else {
				grant.Token = strings.Repeat("1", 64)
			}
		}
		h := sha256.New()
		for offset := int64(0); offset < grant.Total; {
			operation := "read"
			var data []byte
			if in.Op == "write" {
				operation = "write"
				data = bytes.Repeat([]byte{in.Byte}, int(min(int64(grant.ChunkBytes), grant.Total-offset)))
			}
			reply, err := materialExchange(ctx, grant, operation, offset, data)
			if err != nil {
				return nil, err
			}
			if operation == "read" {
				data = reply.Data
			}
			if !bytes.Equal(data, bytes.Repeat([]byte{in.Byte}, len(data))) {
				return nil, errors.New("fixture pattern")
			}
			_, _ = h.Write(data)
			offset = reply.Next
			out.Bytes = offset
			out.Pages++
		}
		reply, err := materialExchange(ctx, grant, "finish", grant.Total, nil)
		if err != nil {
			return nil, err
		}
		out.Finished = reply.Finished
		out.SHA256 = hex.EncodeToString(h.Sum(nil))
		if _, err = host(ctx, capability, json.RawMessage(`{"op":"after"}`)); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	var err error
	switch mode {
	case "serial":
		err = Serve(context.Background(), os.Stdin, os.Stdout, materialProcessManifest(), handler)
	case "concurrent":
		err = ServeConcurrent(context.Background(), os.Stdin, os.Stdout, materialProcessManifest(), handler)
	case "legacy":
		err = materialPreFeatureServeFixture(context.Background(), os.Stdin, os.Stdout, materialProcessManifest(), handler, false)
	default:
		os.Exit(3)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type materialProcessHost struct {
	t       *testing.T
	in      materialProcessInput
	mu      sync.Mutex
	ticket  context.Context
	next    int64
	pages   int
	digest  string
	calls   atomic.Int32
	closes  atomic.Int32
	reads   atomic.Int32
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (h *materialProcessHost) handle(ctx context.Context, id Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
	h.calls.Add(1)
	if id != materialProcessBinding().Identity {
		return nil, errors.New("fixture identity")
	}
	var in materialProcessInput
	if json.Unmarshal(raw, &in) != nil {
		return nil, errors.New("fixture payload")
	}
	if in.Op == "after" {
		h.mu.Lock()
		old := h.ticket
		h.mu.Unlock()
		direction := MaterialToWorker
		port := MaterialPort{Close: func() error { return nil }}
		if method == QBittorrentAddCapability {
			direction = MaterialToHost
			port.Write = func(context.Context, int64, []byte) error { return nil }
		} else {
			port.Read = func(context.Context, int64, int) ([]byte, error) { return nil, nil }
		}
		if codeOf(CheckMaterialHost(old, method)) != "material_issue_closed" {
			return nil, errors.New("ended callback check still live")
		}
		_, err := IssueMaterial(old, MaterialSpec{method, direction, "bytes", 0, materialPatternSHA(0, 0)}, port, func(context.Context) error { return nil })
		if codeOf(err) != "material_issue_closed" {
			return nil, errors.New("ended ordinary callback still issued")
		}
		return json.RawMessage(`true`), nil
	}
	if err := CheckMaterialHost(ctx, method); err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.ticket = ctx
	h.mu.Unlock()
	direction := MaterialToWorker
	if in.Op == "write" {
		direction = MaterialToHost
	}
	digest := sha256.New()
	port := MaterialPort{Close: func() error {
		h.closes.Add(1)
		if h.closed != nil {
			h.once.Do(func() { close(h.closed) })
		}
		return nil
	}}
	if direction == MaterialToWorker {
		port.Read = func(opctx context.Context, offset int64, n int) ([]byte, error) {
			h.reads.Add(1)
			if h.entered != nil {
				select {
				case h.entered <- struct{}{}:
				default:
				}
				<-opctx.Done()
				return nil, opctx.Err()
			}
			return bytes.Repeat([]byte{in.Byte}, n), nil
		}
	} else {
		port.Write = func(opctx context.Context, offset int64, data []byte) error {
			if opctx.Err() != nil {
				return opctx.Err()
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			if offset != h.next || !bytes.Equal(data, bytes.Repeat([]byte{in.Byte}, len(data))) {
				return errors.New("fixture upload offset/content")
			}
			h.next += int64(len(data))
			h.pages++
			_, _ = digest.Write(data)
			h.digest = hex.EncodeToString(digest.Sum(nil))
			return nil
		}
	}
	grant, err := IssueMaterial(ctx, MaterialSpec{method, direction, "bytes", in.Total, materialPatternSHA(in.Total, in.Byte)}, port, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		return nil, err
	}
	return json.Marshal(grant)
}

type materialTransferCase struct {
	mode, op string
	total    int64
	small    bool
}

func TestMaterialNegotiatedProcessSerialTransfer(t *testing.T) {
	materialProcessTransfers(t, []materialTransferCase{{"serial", "read", 65<<20 + 1, false}})
}
func TestMaterialNegotiatedProcessConcurrentTransfer(t *testing.T) {
	c := materialProcessStart(t, "concurrent", true, true)
	if !c.session.materialEnabled || !c.SupportsConcurrent() {
		t.Fatal("real concurrent feature ACK missing")
	}
	const total int64 = 300<<10 + 1
	entered := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	type result struct {
		op   string
		host *materialProcessHost
		out  RunResult
		err  error
	}
	results := make(chan result, 2)
	for _, op := range []string{"read", "write"} {
		op := op
		h := &materialProcessHost{t: t}
		go func() {
			out, err := c.Call(context.Background(), "invoke", materialProcessPayload(op, total, 0x5a), func(ctx context.Context, id Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
				var in materialProcessInput
				_ = json.Unmarshal(raw, &in)
				if in.Op != "after" {
					entered <- op
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return h.handle(ctx, id, method, raw)
			})
			results <- result{op, h, out, err}
		}()
	}
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case op := <-entered:
			if seen[op] {
				t.Fatal("same invocation entered twice")
			}
			seen[op] = true
		case <-time.After(4 * time.Second):
			t.Fatal("real handlers did not overlap")
		}
	}
	releaseOnce.Do(func() { close(release) })
	pid := 0
	for i := 0; i < 2; i++ {
		var got result
		select {
		case got = <-results:
		case <-time.After(15 * time.Second):
			t.Fatal("concurrent transfer did not complete")
		}
		var value materialProcessOutput
		if got.err != nil || json.Unmarshal(got.out.Result, &value) != nil {
			t.Fatal("concurrent transfer", got.op, got.err, string(got.out.Result))
		}
		if pid == 0 {
			pid = value.PID
		}
		if value.PID != pid || pid == os.Getpid() || pid <= 0 || !value.Bridge || !value.Finished || value.Bytes != total || value.Pages <= 64 || value.SHA256 != materialPatternSHA(total, 0x5a) {
			t.Fatal("concurrent identity/content/continuation", value)
		}
		if got.out.HostCalls != 2 || got.host.calls.Load() != 2 || got.host.closes.Load() != 1 {
			t.Fatal("concurrent callback/cleanup count", got.out, got.host.calls.Load(), got.host.closes.Load())
		}
		if got.op == "read" && got.out.SentWireBytes < int(total) || got.op == "write" && got.out.ReadWireBytes < int(total) {
			t.Fatal("concurrent actual wire lost", got.out)
		}
		if got.op == "write" {
			got.host.mu.Lock()
			n, hash := got.host.next, got.host.digest
			got.host.mu.Unlock()
			if n != total || hash != value.SHA256 {
				t.Fatal("received upload mismatch", n, hash)
			}
		}
	}
	out, err := c.Call(context.Background(), "invoke", materialProcessPayload("probe", 0, 0), nil)
	var probe materialProcessOutput
	if err != nil || json.Unmarshal(out.Result, &probe) != nil || probe.PID != pid {
		t.Fatal("same worker after overlap", err, string(out.Result))
	}
}
func materialProcessTransfers(t *testing.T, cases []materialTransferCase) {
	for _, tc := range cases {
		t.Run(tc.mode+"_"+tc.op, func(t *testing.T) {
			c := materialProcessStart(t, tc.mode, true, tc.small)
			if !c.session.materialEnabled || c.SupportsConcurrent() != (tc.mode == "concurrent") {
				t.Fatal("real feature ACK/mode missing")
			}
			h := &materialProcessHost{t: t}
			out, err := c.Call(context.Background(), "invoke", materialProcessPayload(tc.op, tc.total, 0xa5), h.handle)
			var value materialProcessOutput
			if err != nil || json.Unmarshal(out.Result, &value) != nil {
				t.Fatalf("actual child transfer: %+v %v", out, err)
			}
			if value.PID == os.Getpid() || value.PID <= 0 || !value.Bridge || !value.Finished || value.Bytes != tc.total || value.Pages <= 64 || value.SHA256 != materialPatternSHA(tc.total, 0xa5) {
				t.Fatalf("child result %+v", value)
			}
			if out.HostCalls != 2 || h.calls.Load() != 2 || h.closes.Load() != 1 {
				t.Fatalf("callback/cleanup counts %d/%d/%d", out.HostCalls, h.calls.Load(), h.closes.Load())
			}
			if tc.op == "read" && out.SentWireBytes < int(tc.total) || tc.op == "write" && out.ReadWireBytes < int(tc.total) {
				t.Fatal("real material wire hidden", out)
			}
			if tc.mode == "serial" && out.SentWireBytes <= defaultWireBytes {
				t.Fatal("did not cross ordinary cumulative wire bound")
			}
			if tc.op == "write" {
				h.mu.Lock()
				n, hash := h.next, h.digest
				h.mu.Unlock()
				if n != tc.total || hash != value.SHA256 {
					t.Fatal("upload not received", n, hash)
				}
			}
			again, err := c.Call(context.Background(), "invoke", materialProcessPayload("stale", 0, 0), nil)
			var state materialProcessOutput
			if err != nil || json.Unmarshal(again.Result, &state) != nil || state.PID != value.PID || state.StaleCode == "" || again.HostCalls != 0 {
				t.Fatal("stale bridge/same worker", err, string(again.Result))
			}
		})
	}
}

func TestMaterialNegotiatedProcessCompatibility(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		feature bool
	}{{"serial", false}, {"legacy", true}} {
		t.Run(fmt.Sprintf("%s_%v", tc.mode, tc.feature), func(t *testing.T) {
			c := materialProcessStart(t, tc.mode, tc.feature, true)
			out, err := c.Call(context.Background(), "invoke", materialProcessPayload("probe", 0, 0), nil)
			var value materialProcessOutput
			if err != nil || json.Unmarshal(out.Result, &value) != nil || value.Bridge || c.session.materialEnabled {
				t.Fatal("ordinary peer silently enabled material", err, string(out.Result))
			}
		})
	}
	if _, err := (Runner{MaterialContinuation: true}).Run(context.Background(), materialProcessCommand(t, "serial"), materialProcessManifest(), materialProcessBinding().InstanceID, "invoke", nil, nil); codeOf(err) != "material_persistent_binding_required" {
		t.Fatal("Run fabricated generation", err)
	}
}

func TestMaterialNegotiatedProcessCancellation(t *testing.T) {
	for _, mode := range []string{"serial", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			c := materialProcessStart(t, mode, true, true)
			h := &materialProcessHost{t: t, entered: make(chan struct{}, 1), closed: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := c.Call(ctx, "invoke", materialProcessPayload("read", 9001, 7), h.handle)
				done <- err
			}()
			select {
			case <-h.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("no real material port entry")
			}
			cancel()
			select {
			case err := <-done:
				if codeOf(err) != "execution_canceled" {
					t.Fatal("call cancellation", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("caller did not cancel")
			}
			select {
			case <-h.closed:
			case <-time.After(4 * time.Second):
				t.Fatal("owned port not joined/closed")
			}
			if h.closes.Load() != 1 || h.reads.Load() != 1 {
				t.Fatal("cancel cleanup/read counts", h.closes.Load(), h.reads.Load())
			}
			if mode == "concurrent" {
				out, err := c.Call(context.Background(), "invoke", materialProcessPayload("probe", 0, 0), nil)
				if err != nil {
					t.Fatal("one canceled call killed other call", err, string(out.Result))
				}
			}
		})
	}
}

func TestMaterialNegotiatedProcessUnissuedGrant(t *testing.T) {
	for _, mode := range []string{"serial", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			c := materialProcessStart(t, mode, true, true)
			h := &materialProcessHost{t: t, closed: make(chan struct{})}
			out, err := c.Call(context.Background(), "invoke", materialProcessPayload("forge", 9001, 3), h.handle)
			if err == nil || len(out.Result) != 0 || h.reads.Load() != 0 || out.HostCalls != 1 {
				t.Fatal("unissued token reached port or succeeded", out, err, h.reads.Load())
			}
			select {
			case <-h.closed:
			case <-time.After(4 * time.Second):
				t.Fatal("rejected grant scope not cleaned")
			}
			if h.closes.Load() != 1 {
				t.Fatal("wrong exact port cleanup")
			}
		})
	}
}
