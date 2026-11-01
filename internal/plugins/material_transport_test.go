package plugins

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type materialMeasuredWriter struct {
	writer io.Writer
	bytes  atomic.Int64
}

func (w *materialMeasuredWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes.Add(int64(n))
	return n, err
}
func materialTestLimits() transportLimits {
	return transportLimits{Version: 1, FrameBytes: 1 << 20, LogicalBytes: defaultLogicalBytes, HostSendBytes: defaultWireBytes, HostReadBytes: defaultWireBytes, ChunkBytes: defaultChunkBytes, MessageChunks: 32768, SessionChunks: 65536, Transfers: 68}
}

// This is actual in-process pipe IO through the written serial seam, NOT a
// negotiated Serve/Runner/SDK/consumer proof. The later lifecycle gate is still
// required. The data are fixed protocol test bytes, not a Python-source oracle.
func TestMaterialSerialScopedFramesAndOrdinaryBudgets(t *testing.T) {
	t.Run("over_old_cumulative_limits", func(t *testing.T) { materialSerialWitness(t, 1<<20, (65<<20)+1) })
	t.Run("small_negotiated_frame", func(t *testing.T) { materialSerialWitness(t, 4096, 9001) })
}
func materialSerialWitness(t *testing.T, frameBytes int, total int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	toHostR, toHostW := io.Pipe()
	toWorkerR, toWorkerW := io.Pipe()
	t.Cleanup(func() { toHostR.Close(); toHostW.Close(); toWorkerR.Close(); toWorkerW.Close() })
	readMeter := &materialMeasuredWriter{writer: toHostW}
	writeMeter := &materialMeasuredWriter{writer: toWorkerW}
	identity := materialTestBinding().Identity
	host := &wireSession{ctx: ctx, role: "host", identity: identity, reader: bufio.NewReaderSize(toHostR, 4096), writer: writeMeter, maxFrame: frameBytes, maxInput: defaultWireBytes, maxOutput: defaultWireBytes}
	worker := &wireSession{ctx: ctx, role: "worker", identity: identity, reader: bufio.NewReaderSize(toWorkerR, 4096), writer: readMeter, maxFrame: frameBytes, maxInput: defaultWireBytes, maxOutput: defaultWireBytes}
	host.closeIO = func() { toHostR.Close(); toWorkerW.Close() }
	worker.closeIO = func() { toWorkerR.Close(); toHostW.Close() }
	stop := context.AfterFunc(ctx, func() { host.abort(); worker.abort() })
	defer stop()
	s, err := newMaterialHostScope(ctx, materialTestBinding(), "callLarge", frameBytes, []string{DownloadTorrentCapability}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	block := bytes.Repeat([]byte{0xa5}, MaterialFrameBytes)
	digest := sha256.New()
	for remaining := total; remaining > 0; {
		n := min(int64(len(block)), remaining)
		digest.Write(block[:n])
		remaining -= n
	}
	issue, endIssue, _ := s.hostContext(ctx, DownloadTorrentCapability)
	defer endIssue()
	grant, err := IssueMaterial(issue, MaterialSpec{DownloadTorrentCapability, MaterialToWorker, "bytes", total, hex.EncodeToString(digest.Sum(nil))}, MaterialPort{Read: func(ctx context.Context, _ int64, n int) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return bytes.Repeat([]byte{0xa5}, n), nil
	}, Close: func() error { return nil }}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	pages := int((total + int64(grant.ChunkBytes) - 1) / int64(grant.ChunkBytes))
	if frameBytes == 4096 && grant.ChunkBytes >= 4096 {
		t.Fatal("grant did not honor small negotiated frame")
	}
	ended := make(chan error, 1)
	go func() {
		for range pages + 1 {
			if err := host.materialHostExchange(ctx, s); err != nil {
				ended <- err
				return
			}
		}
		ended <- host.write(Frame{Protocol: ProtocolVersion, ID: "ordinaryAfter", Type: "call", Identity: identity, Method: "owned.after", Payload: json.RawMessage(`{}`)})
	}()
	observed := sha256.New()
	offset := int64(0)
	for offset < total {
		frame, err := materialRequestFrame(identity, grant, "read", offset, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := worker.materialPeerExchange(ctx, s.invocation, frame)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(response.Data, block[:len(response.Data)]) {
			t.Fatal("material bytes changed")
		}
		observed.Write(response.Data)
		offset = response.Next
	}
	if hex.EncodeToString(observed.Sum(nil)) != grant.SHA256 {
		t.Fatal("received SHA differs")
	}
	finish, _ := materialRequestFrame(identity, grant, "finish", offset, nil)
	if reply, err := worker.materialPeerExchange(ctx, s.invocation, finish); err != nil || !reply.Finished {
		t.Fatal("finish", err)
	}
	ordinary, err := worker.read()
	if err != nil || ordinary.ID != "ordinaryAfter" {
		t.Fatal("ordinary traffic charged material bytes", err)
	}
	if err = <-ended; err != nil {
		t.Fatal(err)
	}
	if host.inputBytes != int(writeMeter.bytes.Load()) || host.outputBytes != int(readMeter.bytes.Load()) || worker.inputBytes != host.outputBytes || worker.outputBytes != host.inputBytes {
		t.Fatal("physical counts lost or reset")
	}
	if (total > defaultWireBytes && host.inputBytes <= defaultWireBytes) || host.materialSentFrames != pages+1 || host.hostCalls != 0 {
		t.Fatal("not the intended finite seam witness", host.inputBytes, host.materialSentFrames, host.hostCalls)
	}
	if host.inputBytes-host.materialInputBytes <= 0 {
		t.Fatal("ordinary frame misclassified as material")
	}
	// An unchanged ordinary budget is checked separately from actual pipe stats.
	limited := &wireSession{ctx: ctx, writer: io.Discard, maxInput: 64, inputBytes: 164, materialInputBytes: 100}
	if err := limited.writeEncodedPhysical([]byte(`{}`)); err == nil {
		t.Fatal("ordinary budget widened")
	}
}

// Two independent invocation scopes share the real concurrent writer lock.
// Dispatch here is explicit test orchestration, not the pending production
// readLoop negotiation/routing implementation.
func TestMaterialConcurrentSeamIdentityAccountingAndFrames(t *testing.T) {
	ctx := context.Background()
	identity := materialTestBinding().Identity
	var output bytes.Buffer
	wire := &wireSession{ctx: ctx, role: "host", identity: identity, maxFrame: 1 << 20, writer: &output}
	wire.enable(materialTestLimits())
	transport := &concurrentTransport{wire: wire, ctx: ctx}
	type owned struct {
		scope   *materialHostScope
		grant   MaterialGrant
		account *concurrentAccount
		data    []byte
		request Frame
	}
	rows := []owned{}
	for _, id := range []string{"callA", "callB"} {
		s := materialTestScope(t, id)
		data := []byte("owned-" + id)
		g := materialTestIssue(t, s, DownloadTorrentCapability, MaterialToWorker, data)
		account := &concurrentAccount{limits: materialTestLimits()}
		if err := account.bindMaterialScope(s); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, owned{s, g, account, data, Frame{}})
	}
	totalRead, totalWrite := 0, 0
	for _, op := range []string{"read", "finish"} {
		var input bytes.Buffer
		for i := range rows {
			off := int64(0)
			if op == "finish" {
				off = int64(len(rows[i].data))
			}
			f, err := materialRequestFrame(identity, rows[i].grant, op, off, nil)
			if err != nil {
				t.Fatal(err)
			}
			rows[i].request = f
			b, err := materialEncode(f, wire.maxFrame)
			if err != nil {
				t.Fatal(err)
			}
			input.Write(b)
		}
		totalRead += input.Len()
		wire.reader = bufio.NewReader(bytes.NewReader(input.Bytes()))
		var wg sync.WaitGroup
		errs := make(chan error, len(rows))
		for range rows {
			f, count, err := transport.materialReadLine()
			if err != nil {
				t.Fatal(err)
			}
			var row *owned
			for i := range rows {
				if strings.HasPrefix(f.ID, rows[i].scope.invocation+".") {
					row = &rows[i]
				}
			}
			if row == nil {
				t.Fatal("unknown invocation")
			}
			wg.Add(1)
			go func(row *owned, frame Frame, count concurrentWireCount) {
				defer wg.Done()
				errs <- materialConcurrentHostFrame(ctx, transport, row.scope, row.account, frame, count)
			}(row, f, count)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		totalWrite += output.Len()
		reader := bufio.NewReader(bytes.NewReader(output.Bytes()))
		seen := map[string]bool{}
		for range rows {
			reply, _, err := readMaterialLine(reader, wire.maxFrame)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if reply.ID == row.request.ID {
					v, err := materialAcceptReply(row.request, reply)
					if err != nil {
						t.Fatal(err)
					}
					if op == "read" && !bytes.Equal(v.Data, row.data) {
						t.Fatal("cross invocation bytes")
					}
					if seen[reply.ID] {
						t.Fatal("duplicate reply")
					}
					seen[reply.ID] = true
				}
			}
		}
		if len(seen) != len(rows) {
			t.Fatal("missing reply")
		}
		output.Reset()
	}
	sumRead, sumWrite := 0, 0
	for _, row := range rows {
		snapshot := row.account.snapshot()
		stats := row.account.materialStats()
		if snapshot.HostCalls != 0 || snapshot.ReadWireBytes != stats.ReadBytes || snapshot.SentWireBytes != stats.SentBytes || stats.ReadFrames != 2 || stats.SentFrames != 2 {
			t.Fatal("lost per-invocation physical material accounting", snapshot, stats)
		}
		sumRead += snapshot.ReadWireBytes
		sumWrite += snapshot.SentWireBytes
		if err := row.account.send(concurrentWireCount{bytes: defaultWireBytes}); err != nil {
			t.Fatal("material consumed ordinary budget", err)
		}
		if err := row.account.send(concurrentWireCount{bytes: 1}); err == nil {
			t.Fatal("ordinary wire quota disappeared")
		}
		for range 64 {
			if err := row.account.host(64); err != nil {
				t.Fatal(err)
			}
		}
		if err := row.account.host(64); err == nil {
			t.Fatal("65th ordinary callback accepted")
		}
	}
	if sumRead != totalRead || sumWrite != totalWrite {
		t.Fatal("actual framed byte sums differ", sumRead, totalRead, sumWrite, totalWrite)
	}
}

func TestMaterialCountersCheckedAfterLargeMaterial(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	ctx := context.Background()
	var wrote bytes.Buffer
	s := &wireSession{ctx: ctx, writer: &wrote, maxInput: defaultWireBytes, inputBytes: maxInt - 1, materialInputBytes: maxInt - 1}
	if err := s.writeEncodedPhysical([]byte(`{}`)); err == nil || wrote.Len() != 0 || s.inputBytes != maxInt-1 {
		t.Fatal("ordinary write wrapped total counter")
	}
	read := &wireSession{ctx: ctx, reader: bufio.NewReader(strings.NewReader("{}\n")), maxFrame: 4096, maxOutput: defaultWireBytes, outputBytes: maxInt - 1, materialOutputBytes: maxInt - 1}
	if _, err := read.readPhysical(); err == nil || read.outputBytes < 0 {
		t.Fatal("ordinary read wrapped total counter")
	}
	for _, send := range []bool{false, true} {
		a := &concurrentAccount{limits: materialTestLimits()}
		a.sent.bytes = maxInt - 1
		a.read.bytes = maxInt - 1
		a.materialSentBytes = maxInt - 1
		a.materialReadBytes = maxInt - 1
		var err error
		if send {
			err = a.send(concurrentWireCount{bytes: 3})
		} else {
			err = a.receive(concurrentWireCount{bytes: 3})
		}
		if err == nil || a.sent.bytes < 0 || a.read.bytes < 0 {
			t.Fatal("concurrent ordinary total wrapped", send)
		}
	}
	// Unlike serial material writes, concurrent writes used to discover this
	// overflow AFTER bytes escaped. Reserve before IO; the writer must stay empty.
	var concurrentWritten bytes.Buffer
	wire := &wireSession{ctx: ctx, identity: materialTestBinding().Identity, role: "host", writer: &concurrentWritten, maxFrame: 4096}
	transport := &concurrentTransport{wire: wire, ctx: ctx}
	account := &concurrentAccount{limits: materialTestLimits()}
	account.sent.bytes = maxInt - 1
	account.materialSentBytes = maxInt - 1
	payload, _ := json.Marshal(materialResponse{Token: strings.Repeat("a", 64), Data: []byte{}})
	frame := Frame{Protocol: ProtocolVersion, ID: "counter", Type: "host_result", Identity: wire.identity, Result: payload}
	if err := transport.materialWrite(ctx, frame, account); err == nil || concurrentWritten.Len() != 0 || account.sent.bytes != maxInt-1 || account.materialSentBytes != maxInt-1 {
		t.Fatal("concurrent material write escaped before count preflight")
	}
	// Pending material capacity is protected from concurrent ordinary updates,
	// but does not hold the account lock while the real writer/reader progress.
	reservedAccount := &concurrentAccount{limits: materialTestLimits()}
	reservedAccount.sent.bytes = maxInt - 10
	reservedAccount.materialSentBytes = maxInt - 10
	reservation, err := reservedAccount.reserveMaterialSend(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := reservedAccount.send(concurrentWireCount{bytes: 3}); err == nil {
		t.Fatal("ordinary send consumed reserved representability")
	}
	if err := reservation.finish(3); err != nil {
		t.Fatal(err)
	}
	if reservedAccount.sent.bytes != maxInt-7 || reservedAccount.materialSentBytes != maxInt-7 || reservedAccount.materialReservedBytes != 0 || reservedAccount.materialReservedFrames != 0 {
		t.Fatal("partial material count/reservation mismatch")
	}
	if err := reservation.finish(3); err == nil {
		t.Fatal("reservation completed twice")
	}

}
