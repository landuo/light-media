package mergemessagenotify

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/mergemessagenotify/apprise"
)

type localAttachments struct {
	storage sdk.Storage
}

var errLocalAttachment = errors.New("local notification attachment unavailable")

func (a localAttachments) read(ctx context.Context, path string) ([]byte, error) {
	grant, err := a.storage.LocalReadGrant(ctx, path)
	if err != nil {
		return nil, err
	}
	if grant.Size > apprise.MaxAttachmentSize {
		return nil, errLocalAttachment
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(grant.Root)
	if err != nil {
		return nil, errLocalAttachment
	}
	defer root.Close()
	// Confinement and the granted identity protect a replaced path. Nonblocking
	// open prevents a FIFO replacement from blocking before the identity check.
	file, err := root.OpenFile(grant.RelativePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errLocalAttachment
	}
	defer file.Close()
	if !matchesLocalAttachment(file, grant) {
		return nil, errLocalAttachment
	}
	data, err := io.ReadAll(io.LimitReader(file, grant.Size+1))
	if err != nil || int64(len(data)) != grant.Size || !matchesLocalAttachment(file, grant) {
		return nil, errLocalAttachment
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func matchesLocalAttachment(file *os.File, grant sdk.StorageLocalReadGrantV1) bool {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Dev) == grant.Device && uint64(stat.Ino) == grant.Inode &&
		info.Size() == grant.Size && info.ModTime().UnixNano() == grant.ModifiedUnixNano
}
