package scrapehost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// FileJournal is only the candidate's private fixture durability adapter.
// Production App integration must use its sealed store/transaction ownership.
type FileJournal struct {
	mu           sync.Mutex
	path         string
	beforeCommit func() error
}

func NewFileJournal(dir string) (*FileJournal, error) {
	if !validPath(dir) {
		return nil, ErrUnsafe
	}
	f, e := openDir(dir)
	if e != nil {
		return nil, e
	}
	f.Close()
	return &FileJournal{path: filepath.Join(dir, "state.json")}, nil
}
func (j *FileJournal) read() (Durable, error) {
	var s Durable
	parent, e := openDir(filepath.Dir(j.path))
	if e != nil {
		return s, e
	}
	defer parent.Close()
	fd, e := unix.Openat(int(parent.Fd()), filepath.Base(j.path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	var f *os.File
	if e == nil {
		f = os.NewFile(uintptr(fd), j.path)
	}
	if errors.Is(e, os.ErrNotExist) {
		initDurable(&s)
		return s, nil
	}
	if e != nil {
		return s, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return s, ErrUnsafe
	}
	dec := json.NewDecoder(io.LimitReader(f, 64<<20))
	dec.UseNumber()
	if e = dec.Decode(&s); e != nil {
		return s, e
	}
	if dec.Decode(new(any)) != io.EOF {
		return s, errors.New("invalid journal envelope")
	}
	initDurable(&s)
	return s, nil
}
func (j *FileJournal) Read(ctx context.Context) (Durable, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if ctx.Err() != nil {
		return Durable{}, ctx.Err()
	}
	return j.read()
}
func (j *FileJournal) Update(ctx context.Context, fn func(*Durable) error) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s, e := j.read()
	if e != nil {
		return e
	}
	if e = fn(&s); e != nil {
		return e
	}
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	if len(raw) > 64<<20 {
		return errors.New("journal exceeds explicit fixture budget")
	}
	f, e := os.CreateTemp(filepath.Dir(j.path), ".journal-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if j.beforeCommit != nil {
		if e = j.beforeCommit(); e != nil {
			return e
		}
	}
	if e = os.Rename(f.Name(), j.path); e != nil {
		return e
	}
	dir, e := openDir(filepath.Dir(j.path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
