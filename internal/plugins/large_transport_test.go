package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	old "light-media/internal/plugins/testdata/legacywire"
)

func largeCommand(t *testing.T, scenario string, args ...string) Command {
	t.Helper()
	p, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return Command{Path: p, Args: append([]string{"-test.run=^TestLargeWorker$", "--", scenario}, args...)}
}
func blobJSON(n int) json.RawMessage {
	v, _ := json.Marshal(map[string]string{"blob": strings.Repeat("x", n)})
	return v
}
func TestFourWayChunksAndSmallPhysicalFrames(t *testing.T) {
	for _, frame := range []int{1 << 20, 4096} {
		t.Run(strconv.Itoa(frame), func(t *testing.T) {
			raw := blobJSON((2 << 20) + 17)
			calls := 0
			got, e := (Runner{MaxFrameBytes: frame, Timeout: 90 * time.Second}).Run(context.Background(), largeCommand(t, "four"), fixtureManifest(), "Fixture", "invoke", raw, func(_ context.Context, _ Identity, _ string, p json.RawMessage) (json.RawMessage, error) {
				calls++
				if !bytes.Equal(p, raw) {
					t.Error("host payload changed")
				}
				return raw, nil
			})
			if e != nil || calls != 1 || got.HostCalls != 1 || !bytes.Equal(got.Result, raw) || got.SentChunks == 0 || got.ReadChunks == 0 || got.SentWireBytes <= len(raw)*2 || got.ReadWireBytes <= len(raw)*2 {
				t.Fatalf("four-way failed %v calls=%d result=%d stats=%+v", e, calls, len(got.Result), struct{ S, R, C, D int }{got.SentWireBytes, got.ReadWireBytes, got.SentChunks, got.ReadChunks})
			}
		})
	}
}
func oldManifest() old.Manifest {
	m := fixtureManifest()
	return old.Manifest{SchemaVersion: m.SchemaVersion, PluginID: m.PluginID, Name: m.Name, Version: m.Version, Runtime: m.Runtime, Entry: m.Entry, HostAPIMin: 1, HostAPIMax: 1, Methods: m.Methods, Capabilities: m.Capabilities}
}
func TestFrozenOldWireBothDirections(t *testing.T) {
	raw := json.RawMessage(`{"small":true}`)
	got, e := (Runner{}).Run(context.Background(), largeCommand(t, "old"), fixtureManifest(), "Fixture", "invoke", raw, func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) { return raw, nil })
	if e != nil || !bytes.Equal(got.Result, raw) || got.SentChunks != 0 || got.ReadChunks != 0 {
		t.Fatal("new host / frozen old worker", e)
	}
	c := largeCommand(t, "four")
	result, e := (old.Runner{}).Run(context.Background(), old.Command{Path: c.Path, Args: c.Args}, oldManifest(), "Fixture", "invoke", raw, func(context.Context, old.Identity, string, json.RawMessage) (json.RawMessage, error) { return raw, nil })
	if e != nil || !bytes.Equal(result.Result, raw) || result.HostCalls != 1 {
		t.Fatal("frozen old host / new worker", e)
	}
	got, e = (Runner{}).Run(context.Background(), largeCommand(t, "old"), fixtureManifest(), "Fixture", "invoke", blobJSON(2<<20), nil)
	if codeOf(e) != "frame_limit" || got.HostCalls != 0 {
		t.Fatal("old wire silently enabled chunk", e)
	}
}
func TestConcurrentReverseCallsAndClosedCallbackPhase(t *testing.T) {
	for _, scenario := range []string{"concurrent", "late"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			got, e := (Runner{MaxFrameBytes: 4096}).Run(context.Background(), largeCommand(t, scenario), fixtureManifest(), "Fixture", "invoke", json.RawMessage(`{}`), func(_ context.Context, _ Identity, _ string, p json.RawMessage) (json.RawMessage, error) {
				calls++
				return p, nil
			})
			want := 8
			if scenario == "late" {
				want = 1
			}
			if e != nil || calls != want || got.HostCalls != want || string(got.Result) != `{"ok":true}` {
				t.Fatal("reverse exchanges interleaved or old callback revived", scenario, e, calls, got.HostCalls)
			}
		})
	}
}
func TestCanceledGateWaitDoesNotInterruptActiveExchange(t *testing.T) {
	marker := t.TempDir() + "/entered"
	calls := 0
	got, e := (Runner{Timeout: 5 * time.Second}).Run(context.Background(), largeCommand(t, "gate", marker), fixtureManifest(), "Fixture", "invoke", json.RawMessage(`{}`), func(ctx context.Context, _ Identity, _ string, p json.RawMessage) (json.RawMessage, error) {
		calls++
		if err := os.WriteFile(marker, []byte("entered"), 0600); err != nil {
			return nil, err
		}
		select {
		case <-time.After(150 * time.Millisecond):
			return p, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if e != nil || calls != 1 || got.HostCalls != 1 || string(got.Result) != `{"ok":true}` {
		t.Fatal("waiting cancellation poisoned active call", e, calls)
	}
}
func TestBudgetsAndErrorsRetainHostCallEvidence(t *testing.T) {
	for _, item := range []struct {
		name           string
		runner         Runner
		scenario, code string
		calls          int
	}{
		{"send_wire", Runner{MaxInputBytes: 4 << 20}, "four", "input_limit", 1},
		{"read_wire", Runner{MaxOutputBytes: 4 << 20}, "four", "incomplete_transport_message", 1},
		{"message", Runner{MaxFrameBytes: 4096, MaxMessageBytes: 32 << 10}, "four", "message_limit", 0},
		{"worker_output_after_host", Runner{}, "oversize_after_host", "message_limit", 1},
		{"message_chunks", Runner{MaxFrameBytes: 4096, MaxMessageChunks: 2}, "four", "transport_chunk_limit", 0},
		{"session_chunks", Runner{MaxFrameBytes: 4096, MaxSessionChunks: 2}, "four", "transport_chunk_limit", 0},
	} {
		t.Run(item.name, func(t *testing.T) {
			calls := 0
			result, e := item.runner.Run(context.Background(), largeCommand(t, item.scenario), fixtureManifest(), "Fixture", "invoke", blobJSON(2<<20), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				return blobJSON(2 << 20), nil
			})
			if codeOf(e) != item.code || calls != item.calls || result.HostCalls != calls || len(result.Result) != 0 {
				t.Fatalf("budget/evidence: err=%v external=%d resultcalls=%d", e, calls, result.HostCalls)
			}
		})
	}
}

// The marker is published only by the actual child, after the transport stage
// named by Scenario. ReadBytes counts complete setup lines plus the invocation
// prefix (block_write) or the complete first chunk (block_ack).
type blockedTransportReady struct {
	PID       int    `json:"pid"`
	Scenario  string `json:"scenario"`
	ReadBytes int    `json:"read_bytes"`
}

func TestBlockedWriteAndACKCancellationReapActualChild(t *testing.T) {
	for _, scenario := range []string{"block_write", "block_ack"} {
		t.Run(scenario, func(t *testing.T) {
			readyfile := t.TempDir() + "/ready"
			command, payload := largeCommand(t, scenario, readyfile), blobJSON(2<<20)
			ctx, cancel := context.WithCancel(context.Background())
			type outcome struct {
				result RunResult
				err    error
				ended  time.Time
			}
			done := make(chan outcome, 1)
			invoked := make(chan struct{}, 1)
			finished := false
			defer func() {
				cancel()
				if !finished {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("fixture cleanup did not finish")
					}
				}
			}()
			started := time.Now()
			go func() {
				// This test controls cancellation after proven I/O readiness.
				// It does not replace or extend any production Runner budget.
				result, err := (Runner{ObservePhase: func(o RunObservation) {
					if o.Phase == PhaseInvokeBegin && o.ContextState == RunContextActive {
						select {
						case invoked <- struct{}{}:
						default:
						}
					}
				}}).Run(ctx, command, fixtureManifest(), "Fixture", "invoke", payload, nil)
				done <- outcome{result, err, time.Now()}
			}()

			// Startup is a fixture precondition, not part of the 200 ms
			// blocked-I/O observation or the 3 s cancellation/reap bound.
			startup := time.NewTimer(10 * time.Second)
			defer startup.Stop()
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			var ready blockedTransportReady
			for {
				raw, err := os.ReadFile(readyfile)
				if err == nil {
					if json.Unmarshal(raw, &ready) != nil || ready.PID <= 0 || ready.Scenario != scenario || ready.ReadBytes <= 0 {
						t.Fatalf("invalid child readiness: %q", raw)
					}
					break
				}
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("read child readiness", err)
				}
				select {
				case got := <-done:
					finished = true
					t.Fatalf("Run returned before child readiness: err=%v result=%+v", got.err, got.result)
				case <-startup.C:
					t.Fatal("child did not reach blocked-transport readiness within the fixture startup guard")
				case <-poll.C:
				}
			}
			readyAt := time.Now()
			select {
			case <-invoked:
			default:
				t.Fatal("child ready without successful parent handshake/negotiation and active invocation")
			}
			if err := syscall.Kill(ready.PID, 0); err != nil {
				t.Fatal("ready child is not alive", ready.PID, err)
			}
			bound := time.NewTimer(3 * time.Second)
			defer bound.Stop()
			observation := time.NewTimer(200 * time.Millisecond)
			defer observation.Stop()
			select {
			case got := <-done:
				finished = true
				t.Fatalf("blocked exchange returned before cancellation: err=%v result=%+v", got.err, got.result)
			case <-observation.C:
			}
			canceledAt := time.Now()
			cancel()
			var got outcome
			select {
			case got = <-done:
				finished = true
			case <-bound.C:
				t.Fatal("blocked transport did not return within 3 s of readiness")
			}
			if got.ended.Before(canceledAt) || codeOf(got.err) != "execution_canceled" || len(got.result.Result) != 0 || got.result.HostCalls != 0 {
				t.Fatalf("blocked transport cancellation lost evidence: err=%v result=%+v", got.err, got.result)
			}
			if scenario == "block_write" {
				// The child stopped after one byte, and no physical chunk
				// completed. Positive partial-write bytes rule out canceling
				// during encoding or merely while waiting for an ACK.
				if got.result.SentChunks != 0 || got.result.SentWireBytes < ready.ReadBytes {
					t.Fatalf("not a partial blocked write: child=%+v result=%+v", ready, got.result)
				}
			} else if got.result.SentChunks != 1 || got.result.SentWireBytes != ready.ReadBytes {
				t.Fatalf("not a complete chunk awaiting ACK: child=%+v result=%+v", ready, got.result)
			}
			if err := syscall.Kill(ready.PID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatal("child not reaped", ready.PID, err)
			}
			if elapsed := time.Since(readyAt); elapsed > 3*time.Second {
				t.Fatal("child reap exceeded 3 s from readiness", elapsed)
			}
			t.Logf("actual child pid=%d ready_after=%s canceled_and_reaped_after=%s sent_bytes=%d sent_chunks=%d child_read_bytes=%d", ready.PID, readyAt.Sub(started), time.Since(readyAt), got.result.SentWireBytes, got.result.SentChunks, ready.ReadBytes)
		})
	}
}

// Keep the independent 200 ms whole-Run deadline contract. The actual child
// deliberately withholds the handshake; this test does not pretend that a
// deadline guarantees the child reached its first instruction or wrote a PID.
func TestBlockedTransportStartupDeadlinePrecedesReadiness(t *testing.T) {
	readyfile := t.TempDir() + "/ready"
	invoked, waited := false, false
	start := time.Now()
	_, err := (Runner{Timeout: 200 * time.Millisecond, ObservePhase: func(o RunObservation) {
		invoked = invoked || o.Phase == PhaseInvokeBegin
		waited = waited || o.Phase == PhaseCleanupWaitEnd
	}}).Run(context.Background(), largeCommand(t, "block_startup", readyfile), fixtureManifest(), "Fixture", "invoke", nil, nil)
	if codeOf(err) != "execution_canceled" || invoked || !waited || time.Since(start) > 3*time.Second {
		t.Fatal("whole-Run startup deadline changed", err, invoked, waited, time.Since(start))
	}
	if raw, readErr := os.ReadFile(readyfile); readErr == nil {
		var ready blockedTransportReady
		if json.Unmarshal(raw, &ready) != nil || ready.PID <= 0 || ready.Scenario != "block_startup" || ready.ReadBytes != 0 {
			t.Fatalf("invalid startup child record: %q", raw)
		}
		if err := syscall.Kill(ready.PID, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatal("startup child not reaped", ready.PID, err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal("read startup child record", readErr)
	}
}

func blockedTransportWorker(t *testing.T, scenario, readyfile string) {
	t.Helper()
	ready := blockedTransportReady{PID: os.Getpid(), Scenario: scenario}
	publish := func() {
		raw, err := json.Marshal(ready)
		if err != nil {
			t.Fatal("encode child readiness", err)
		}
		if err := os.WriteFile(readyfile+".tmp", raw, 0600); err != nil {
			t.Fatal("write child readiness", err)
		}
		if err := os.Rename(readyfile+".tmp", readyfile); err != nil {
			t.Fatal("publish child readiness", err)
		}
	}
	if scenario == "block_startup" {
		publish()
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	reader, writer := bufio.NewReaderSize(os.Stdin, 4096), json.NewEncoder(os.Stdout)
	identity := Identity{PluginID: "Fixture", InstanceID: "Fixture", Version: "1.2.3"}
	readCall := func(method string) Frame {
		raw, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal("read child setup/chunk", method, err)
		}
		ready.ReadBytes += len(raw)
		var frame Frame
		if json.Unmarshal(raw, &frame) != nil || frame.Protocol != ProtocolVersion || frame.Identity != identity || frame.Type != "call" || frame.Method != method {
			t.Fatal("unexpected child setup/chunk", method)
		}
		return frame
	}
	reply := func(frame Frame, result json.RawMessage) {
		if err := writer.Encode(Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: "result", Identity: frame.Identity, Result: result}); err != nil {
			t.Fatal("write child setup reply", frame.Method, err)
		}
	}
	frame := readCall("handshake")
	hello, err := json.Marshal(Handshake{Protocol: ProtocolVersion, PluginID: "Fixture", Version: "1.2.3", HostAPI: HostAPIVersion, Methods: fixtureManifest().Methods, Capabilities: fixtureManifest().Capabilities, Transport: &transportAdvertisement{Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	reply(frame, hello)
	frame = readCall("transport.negotiate")
	var limits transportLimits
	if strictJSON(frame.Payload, &limits) != nil || limits.valid() != nil {
		t.Fatal("invalid child transport negotiation")
	}
	reply(frame, frame.Payload)
	if scenario == "block_write" {
		// ReadBytes consumed the setup newlines. The host cannot send any
		// invocation before our negotiation reply, so this byte comes from
		// the first invocation chunk, not leftover setup or payload encoding.
		first, err := reader.ReadByte()
		if err != nil || first != '{' {
			t.Fatal("read first invocation byte", first, err)
		}
		ready.ReadBytes++
	} else if scenario == "block_ack" {
		frame = readCall("transport.chunk")
		var chunk transportChunk
		if strictJSON(frame.Payload, &chunk) != nil || chunk.Sequence != 0 || chunk.Last || chunk.LogicalType != "call" || len(chunk.Data) == 0 || chunk.TotalBytes <= len(chunk.Data) {
			t.Fatal("invalid first invocation chunk")
		}
	} else {
		t.Fatal("unknown blocked transport scenario", scenario)
	}
	publish()
	// No more stdin reads and no chunk ACK. The parent must cancel Run and
	// reap this actual process, not let a cooperative worker return success.
	time.Sleep(20 * time.Second)
	os.Exit(0)
}

func TestLargeWorker(t *testing.T) {
	at := -1
	for i, v := range os.Args {
		if v == "--" {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	scenario := os.Args[at+1]
	args := os.Args[at+2:]
	if scenario == "old" {
		e := old.Serve(context.Background(), os.Stdin, os.Stdout, oldManifest(), func(ctx context.Context, m string, p json.RawMessage, h old.HostCaller) (json.RawMessage, error) {
			if m == "stop" {
				return json.RawMessage(`null`), nil
			}
			return h(ctx, "data.read", p)
		})
		if e != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if strings.HasPrefix(scenario, "block_") {
		blockedTransportWorker(t, scenario, args[0])
		return
	}
	var oldHost HostCaller
	e := Serve(context.Background(), os.Stdin, os.Stdout, fixtureManifest(), func(ctx context.Context, method string, p json.RawMessage, host HostCaller) (json.RawMessage, error) {
		if method == "stop" {
			if scenario == "late" {
				if _, e := oldHost(ctx, "data.read", json.RawMessage(`{}`)); e == nil {
					return nil, fmt.Errorf("old callback revived")
				}
				if _, e := host(ctx, "data.read", json.RawMessage(`{}`)); e != nil {
					return nil, e
				}
			}
			return json.RawMessage(`null`), nil
		}
		switch scenario {
		case "four":
			return host(ctx, "data.read", p)
		case "cancel_active":
			short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			defer cancel()
			_, err := host(short, "data.read", blobJSON(2<<20))
			if err == nil {
				return nil, fmt.Errorf("active call was not canceled")
			}
			return nil, err
		case "oversize_after_host":
			if _, e := host(ctx, "data.read", json.RawMessage(`{}`)); e != nil {
				return nil, e
			}
			return blobJSON(17 << 20), nil
		case "late":
			oldHost = host
			return json.RawMessage(`{"ok":true}`), nil
		case "concurrent":
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					raw := blobJSON(128 << 10)
					got, e := host(ctx, "data.read", raw)
					if e == nil && !bytes.Equal(got, raw) {
						e = fmt.Errorf("reply changed")
					}
					errs <- e
				}()
			}
			wg.Wait()
			for range 8 {
				if e := <-errs; e != nil {
					return nil, e
				}
			}
			return json.RawMessage(`{"ok":true}`), nil
		case "gate":
			done := make(chan error, 1)
			go func() { _, e := host(ctx, "data.read", json.RawMessage(`{}`)); done <- e }()
			deadline := time.Now().Add(2 * time.Second)
			for {
				if _, e := os.Stat(args[0]); e == nil {
					break
				}
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("no marker")
				}
				time.Sleep(time.Millisecond)
			}
			wait, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			defer cancel()
			if _, e := host(wait, "data.read", json.RawMessage(`{}`)); e == nil {
				return nil, fmt.Errorf("waiting call succeeded")
			}
			if e := <-done; e != nil {
				return nil, e
			}
			return json.RawMessage(`{"ok":true}`), nil
		}
		return nil, fmt.Errorf("unknown scenario")
	})
	if e != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func receiverForTest() *wireSession {
	s := &wireSession{ctx: context.Background(), identity: Identity{"Fixture", "Fixture", "1.2.3"}, role: "host"}
	s.enable(transportLimits{1, 4096, 32 << 10, 1 << 20, 1 << 20, 1024, 100, 100, 8})
	return s
}
func chunkForTest(s *wireSession, seq int, data []byte, last bool, total int) Frame {
	p, _ := json.Marshal(transportChunk{"transfer", "logical", "result", seq, total, last, data})
	return Frame{Protocol: 1, ID: fmt.Sprint("control", seq), Type: "host_call", Identity: s.identity, Method: "transport.chunk", Payload: p}
}
func TestReassemblyRejectsMalformedTransfersBeforeBusinessDispatch(t *testing.T) {
	s := receiverForTest()
	original, _ := json.Marshal(Frame{Protocol: 1, ID: "logical", Type: "result", Identity: s.identity, Result: json.RawMessage(`{"ok":true}`)})
	half := len(original) / 2
	for _, name := range []string{"empty", "huge", "first_sequence", "identity", "type", "duplicate", "skip", "transfer", "logical", "length", "premature_last", "missing_last", "overflow", "unknown_field", "replay"} {
		t.Run(name, func(t *testing.T) {
			s := receiverForTest()
			first := chunkForTest(s, 0, original[:half], false, len(original))
			next := chunkForTest(s, 1, original[half:], true, len(original))
			switch name {
			case "empty":
				first = chunkForTest(s, 0, nil, false, len(original))
			case "huge":
				first = chunkForTest(s, 0, original[:half], false, defaultLogicalBytes+1)
			case "first_sequence":
				first = chunkForTest(s, 1, original[:half], false, len(original))
			case "identity":
				first.InstanceID = "Other"
			case "type":
				first.Type = "call"
			case "premature_last":
				first = chunkForTest(s, 0, original[:half], true, len(original))
			case "unknown_field":
				first.Payload = append(first.Payload[:len(first.Payload)-1], []byte(`,"unknown":true}`)...)
			}
			_, complete, e := s.acceptChunk(first)
			switch name {
			case "empty", "huge", "first_sequence", "identity", "type", "premature_last", "unknown_field":
				if e == nil || complete != nil {
					t.Fatal("malformed first chunk accepted")
				}
				return
			}
			if e != nil || complete != nil {
				t.Fatal("valid prefix rejected", e)
			}
			var c transportChunk
			json.Unmarshal(next.Payload, &c)
			switch name {
			case "duplicate":
				c.Sequence = 0
			case "skip":
				c.Sequence = 2
			case "transfer":
				c.TransferID = "other"
			case "logical":
				c.LogicalID = "other"
			case "length":
				c.TotalBytes++
			case "missing_last":
				c.Last = false
			case "overflow":
				c.Data = append(c.Data, 'x')
			}
			next.Payload, _ = json.Marshal(c)
			_, complete, e = s.acceptChunk(next)
			if name == "replay" {
				if e != nil || complete == nil {
					t.Fatal("valid complete rejected", e)
				}
				_, complete, e = s.acceptChunk(first)
			}
			if e == nil || complete != nil {
				t.Fatal("invalid/replayed complete reached business boundary")
			}
		})
	}
	// EOF after a valid non-final chunk cannot return a partial Frame.
	prefix := chunkForTest(s, 0, original[:half], false, len(original))
	wire, _ := json.Marshal(prefix)
	s.reader = bufio.NewReader(bytes.NewReader(append(wire, '\n')))
	s.writer = io.Discard
	if _, e := s.read(); codeOf(e) != "incomplete_transport_message" {
		t.Fatal("truncated transfer accepted", e)
	}
}

func TestActiveReverseChunkCancellationStopsBeforeHostDispatch(t *testing.T) {
	calls := 0
	start := time.Now()
	got, e := (Runner{MaxFrameBytes: 4096, Timeout: 5 * time.Second}).Run(context.Background(), largeCommand(t, "cancel_active"), fixtureManifest(), "Fixture", "invoke", json.RawMessage(`{}`), func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, nil
	})
	if e == nil || time.Since(start) > 3*time.Second || calls != 0 || got.HostCalls != 0 {
		t.Fatal("canceled partial host call escaped transport boundary", e, calls, time.Since(start))
	}
}
func TestPhysicalBudgetsCountEveryWireByte(t *testing.T) {
	s := receiverForTest()
	f := Frame{Protocol: 1, ID: "a", Type: "result", Identity: s.identity, Result: json.RawMessage(`null`)}
	raw, _ := json.Marshal(f)
	line := append(raw, '\n')
	s.maxOutput = len(line)*2 - 1
	s.reader = bufio.NewReader(bytes.NewReader(append(append([]byte{}, line...), line...)))
	if _, e := s.readPhysical(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.readPhysical(); codeOf(e) != "output_limit" || s.outputBytes != len(line)*2 {
		t.Fatal("inbound cumulative accounting", e, s.outputBytes)
	}
	var out bytes.Buffer
	s.writer = &out
	s.maxInput = len(line)*2 - 1
	if e := s.writePhysical(f); e != nil {
		t.Fatal(e)
	}
	if e := s.writePhysical(f); codeOf(e) != "input_limit" || s.inputBytes != len(line) || out.Len() != len(line) {
		t.Fatal("outbound cumulative accounting", e, s.inputBytes, out.Len())
	}
}

type ackScript struct {
	queue  bytes.Buffer
	s      *wireSession
	change func(*Frame, *transportACK)
}

func (a *ackScript) Read(p []byte) (int, error) { return a.queue.Read(p) }
func (a *ackScript) Write(p []byte) (int, error) {
	var request Frame
	if json.Unmarshal(bytes.TrimSpace(p), &request) != nil {
		return 0, fmt.Errorf("bad scripted request")
	}
	var c transportChunk
	json.Unmarshal(request.Payload, &c)
	value := transportACK{c.TransferID, c.Sequence}
	reply := Frame{Protocol: 1, ID: request.ID, Type: "result", Identity: a.s.identity}
	a.change(&reply, &value)
	reply.Result, _ = json.Marshal(value)
	raw, _ := json.Marshal(reply)
	a.queue.Write(raw)
	a.queue.WriteByte('\n')
	return len(p), nil
}
func TestChunkSenderRejectsUnexpectedACKs(t *testing.T) {
	for _, name := range []string{"identity", "id", "kind", "transfer", "sequence"} {
		t.Run(name, func(t *testing.T) {
			s := receiverForTest()
			s.role = "host"
			script := &ackScript{s: s, change: func(f *Frame, a *transportACK) {
				switch name {
				case "identity":
					f.InstanceID = "Other"
				case "id":
					f.ID = "wrong"
				case "kind":
					f.Type = "host_result"
				case "transfer":
					a.TransferID = "wrong"
				case "sequence":
					a.Sequence++
				}
			}}
			s.writer = script
			s.reader = bufio.NewReader(script)
			f := Frame{Protocol: 1, ID: "logical", Type: "call", Identity: s.identity, Method: "invoke", Payload: blobJSON(12 << 10)}
			if e := s.write(f); codeOf(e) != "invalid_transport_ack" {
				t.Fatal("unrelated ACK accepted", e)
			}
		})
	}
}

func TestLogicalEnvelopeAllowsFullStorageValueAndRejectsOverflow(t *testing.T) {
	s := receiverForTest()
	s.enable(transportLimits{1, 1 << 20, MaxLogicalMessageBytes, defaultWireBytes, defaultWireBytes, defaultChunkBytes, 16384, 32768, 68})
	script := &ackScript{s: s, change: func(*Frame, *transportACK) {}}
	s.writer = script
	s.reader = bufio.NewReader(script)
	// A legal 16 MiB KV scalar needs its scoped key/value wrapper as well as
	// JSON-RPC identity. The envelope allowance must not shrink the storage cap.
	value := `"` + strings.Repeat("x", MaxPayloadBytes-2) + `"`
	wrapped := json.RawMessage(`{"key":"history","value":` + value + `}`)
	f := Frame{Protocol: 1, ID: "large", Type: "call", Identity: s.identity, Method: "invoke", Payload: wrapped}
	if len(wrapped) <= MaxPayloadBytes {
		t.Fatal("fixture omitted the real wrapper")
	}
	if e := s.write(f); e != nil {
		t.Fatal("full storage value lost envelope allowance", e)
	}
	before := s.inputBytes
	f.Payload = blobJSON(MaxLogicalMessageBytes)
	if e := s.write(f); codeOf(e) != "message_limit" || s.inputBytes != before {
		t.Fatal("logical overflow wrote a partial message", e)
	}
}

func TestActualMaximumPayloadFourWays(t *testing.T) {
	raw := json.RawMessage(`"` + strings.Repeat("x", MaxPayloadBytes-2) + `"`)
	calls := 0
	got, e := (Runner{Timeout: 90 * time.Second}).Run(context.Background(), largeCommand(t, "four"), fixtureManifest(), "Fixture", "invoke", raw, func(_ context.Context, _ Identity, _ string, p json.RawMessage) (json.RawMessage, error) {
		calls++
		if !bytes.Equal(p, raw) {
			t.Error("maximum payload changed")
		}
		return raw, nil
	})
	if e != nil || calls != 1 || got.HostCalls != 1 || !bytes.Equal(got.Result, raw) {
		t.Fatalf("maximum actual four-way payload failed err=%v calls=%d bytes=%d", e, calls, len(got.Result))
	}
}

func TestOversizedInvocationRejectedBeforeStartingWorker(t *testing.T) {
	got, e := (Runner{}).Run(context.Background(), Command{Path: "/does/not/exist"}, fixtureManifest(), "Fixture", "invoke", blobJSON(MaxLogicalMessageBytes), nil)
	if codeOf(e) != "invalid_invocation" || got.HostCalls != 0 || got.SentWireBytes != 0 {
		t.Fatal("oversized input started or touched a worker", e)
	}
}

func TestWorkerReplyReportsOnlyUnwrittenTransportFailure(t *testing.T) {
	for _, item := range []struct{ local, remote string }{
		{"message_limit", "message_limit"},
		{"frame_limit", "frame_limit"},
		{"input_limit", "output_limit"},
		{"transport_chunk_limit", "transport_chunk_limit"},
	} {
		t.Run(item.local, func(t *testing.T) {
			s := receiverForTest()
			s.role = "worker"
			s.enable(transportLimits{1, 4096, MaxLogicalMessageBytes, defaultWireBytes, defaultWireBytes, defaultChunkBytes, 16384, 32768, 68})
			var wire bytes.Buffer
			s.writer = &wire
			f := Frame{Protocol: 1, ID: "unwritten", Type: "result", Identity: s.identity, Result: blobJSON(12 << 10)}
			switch item.local {
			case "message_limit":
				s.limits.LogicalBytes = 4096
			case "frame_limit":
				s.enabled = false
			case "input_limit":
				f.Result = blobJSON(1024)
				s.maxInput = 1024
			case "transport_chunk_limit":
				s.sentChunks = s.limits.SessionChunks
			}
			if e := s.writeWorkerReply(f); codeOf(e) != item.local {
				t.Fatal("local failure lost", e)
			}
			var reply Frame
			if e := strictJSON(wire.Bytes(), &reply); e != nil || reply.Error == nil || codeOf(workerReplyFailure(reply.Error.Code)) != item.remote || reply.Result != nil {
				t.Fatal("safe reply did not preserve fixed error", e, wire.String())
			}
		})
	}
	s := receiverForTest()
	s.role = "worker"
	s.enable(transportLimits{1, 4096, MaxLogicalMessageBytes, defaultWireBytes, defaultWireBytes, defaultChunkBytes, 16384, 32768, 68})
	writer := &partialWriteForTest{}
	s.writer = writer
	f := Frame{Protocol: 1, ID: "partial", Type: "result", Identity: s.identity, Result: blobJSON(12 << 10)}
	if e := s.writeWorkerReply(f); codeOf(e) != "worker_io_failed" || writer.calls != 1 || s.inputBytes != 17 {
		t.Fatal("partial transfer was followed by a misleading ordinary error", e, writer.calls, s.inputBytes)
	}
	if codeOf(workerReplyFailure("untrusted arbitrary error")) != "plugin_operation_failed" {
		t.Fatal("unrecognized worker error escaped sanitization")
	}
}

type partialWriteForTest struct{ calls int }

func (w *partialWriteForTest) Write(p []byte) (int, error) {
	w.calls++
	return min(17, len(p)), io.ErrClosedPipe
}

func TestKnownReadQuotaFailureSurvivesPartialAssembly(t *testing.T) {
	s := receiverForTest()
	original, _ := json.Marshal(Frame{Protocol: 1, ID: "logical", Type: "result", Identity: s.identity, Result: blobJSON(2048)})
	first := chunkForTest(s, 0, original[:1024], false, len(original))
	second := chunkForTest(s, 1, original[1024:2048], false, len(original))
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	wire := append(append(append(a, '\n'), b...), '\n')
	s.maxOutput = len(wire) - 1
	s.reader = bufio.NewReader(bytes.NewReader(wire))
	s.writer = io.Discard
	if _, e := s.read(); codeOf(e) != "output_limit" || s.outputBytes != len(wire) {
		t.Fatal("known quota failure became an ambiguous truncation", e, s.outputBytes)
	}
}

func TestReservedReverseTransportMethodsNeverReachHostHandler(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := receiverForTest()
		s.enabled = enabled
		s.maxHostCalls = 64
		calls := 0
		s.host = func(context.Context, Identity, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`null`), nil
		}
		var out bytes.Buffer
		s.writer = &out
		raw, _ := json.Marshal(Frame{Protocol: 1, ID: "reverse", Type: "host_call", Identity: s.identity, Method: "transport.negotiate", Payload: json.RawMessage(`{}`)})
		s.reader = bufio.NewReader(bytes.NewReader(append(raw, '\n')))
		if _, e := s.exchange("invoke", json.RawMessage(`{}`)); codeOf(e) != "invalid_host_call" || calls != 0 || s.hostCalls != 0 {
			t.Fatal("transport method reached business dispatch", enabled, e, calls)
		}
	}
}
