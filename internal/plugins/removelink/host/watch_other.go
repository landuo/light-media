//go:build !linux

package cleanuphost

import (
	"context"
	"light-media/internal/plugins/removelink"
	"os"
)

func NativeWatch(context.Context, []removelink.Watch, func(string) (*os.File, error)) (Watcher, error) {
	return nil, ErrUnsupported
}
