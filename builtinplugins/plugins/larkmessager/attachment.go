package larkmessager

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	sdk "light-media/builtinplugins/internal/sdk"
)

const maxLarkAttachment = 30 << 20

func (a *provider) ReadFile(ctx context.Context, path string) (Blob, error) {
	call := currentInvocation(ctx)
	if call == nil {
		return Blob{}, ErrDependency
	}
	grant, err := (sdk.Storage{Host: call.host}).LocalReadGrant(ctx, path)
	if err != nil {
		return Blob{}, err
	}
	if grant.Size > maxLarkAttachment {
		return Blob{}, ErrUnsupported
	}
	root, err := os.OpenRoot(grant.Root)
	if err != nil {
		return Blob{}, ErrDenied
	}
	defer root.Close()
	// Verify the opened file against the grant, including during the read.
	file, err := root.OpenFile(grant.RelativePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Blob{}, ErrDenied
	}
	defer file.Close()
	if !matchesAttachment(file, grant) {
		return Blob{}, ErrDenied
	}
	data, err := io.ReadAll(io.LimitReader(file, grant.Size+1))
	if err != nil || int64(len(data)) != grant.Size || !matchesAttachment(file, grant) {
		return Blob{}, ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return Blob{}, err
	}
	return Blob{Name: filepath.Base(path), Data: data}, nil
}

func matchesAttachment(file *os.File, grant sdk.StorageLocalReadGrantV1) bool {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Dev) == grant.Device && uint64(stat.Ino) == grant.Inode &&
		info.Size() == grant.Size && info.ModTime().UnixNano() == grant.ModifiedUnixNano
}

func (a *provider) FetchImage(ctx context.Context, ref string) (Blob, error) {
	call := currentInvocation(ctx)
	if call == nil {
		return Blob{}, ErrDependency
	}
	address, err := url.Parse(ref)
	if err != nil {
		return Blob{}, ErrDenied
	}
	result, err := (sdk.Network{Host: call.host}).Get(ctx, sdk.NetworkGetRequestV1{
		URL: ref, Profile: sdk.NetworkGetSourcePublicProfile,
	})
	if err != nil {
		return Blob{}, err
	}
	response := result.Response
	if response == nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return Blob{}, ErrRejected
	}
	return Blob{Name: filepath.Base(address.Path), MIME: response.ContentType, Data: response.Content}, nil
}
