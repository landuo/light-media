package materialio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func ownedSpool(t *testing.T, ctx context.Context, guard Guard) (*Spool, string) {
	t.Helper()
	dir := t.TempDir()
	if guard == nil {
		guard = func(context.Context) error { return nil }
	}
	s, err := New(ctx, dir, guard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, dir
}
func emptyOwnedDir(t *testing.T, dir string) {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("owned cleanup: %v, entries=%d", err, len(files))
	}
}

type patternReader struct {
	left, offset int64
	hash         hash.Hash
}

func (r *patternReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.left {
		n = int(r.left)
	}
	for i := 0; i < n; i++ {
		p[i] = byte((r.offset + int64(i)) % 251)
	}
	r.offset += int64(n)
	r.left -= int64(n)
	_, _ = r.hash.Write(p[:n])
	return n, nil
}

// One bounded witness crosses both old 16MiB and 64MiB totals. This is target
// IO verification, not a source/SDK/host/network or unlimited-resource claim.
func TestSpoolOwnedStreamingAndEmpty(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	untouched := filepath.Join(dir, "existing-backup.torrent")
	if err := os.WriteFile(untouched, []byte("owned backup must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(untouched)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, dir, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	st, err := s.file.Stat()
	if err != nil || st.Mode().Perm() != 0600 || !st.Mode().IsRegular() {
		t.Fatalf("private file: %v %v", st, err)
	}
	const total = int64(65<<20) + 1
	input := &patternReader{left: total, hash: sha256.New()}
	n, err := s.CopyFrom(ctx, input)
	if err != nil || n != total {
		t.Fatalf("copy: %d %v", n, err)
	}
	sum, err := s.Snapshot(ctx)
	if err != nil || sum.Size != total || !bytes.Equal(sum.SHA256[:], input.hash.Sum(nil)) {
		t.Fatalf("summary: %+v %v", sum, err)
	}
	r, err := s.Claim(ctx, sum.Size, sum.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if r.Size() != total || r.SHA256() != sum.SHA256 {
		t.Fatal("claimed summary changed")
	}
	page := make([]byte, CopyChunkBytes+13)
	nInt, err := r.ReadAtContext(ctx, page, 31)
	if err != nil || nInt != len(page) {
		t.Fatalf("ReadAt: %d %v", nInt, err)
	}
	for i, v := range page {
		if v != byte((31+i)%251) {
			t.Fatalf("ReadAt byte %d", i)
		}
	}
	digest := sha256.New()
	n, err = io.CopyBuffer(digest, r, make([]byte, CopyChunkBytes))
	if err != nil || n != total || !bytes.Equal(digest.Sum(nil), sum.SHA256[:]) {
		t.Fatalf("read: %d %v", n, err)
	}
	if _, err = s.Claim(ctx, total, sum.SHA256); !errors.Is(err, ErrConsumed) {
		t.Fatalf("claim reused: %v", err)
	}
	if _, err = s.Append(ctx, total, []byte("later")); !errors.Is(err, ErrConsumed) {
		t.Fatalf("append after claim: %v", err)
	}
	saved := append([]byte{}, page...)
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, page) {
		t.Fatal("close mutated already returned caller bytes")
	}
	if _, err = r.Read(page); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != filepath.Base(untouched) {
		t.Fatalf("foreign file cleanup: %v %v", files, err)
	}
	raw, err := os.ReadFile(untouched)
	after, statErr := os.Stat(untouched)
	if err != nil || statErr != nil || string(raw) != "owned backup must remain" || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("preexisting backup changed")
	}
	e, emptyDir := ownedSpool(t, ctx, nil)
	empty, err := e.Claim(ctx, 0, sha256.Sum256(nil))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(empty)
	if err != nil || b == nil || len(b) != 0 {
		t.Fatalf("non-nil empty bytes: %v %v", b, err)
	}
	if err = empty.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOwnedDir(t, emptyDir)
}

func TestSpoolValidationAndFailures(t *testing.T) {
	ctx := context.Background()
	raw := []byte("owned exact material")
	sum := sha256.Sum256(raw)
	for _, tc := range []struct {
		name   string
		length int64
		digest [sha256.Size]byte
		want   error
	}{
		{"length", int64(len(raw) + 1), sum, ErrLength},
		{"digest", int64(len(raw)), sha256.Sum256([]byte("other")), ErrDigest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, dir := ownedSpool(t, ctx, nil)
			if _, err := s.Append(ctx, 0, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Claim(ctx, tc.length, tc.digest); !errors.Is(err, tc.want) {
				t.Fatalf("claim: %v", err)
			}
			if _, err := s.Claim(ctx, int64(len(raw)), sum); !errors.Is(err, ErrConsumed) {
				t.Fatalf("failed claim resurrected: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			emptyOwnedDir(t, dir)
		})
	}
	t.Run("offset", func(t *testing.T) {
		s, _ := ownedSpool(t, ctx, nil)
		if _, err := s.Append(ctx, 1, raw); !errors.Is(err, ErrOffset) {
			t.Fatal(err)
		}
		if _, err := s.Append(ctx, 0, raw); !errors.Is(err, ErrConsumed) {
			t.Fatal("wrong-offset write resurrected", err)
		}
	})
	t.Run("checked_integer", func(t *testing.T) {
		if n, err := checkedLength(math.MaxInt64-1, 1); err != nil || n != math.MaxInt64 {
			t.Fatal(n, err)
		}
		for _, tc := range []struct {
			offset int64
			n      int
		}{{math.MaxInt64, 1}, {-1, 0}, {0, -1}} {
			if _, err := checkedLength(tc.offset, tc.n); !errors.Is(err, ErrOverflow) {
				t.Fatal(err)
			}
		}
	})
	t.Run("physical_write_error", func(t *testing.T) {
		s, dir := ownedSpool(t, ctx, nil)
		// Only this owned file is closed; no chmod/permission/symlink probe.
		if err := s.file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Append(ctx, 0, raw); !errors.Is(err, os.ErrClosed) {
			t.Fatal(err)
		}
		if _, err := s.Claim(ctx, 0, sha256.Sum256(nil)); !errors.Is(err, ErrConsumed) {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		emptyOwnedDir(t, dir)
	})
	t.Run("reader_error", func(t *testing.T) {
		s, dir := ownedSpool(t, ctx, nil)
		failure := errors.New("owned reader failed")
		n, err := s.CopyFrom(ctx, &failingReader{err: failure})
		if n != 3 || !errors.Is(err, failure) {
			t.Fatal(n, err)
		}
		if _, err = s.Claim(ctx, 3, sha256.Sum256([]byte("abc"))); !errors.Is(err, ErrConsumed) {
			t.Fatal("partial read claimed", err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
		emptyOwnedDir(t, dir)
	})
}

type failingReader struct {
	err  error
	done bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, "abc"), r.err
}

func TestSpoolCurrentGuardAndCancellation(t *testing.T) {
	ctx := context.Background()
	denied := errors.New("owned current scope revoked")
	var armed atomic.Bool
	var count atomic.Int64
	guard := func(context.Context) error {
		if armed.Load() && count.Add(1) == 3 {
			return denied
		}
		return nil
	}
	s, dir := ownedSpool(t, ctx, guard)
	armed.Store(true)
	next, err := s.Append(ctx, 0, make([]byte, CopyChunkBytes+7))
	if next != CopyChunkBytes || !errors.Is(err, denied) {
		t.Fatalf("post-write guard: %d %v", next, err)
	}
	st, err := s.file.Stat()
	if err != nil || st.Size() != CopyChunkBytes {
		t.Fatalf("actual partial file: %v %v", st, err)
	}
	if _, err = s.Claim(ctx, CopyChunkBytes, sha256.Sum256(make([]byte, CopyChunkBytes))); !errors.Is(err, ErrConsumed) {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOwnedDir(t, dir)

	var readArmed atomic.Bool
	var readCalls atomic.Int64
	rguard := func(context.Context) error {
		if readArmed.Load() && readCalls.Add(1) == 3 {
			return denied
		}
		return nil
	}
	rOwner, _ := ownedSpool(t, ctx, rguard)
	raw := []byte("material")
	if _, err = rOwner.Append(ctx, 0, raw); err != nil {
		t.Fatal(err)
	}
	reader, err := rOwner.Claim(ctx, int64(len(raw)), sha256.Sum256(raw))
	if err != nil {
		t.Fatal(err)
	}
	readArmed.Store(true)
	dest := bytes.Repeat([]byte{0xa5}, len(raw))
	want := append([]byte{}, dest...)
	if n, err := reader.Read(dest); n != 0 || !errors.Is(err, denied) {
		t.Fatal(n, err)
	}
	if !bytes.Equal(dest, want) {
		t.Fatal("failed post-read guard published page")
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}

	ownerCtx, cancel := context.WithCancel(ctx)
	canceled, cancelDir := ownedSpool(t, ownerCtx, nil)
	cancel()
	if _, err = canceled.Append(ctx, 0, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Join the cancellation callback; no timing assumption about its scheduling.
	if err = canceled.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOwnedDir(t, cancelDir)
}

func TestSpoolClaimRevokeConcurrency(t *testing.T) {
	ctx := context.Background()
	raw := []byte("immutable owned material")
	digest := sha256.Sum256(raw)
	s, dir := ownedSpool(t, ctx, nil)
	if _, err := s.Append(ctx, 0, raw); err != nil {
		t.Fatal(err)
	}
	var claims atomic.Int64
	var wg sync.WaitGroup
	var winner *Reader
	var winnerMu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Claim(ctx, int64(len(raw)), digest)
			if err == nil {
				claims.Add(1)
				winnerMu.Lock()
				winner = r
				winnerMu.Unlock()
			} else if !errors.Is(err, ErrConsumed) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("claim count", claims.Load())
	}
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p := make([]byte, len(raw))
			_, err := winner.ReadAt(p, 0)
			if err != nil && !errors.Is(err, ErrClosed) {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if err := s.Revoke(); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOwnedDir(t, dir)

	// Revoke is visible before Close waits on an in-flight owned IO guard.
	entered, release := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	var once sync.Once
	held, heldDir := ownedSpool(t, ctx, func(context.Context) error {
		if block.Load() {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	})
	block.Store(true)
	appendDone := make(chan error, 1)
	go func() { _, err := held.Append(ctx, 0, raw); appendDone <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned IO not entered")
	}
	closed := make(chan error, 1)
	go func() { closed <- held.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for !held.revoked.Load() && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	revoked := held.revoked.Load()
	close(release)
	if !revoked {
		t.Fatal("Close did not revoke before IO join")
	}
	if err := <-appendDone; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	emptyOwnedDir(t, heldDir)
}
