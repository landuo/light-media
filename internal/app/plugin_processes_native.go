//go:build !darwin

package app

import (
	"context"
	"github.com/shirou/gopsutil/v4/process"
)

func openNativePluginProcess(ctx context.Context, pid int32) (pluginProcessReader, error) {
	return process.NewProcessWithContext(ctx, pid)
}
