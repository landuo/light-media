package durable

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const maxStateBytes = 16 << 20

// FileStore is an executable encrypted standalone store, not an App database
// migration. Every update holds only a short filesystem lock; network is never
// run under it. Key material is supplied in memory and never persisted here.
type FileStore struct {
	*StateJournal
	dir, path   string
	instance    string
	key         [32]byte
	mu          sync.Mutex
	ownerMu     sync.Mutex
	ownerEpoch  string
	ownerActive bool
	active      int
	Fault       func(string) error
}

func OpenFileStore(dir, instance string, key [32]byte) (*FileStore, error) {
	if key == [32]byte{} {
		return nil, ErrStore
	}
	if instance == "" {
		return nil, ErrStale
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, ErrStore
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStore
	}
	sum := sha256.Sum256([]byte(instance))
	name := hex.EncodeToString(sum[:]) + ".sealed"
	store := &FileStore{dir: dir, path: filepath.Join(dir, name), instance: instance, key: key}
	store.StateJournal, _ = NewStateJournal(fileStoreBackend{store})
	return store, nil
}
func (f *FileStore) crypt() (cipher.AEAD, error) {
	block, err := aes.NewCipher(f.key[:])
	if err != nil {
		return nil, ErrStore
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrStore
	}
	return aead, nil
}
func (f *FileStore) read() (State, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxStateBytes+65))
	if err != nil {
		return State{}, ErrStore
	}
	if len(raw) > maxStateBytes+64 {
		return State{}, ErrCapacity
	}
	aead, err := f.crypt()
	if err != nil {
		return State{}, err
	}
	if len(raw) < aead.NonceSize() {
		return State{}, ErrStore
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("autosignin-durable-v1:"+filepath.Base(f.path)))
	if err != nil {
		return State{}, ErrStore
	}
	var state State
	if json.Unmarshal(plain, &state) != nil || state.Schema != 1 {
		return State{}, ErrStore
	}
	return state, nil
}
func (f *FileStore) write(state State) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return ErrStore
	}
	if len(raw) > maxStateBytes {
		return ErrCapacity
	}
	aead, err := f.crypt()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return ErrStore
	}
	sealed := aead.Seal(nonce, nonce, raw, []byte("autosignin-durable-v1:"+filepath.Base(f.path)))
	if f.Fault != nil {
		if err = f.Fault("before_write"); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(f.dir, ".owned-state-*")
	if err != nil {
		return ErrStore
	}
	name := tmp.Name()
	defer os.Remove(name)
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
		}
	}()
	if _, err = tmp.Write(sealed); err != nil {
		return ErrStore
	}
	if err = tmp.Sync(); err != nil {
		return ErrStore
	}
	if err = tmp.Close(); err != nil {
		return ErrStore
	}
	ok = true
	if err = os.Rename(name, f.path); err != nil {
		return ErrStore
	}
	directory, err := os.Open(f.dir)
	if err != nil {
		return ErrStore
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return ErrStore
	}
	if f.Fault != nil {
		if err = f.Fault("after_commit"); err != nil {
			return err
		}
	}
	return nil
}

func (f *FileStore) update(ctx context.Context, fn func(*State) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	lock, err := os.OpenFile(f.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ErrStore
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return ErrStore
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	state, err := f.read()
	if errors.Is(err, os.ErrNotExist) {
		state = State{}
	} else if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = fn(&state); errors.Is(err, ErrNoChange) {
		return nil
	} else if err != nil {
		return err
	}
	return f.write(state)
}

func (f *FileStore) Provision(ctx context.Context, scope Scope, runtime json.RawMessage) error {
	if scope.Instance != f.instance || scope.Generation == 0 || scope.Revision == 0 || scope.Implementation == "" {
		return ErrStale
	}
	return f.update(ctx, func(s *State) error {
		if s.Schema != 0 {
			return ErrConflict
		}
		initial, err := NewState(scope, runtime)
		if err == nil {
			*s = initial
		}
		return err
	})
}

// Claim requires a host lifecycle lease. The standalone implementation acquires
// an OS lease until Close, so a second live process cannot steal an epoch.
type Lease struct {
	mu      sync.Mutex
	store   *FileStore
	file    *os.File
	Binding Binding
}

func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	l.store.ownerMu.Lock()
	if l.store.active > 0 {
		l.store.ownerMu.Unlock()
		return ErrBusy
	}
	l.store.ownerActive = false
	l.store.ownerMu.Unlock()
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
func (f *FileStore) Claim(ctx context.Context, b Binding) (*Lease, error) {
	if b.Epoch == "" {
		return nil, ErrStale
	}
	file, err := os.OpenFile(f.path+".owner", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrStore
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, ErrBusy
	}
	err = f.update(ctx, func(s *State) error {
		return RestoreOwnerState(s, b)
	})
	if err != nil {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		return nil, err
	}
	f.ownerMu.Lock()
	f.ownerActive = true
	f.ownerEpoch = b.Epoch
	f.ownerMu.Unlock()
	return &Lease{store: f, file: file, Binding: b}, nil
}

func (f *FileStore) updateRead(ctx context.Context, fn func(*State) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.read()
	if err != nil {
		return err
	}
	return fn(&s)
}

func (f *FileStore) retainOwner(b Binding) (func(), error) {
	f.ownerMu.Lock()
	defer f.ownerMu.Unlock()
	if !f.ownerActive || f.ownerEpoch != b.Epoch {
		return nil, ErrStale
	}
	f.active++
	return func() { f.ownerMu.Lock(); f.active--; f.ownerMu.Unlock() }, nil
}
func (f *FileStore) AcquireExecution(ctx context.Context, b Binding) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	releaseOwner, err := f.retainOwner(b)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(f.path+".execute", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		releaseOwner()
		return nil, ErrStore
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		releaseOwner()
		return nil, ErrBusy
	}
	release := func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close(); releaseOwner() }
	if _, err = f.State(ctx, b); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// AbortKnown is a trusted lifecycle operation. It cannot discard an ambiguous
// request or alter receipts; it only releases a run whose external outcomes are
// already known so a deliberate configuration correction can start a new run.
