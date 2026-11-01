// Package materialio owns private invocation transport spools. It does not
// authorize a plugin, select a backup path, or implement a persistent cache.
package materialio

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
)

const CopyChunkBytes = 256 << 10 // Per-IO working space, never a total quota.

var (
	ErrClosed        = errors.New("material spool revoked")
	ErrConsumed      = errors.New("material spool already consumed or failed")
	ErrOffset        = errors.New("material spool offset mismatch")
	ErrLength        = errors.New("material spool length mismatch")
	ErrDigest        = errors.New("material spool digest mismatch")
	ErrOverflow      = errors.New("material spool length overflow")
	ErrIdentity      = errors.New("material spool file identity changed")
	ErrGuardRequired = errors.New("material spool requires a current authority guard")
)

// Guard must validate current host authority, including its selected directory.
// It must not reenter this Spool. The directory is host-owned and already
// authorized, not a worker pathname. This package never creates that directory.
type Guard func(context.Context) error

type Summary struct {
	Size   int64
	SHA256 [sha256.Size]byte
}

// Spool uses lazy disk growth. Claim is a one-use transition even on a failed
// length/digest check. No file path or file descriptor is exposed to consumers.
// Call Close on all paths; cancellation of New's context also calls Close.
type Spool struct {
	mu        sync.Mutex
	revoked   atomic.Bool
	closeOnce sync.Once
	closeErr  error
	ctx       context.Context
	guard     Guard
	root      *os.Root
	file      *os.File
	name      string
	identity  os.FileInfo
	digest    hash.Hash
	size      int64
	consumed  bool
	failure   error
	stop      func() bool
}

// New creates exactly one unpredictable O_EXCL/0600 regular file below an
// already-authorized private directory. os.Root confines all pathname IO;
// O_EXCL rejects an existing name, including a symlink, rather than following it.
func New(ctx context.Context, directory string, guard Guard) (*Spool, error) {
	if ctx == nil || guard == nil {
		return nil, ErrGuardRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	if err = guard(ctx); err == nil {
		err = ctx.Err()
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		root.Close()
		return nil, err
	}
	name := ".material-" + hex.EncodeToString(nonce[:]) + ".spool"
	if err = guard(ctx); err == nil {
		err = ctx.Err()
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		root.Close()
		return nil, err
	}
	s := &Spool{ctx: ctx, guard: guard, root: root, file: f, name: name, digest: sha256.New()}
	s.identity, err = f.Stat()
	if err == nil && !s.identity.Mode().IsRegular() {
		err = ErrIdentity
	}
	if err == nil {
		err = s.check(ctx)
	}
	if err != nil {
		return nil, errors.Join(err, s.Close())
	}
	// The mutex makes publication of stop safe if an already-cancelled context
	// starts its callback before AfterFunc returns.
	s.mu.Lock()
	s.stop = context.AfterFunc(ctx, func() { _ = s.Close() })
	s.mu.Unlock()
	if err = s.check(ctx); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

func (s *Spool) check(ctx context.Context) error {
	if ctx == nil {
		return ErrClosed
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.revoked.Load() {
		return ErrClosed
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.revoked.Load() {
		return ErrClosed
	}
	return nil
}

func (s *Spool) writable(ctx context.Context) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if s.consumed || s.failure != nil {
		return errors.Join(ErrConsumed, s.failure)
	}
	return nil
}

func checkedLength(offset int64, n int) (int64, error) {
	if offset < 0 || n < 0 || int64(n) > math.MaxInt64-offset {
		return 0, ErrOverflow
	}
	return offset + int64(n), nil
}

// Append accepts only the exact next offset. The returned offset accounts for
// bytes physically written even on error; an error permanently poisons writes.
// A partially written file is not claimed or reported as complete.
func (s *Spool) Append(ctx context.Context, offset int64, p []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(ctx, offset, p)
}
func (s *Spool) appendLocked(ctx context.Context, offset int64, p []byte) (int64, error) {
	if err := s.writable(ctx); err != nil {
		return s.size, err
	}
	if offset != s.size {
		s.failure = ErrOffset
		return s.size, s.failure
	}
	if _, err := checkedLength(offset, len(p)); err != nil {
		s.failure = err
		return s.size, err
	}
	for len(p) > 0 {
		if err := s.check(ctx); err != nil {
			s.failure = err
			return s.size, err
		}
		step := len(p)
		if step > CopyChunkBytes {
			step = CopyChunkBytes
		}
		n, err := s.file.Write(p[:step])
		if n > 0 {
			_, _ = s.digest.Write(p[:n])
			s.size += int64(n)
			p = p[n:]
		}
		// Current authority/cancellation takes priority over an ordinary IO error.
		if checkErr := s.check(ctx); checkErr != nil {
			s.failure = checkErr
			return s.size, checkErr
		}
		if err == nil && n != step {
			err = io.ErrShortWrite
		}
		if err != nil {
			s.failure = err
			return s.size, err
		}
	}
	return s.size, nil
}

type writer struct {
	spool *Spool
	ctx   context.Context
}

// Writer supplies the same sequential, checked append path to streaming codecs.
func (s *Spool) Writer(ctx context.Context) io.Writer { return &writer{s, ctx} }
func (w *writer) Write(p []byte) (int, error) {
	s := w.spool
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.size
	after, err := s.appendLocked(w.ctx, before, p)
	return int(after - before), err
}

// CopyFrom does not own r. Its owner must cancel/close a blocking external
// reader; ctx checks cannot interrupt arbitrary caller code inside Read.
func (s *Spool) CopyFrom(ctx context.Context, r io.Reader) (int64, error) {
	if err := s.check(ctx); err != nil {
		return 0, err
	}
	// Hide ReaderFrom/WriterTo fast paths so all destination IO uses Writer.
	n, err := io.CopyBuffer(s.Writer(ctx), struct{ io.Reader }{r}, make([]byte, CopyChunkBytes))
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkErr := s.check(ctx); checkErr != nil {
		err = checkErr
	}
	if err != nil && s.failure == nil {
		s.failure = err
	}
	return n, err
}
func (s *Spool) summary() Summary {
	out := Summary{Size: s.size}
	copy(out.SHA256[:], s.digest.Sum(nil))
	return out
}
func (s *Spool) Snapshot(ctx context.Context) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return Summary{}, err
	}
	if s.failure != nil {
		return Summary{}, errors.Join(ErrConsumed, s.failure)
	}
	return s.summary(), nil
}

// Claim is atomic with append/revoke. All attempts consume the writable state,
// including mismatched length/digest; no failed submission can be resurrected.
// Successful Claim keeps the spool alive until Reader.Close/Spool.Close.
func (s *Spool) Claim(ctx context.Context, expectedSize int64, expectedSHA256 [sha256.Size]byte) (*Reader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumed {
		return nil, ErrConsumed
	}
	s.consumed = true
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if s.failure != nil {
		return nil, errors.Join(ErrConsumed, s.failure)
	}
	result := s.summary()
	if expectedSize < 0 || result.Size != expectedSize {
		return nil, ErrLength
	}
	if result.SHA256 != expectedSHA256 {
		return nil, ErrDigest
	}
	if err := s.file.Sync(); err != nil {
		s.failure = err
		return nil, err
	}
	if err := s.check(ctx); err != nil {
		s.failure = err
		return nil, err
	}
	st, err := s.file.Stat()
	if err == nil && (!st.Mode().IsRegular() || !os.SameFile(s.identity, st) || st.Size() != s.size) {
		err = ErrIdentity
	}
	if err != nil {
		s.failure = err
		return nil, err
	}
	if err = s.check(ctx); err != nil {
		s.failure = err
		return nil, err
	}
	return &Reader{spool: s, ctx: ctx, summary: result}, nil
}

// Reader is the only claimed immutable-content view. ReadAt does not change the
// sequential cursor. Reads use bounded staging so a rejected post-read guard
// never publishes that page. Earlier successfully returned pages stay owned by
// their caller; Close never clears caller memory.
type Reader struct {
	spool    *Spool
	ctx      context.Context
	summary  Summary
	position int64 // protected by spool.mu
	scratch  [CopyChunkBytes]byte
}

func (r *Reader) Size() int64               { return r.summary.Size }
func (r *Reader) SHA256() [sha256.Size]byte { return r.summary.SHA256 }
func (r *Reader) Read(p []byte) (int, error) {
	s := r.spool
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := r.readPageLocked(r.ctx, p, r.position)
	r.position += int64(n)
	return n, err
}
func (r *Reader) ReadAt(p []byte, offset int64) (int, error) {
	return r.ReadAtContext(r.ctx, p, offset)
}
func (r *Reader) ReadAtContext(ctx context.Context, p []byte, offset int64) (int, error) {
	if _, err := checkedLength(offset, len(p)); err != nil {
		return 0, err
	}
	total := 0
	for {
		s := r.spool
		s.mu.Lock()
		n, err := r.readPageLocked(ctx, p[total:], offset+int64(total))
		s.mu.Unlock()
		total += n
		if total == len(p) {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return total, err
		}
		if err != nil {
			return total, err
		}
	}
}
func (r *Reader) readPageLocked(ctx context.Context, p []byte, offset int64) (int, error) {
	s := r.spool
	if err := s.check(r.ctx); err != nil {
		return 0, err
	}
	if err := s.check(ctx); err != nil {
		return 0, err
	}
	if s.failure != nil {
		return 0, errors.Join(ErrConsumed, s.failure)
	}
	if offset < 0 {
		return 0, ErrOffset
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.summary.Size {
		return 0, io.EOF
	}
	step := len(p)
	if step > len(r.scratch) {
		step = len(r.scratch)
	}
	if remaining := r.summary.Size - offset; int64(step) > remaining {
		step = int(remaining)
	}
	n, err := s.file.ReadAt(r.scratch[:step], offset)
	if checkErr := s.check(r.ctx); checkErr != nil {
		return 0, checkErr
	}
	if checkErr := s.check(ctx); checkErr != nil {
		return 0, checkErr
	}
	copy(p, r.scratch[:n])
	return n, err
}
func (r *Reader) Close() error { return r.spool.Close() }
func (s *Spool) Revoke() error { return s.Close() }

// Close first revokes without waiting for the IO mutex, then joins current file
// IO and closes/removes only its own created inode/name. No directory scan,
// recursive removal, crash sweep, old cache or backup deletion is performed.
func (s *Spool) Close() error {
	s.revoked.Store(true)
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stop != nil {
			s.stop()
		}
		closeErr := s.file.Close()
		// An already-closed file still needs its owned name removed.
		if errors.Is(closeErr, os.ErrClosed) {
			closeErr = nil
		}
		st, statErr := s.root.Lstat(s.name)
		var removeErr error
		switch {
		case os.IsNotExist(statErr):
		case statErr != nil:
			removeErr = statErr
		case s.identity == nil || !st.Mode().IsRegular() || !os.SameFile(s.identity, st):
			removeErr = ErrIdentity
		default:
			removeErr = s.root.Remove(s.name)
		}
		s.closeErr = errors.Join(closeErr, removeErr, s.root.Close())
	})
	return s.closeErr
}
