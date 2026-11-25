package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	sdk "light-media/builtinplugins/internal/sdk"
)

// Source TEMP_PATH/signin/tjupt.json is shared by base/clone classes and
// belongs to host temporary-file cleanup, never persistent Base plugin data.
// Only the path snapshot is synchronized; cache file IO remains unsynchronized.
type sdkTjuptCache struct {
	backend   sdkBackend
	pathOnce  sync.Once
	path      string
	pathError error
}
type cacheIOError struct{ cause error }

func (e cacheIOError) Error() string { return e.cause.Error() }
func (e cacheIOError) Unwrap() error { return e.cause }
func (e cacheIOError) Is(target error) bool {
	return target == ErrTjuptCacheIO || errors.Is(e.cause, target)
}
func (c *sdkTjuptCache) ioError(err error) error {
	if err == nil {
		return nil
	}
	if c.backend.ctx.Err() != nil {
		return c.backend.ctx.Err()
	}
	var errno syscall.Errno
	if errors.As(err, &errno) || errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return cacheIOError{err}
	}
	// Root escape and non-OS guard failures are not source-caught IO errors.
	c.backend.fatal(err)
	return err
}

func (c *sdkTjuptCache) root(ctx context.Context) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.pathOnce.Do(func() {
		result, err := (sdk.SystemSettings{Host: c.backend.host}).Get(ctx, "TEMP_PATH")
		if err != nil {
			c.pathError = err
			return
		}
		var path *string
		if err = json.Unmarshal(result.Value, &path); err != nil || path == nil || *path == "" || !filepath.IsAbs(*path) {
			c.pathError = errors.New("invalid host shared TEMP_PATH setting")
			return
		}
		c.path = *path
	})
	// Missing/invalid settings and typed scope failures are not source-caught IO
	// misses. Retain the same failure for concurrent users, without an old-path
	// fallback or automatic retry of the rejected host call.
	c.backend.fatal(c.pathError)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.OpenRoot(c.path)
}
func (c *sdkTjuptCache) Ensure(ctx context.Context) error {
	root, err := c.root(ctx)
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Mkdir("signin", 0755)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
func (c *sdkTjuptCache) Read(ctx context.Context) ([]byte, error) {
	root, err := c.root(ctx)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open("signin/tjupt.json")
	if err != nil {
		return nil, c.ioError(err)
	}
	defer file.Close()
	value, err := io.ReadAll(file)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return value, c.ioError(err)
}
func (c *sdkTjuptCache) Write(ctx context.Context, value []byte) error {
	root, err := c.root(ctx)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile("signin/tjupt.json", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0666)
	if err != nil {
		return c.ioError(err)
	}
	_, err = file.Write(value)
	closeErr := file.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return c.ioError(err)
	}
	return c.ioError(closeErr)
}

type unavailableTjuptPublic struct{ backend sdkBackend }

func (u unavailableTjuptPublic) Suggest(context.Context, string) (*PageResponse, error) {
	u.backend.fatal(&MissingCapability{"source public Douban GET", "Network.Get cannot supply source Referer and env-only proxy mode"})
	return nil, nil
}
func (u unavailableTjuptPublic) Image(context.Context, string) (*PageResponse, error) {
	u.backend.fatal(&MissingCapability{"source public image GET", "Network.Get cannot supply source Referer and env-only proxy mode"})
	return nil, nil
}
