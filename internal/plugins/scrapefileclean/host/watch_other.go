//go:build !linux && !darwin

package scrapehost

import (
	"context"
	"os"
)

func NativeWatch(context.Context, []Watch, func(string) (*os.File, error)) (Watcher, error) {
	return nil, ErrUnsupported
}
