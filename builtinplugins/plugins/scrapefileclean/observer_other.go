//go:build !linux

package scrapefileclean

import (
	"context"
	"errors"
	"os"
)

var errPlatform = errors.New("local cleanup requires Linux")

func openNativeDirectory(string, *authorizedRoot) (*os.File, error) { return nil, errPlatform }

func NativeWatch(context.Context, string, func(string) (*os.File, error), func(error)) (Watcher, error) {
	return nil, errPlatform
}
