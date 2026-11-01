package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func materialTestBinding() SessionBinding {
	return SessionBinding{Identity: Identity{PluginID: "OwnedMaterial", InstanceID: "OwnedMaterial", Version: "1.0.0"}, Generation: 1}
}
func materialTestSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func materialTestScope(t *testing.T, id string) *materialHostScope {
	t.Helper()
	s, err := newMaterialHostScope(context.Background(), materialTestBinding(), id, 1<<20, []string{DownloadTorrentCapability, LocalTorrentBackupCapability, QBittorrentAddCapability, TransmissionAddCapability}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func materialTestIssue(t *testing.T, s *materialHostScope, capability string, direction MaterialDirection, data []byte) MaterialGrant {
	t.Helper()
	ctx, end, err := s.hostContext(context.Background(), capability)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	port := MaterialPort{Close: func() error { return nil }}
	if direction == MaterialToWorker {
		port.Read = func(_ context.Context, offset int64, n int) ([]byte, error) {
			return bytes.Clone(data[offset : offset+int64(n)]), nil
		}
	} else {
		port.Write = func(context.Context, int64, []byte) error { return nil }
	}
	g, err := IssueMaterial(ctx, MaterialSpec{capability, direction, "bytes", int64(len(data)), materialTestSHA(data)}, port, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestMaterialGrantAuthorityAndExactFrames(t *testing.T) {
	s := materialTestScope(t, "callAuthority")
	raw := []byte("owned material bytes")
	port := MaterialPort{Read: func(_ context.Context, offset int64, n int) ([]byte, error) {
		return raw[offset : offset+int64(n)], nil
	}, Close: func() error { return nil }}
	spec := MaterialSpec{DownloadTorrentCapability, MaterialToWorker, "bytes", int64(len(raw)), materialTestSHA(raw)}
	if _, err := IssueMaterial(context.Background(), spec, port, func(context.Context) error { return nil }); err == nil {
		t.Fatal("plain context issued grant")
	}
	ctx, end, err := s.hostContext(context.Background(), DownloadTorrentCapability)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	grant, err := IssueMaterial(ctx, spec, port, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = IssueMaterial(ctx, spec, port, func(context.Context) error { return nil }); err == nil {
		t.Fatal("same callback repeated same kind")
	}
	other := spec
	other.Kind = "result_record"
	other.Total = 0
	other.SHA256 = materialTestSHA(nil)
	if _, err = IssueMaterial(ctx, other, port, func(context.Context) error { return nil }); err != nil {
		t.Fatal("S12 second kind", err)
	}
	// A new ordinary callback ticket in the SAME invocation is not forbidden.
	later, endLater, err := s.hostContext(context.Background(), DownloadTorrentCapability)
	if err != nil {
		t.Fatal(err)
	}
	defer endLater()
	if _, err = IssueMaterial(later, spec, port, func(context.Context) error { return nil }); err != nil {
		t.Fatal("later invocation-local download", err)
	}
	ended, endTicket, err := s.hostContext(context.Background(), DownloadTorrentCapability)
	if err != nil {
		t.Fatal(err)
	}
	endTicket()
	if _, err = IssueMaterial(ended, spec, port, func(context.Context) error { return nil }); err == nil {
		t.Fatal("ended callback ticket issued a grant")
	}

	frame, err := materialRequestFrame(s.binding.Identity, grant, "read", 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	badBase64 := frame
	badBase64.Payload = bytes.Replace(frame.Payload, []byte(`"data":""`), []byte(`"data":"YR=="`), 1)
	if _, err := decodeMaterialRequest(badBase64); err == nil {
		t.Fatal("noncanonical base64 accepted")
	}
	foreign := frame
	foreign.InstanceID = "OtherClone"
	if _, err = s.handle(context.Background(), foreign); err == nil {
		t.Fatal("foreign identity reached port")
	}
	wrong := grant
	wrong.Capability = LocalTorrentBackupCapability
	wrongFrame, _ := materialRequestFrame(s.binding.Identity, wrong, "read", 0, nil)
	if _, err = s.handle(context.Background(), wrongFrame); err == nil {
		t.Fatal("changed grant capability")
	}
	reply, err := s.handle(context.Background(), frame)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := materialAcceptReply(frame, reply)
	if err != nil || !bytes.Equal(decoded.Data, raw) {
		t.Fatal("exact read", err, decoded)
	}
	if _, err = s.handle(context.Background(), frame); err == nil {
		t.Fatal("duplicate offset replay")
	}
	finish, _ := materialRequestFrame(s.binding.Identity, grant, "finish", int64(len(raw)), nil)
	reply, err = s.handle(context.Background(), finish)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = materialAcceptReply(finish, reply)
	if err != nil || !decoded.Finished {
		t.Fatal("finish", err)
	}
	if _, err = s.handle(context.Background(), finish); err == nil {
		t.Fatal("second finish")
	}
	if materialUniqueJSON([]byte(`{"a":1,"a":2}`)) == nil {
		t.Fatal("duplicate key")
	}
	if materialUniqueJSON([]byte(`{"a":[]}`)) == nil {
		t.Fatal("unexpected array")
	}
}

func TestMaterialGrantCurrentGuardAndJoinedRevoke(t *testing.T) {
	binding := materialTestBinding()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var allowed atomic.Bool
	allowed.Store(true)
	guard := func(context.Context) error {
		if !allowed.Load() {
			return errors.New("owned current guard denied")
		}
		return nil
	}
	s, err := newMaterialHostScope(ctx, binding, "callRevoke", 1<<20, []string{DownloadTorrentCapability}, guard)
	if err != nil {
		t.Fatal(err)
	}
	issue, endIssue, err := s.hostContext(ctx, DownloadTorrentCapability)
	if err != nil {
		t.Fatal(err)
	}
	defer endIssue()
	entered := make(chan struct{})
	portDone := make(chan struct{})
	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseClose) })
	var closed atomic.Int32
	grant, err := IssueMaterial(issue, MaterialSpec{DownloadTorrentCapability, MaterialToWorker, "bytes", 1, materialTestSHA([]byte{'x'})}, MaterialPort{
		Read: func(ctx context.Context, _ int64, _ int) ([]byte, error) {
			close(entered)
			<-ctx.Done()
			close(portDone)
			return nil, ctx.Err()
		},
		Close: func() error { closed.Add(1); close(closeEntered); <-releaseClose; return nil },
	}, guard)
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := materialRequestFrame(binding.Identity, grant, "read", 0, nil)
	handled := make(chan error, 1)
	go func() { _, err := s.handle(ctx, frame); handled <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("port did not start")
	}
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- s.close() }()
	select {
	case <-portDone:
	case <-time.After(2 * time.Second):
		t.Fatal("scope close did not cancel in-flight port")
	}
	select {
	case <-closeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("port close did not follow join")
	}
	go func() { second <- s.close() }()
	select {
	case <-second:
		t.Fatal("second close returned before shared close completed")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseClose) })
	for _, ch := range []chan error{first, second} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("close did not complete")
		}
	}
	if err := <-handled; !errors.Is(err, context.Canceled) {
		t.Fatal("lost canceled port", err)
	}
	if closed.Load() != 1 {
		t.Fatal("port closed more than once")
	}
	if _, err = s.handle(ctx, frame); err == nil {
		t.Fatal("closed scope revived")
	}

	s2 := materialTestScope(t, "callGuard")
	var calls atomic.Int32
	issue2, endIssue2, _ := s2.hostContext(context.Background(), DownloadTorrentCapability)
	defer endIssue2()
	g2, err := IssueMaterial(issue2, MaterialSpec{DownloadTorrentCapability, MaterialToWorker, "bytes", 1, materialTestSHA([]byte{'x'})}, MaterialPort{Read: func(context.Context, int64, int) ([]byte, error) {
		calls.Add(1)
		allowed.Store(false)
		return []byte{'x'}, nil
	}, Close: func() error { return nil }}, guard)
	if err != nil {
		t.Fatal(err)
	}
	f2, _ := materialRequestFrame(binding.Identity, g2, "read", 0, nil)
	if _, err = s2.handle(context.Background(), f2); err == nil {
		t.Fatal("post-read guard ignored")
	}
	allowed.Store(true)
	if _, err = s2.handle(context.Background(), f2); err == nil {
		t.Fatal("failed grant restored after guard change")
	}
	if calls.Load() != 1 {
		t.Fatal("failed grant reread", calls.Load())
	}
}

func TestMaterialGrantWriteIntegrityAndConcurrentTerminal(t *testing.T) {
	s := materialTestScope(t, "callWrite")
	issue, endIssue, _ := s.hostContext(context.Background(), QBittorrentAddCapability)
	defer endIssue()
	var got bytes.Buffer
	var writes atomic.Int32
	data := []byte("owned")
	g, err := IssueMaterial(issue, MaterialSpec{QBittorrentAddCapability, MaterialToHost, "bytes", int64(len(data)), materialTestSHA(data)}, MaterialPort{Write: func(_ context.Context, off int64, p []byte) error {
		if off != int64(got.Len()) {
			return errors.New("wrong port offset")
		}
		writes.Add(1)
		_, err := got.Write(p)
		return err
	}, Close: func() error { return nil }}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	f, _ := materialRequestFrame(s.binding.Identity, g, "write", 0, data)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.handle(context.Background(), f); errs <- err }()
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 || writes.Load() != 1 || !bytes.Equal(got.Bytes(), data) {
		t.Fatal("duplicate concurrent append", ok, writes.Load(), got.String())
	}
	finish, _ := materialRequestFrame(s.binding.Identity, g, "finish", int64(len(data)), nil)
	if _, err = s.handle(context.Background(), finish); err != nil {
		t.Fatal(err)
	}
	bad := materialTestIssue(t, s, TransmissionAddCapability, MaterialToHost, []byte("same"))
	wrong, _ := materialRequestFrame(s.binding.Identity, bad, "write", 0, []byte("diff"))
	if _, err = s.handle(context.Background(), wrong); err != nil {
		t.Fatal(err)
	}
	end, _ := materialRequestFrame(s.binding.Identity, bad, "finish", 4, nil)
	if _, err = s.handle(context.Background(), end); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	if _, err = s.handle(context.Background(), end); err == nil {
		t.Fatal("failed digest retry revived")
	}
}
