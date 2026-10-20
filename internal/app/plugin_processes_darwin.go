//go:build darwin

package app

import (
	"context"
	"github.com/ebitengine/purego"
	"github.com/shirou/gopsutil/v4/process"
	"runtime"
	"sync"
	"unsafe"
)

var pluginProcName struct {
	once sync.Once
	read func(int32, unsafe.Pointer, uint32) int32
	err  error
}

type darwinPluginProcess struct{ *process.Process }

func openNativePluginProcess(ctx context.Context, pid int32) (pluginProcessReader, error) {
	// PID 0 is present in Darwin's process inventory. gopsutil's constructor
	// rejects it, although its field readers can handle it without signal(0).
	if pid == 0 {
		return darwinPluginProcess{&process.Process{Pid: pid}}, nil
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil, err
	}
	return darwinPluginProcess{p}, nil
}
func (p darwinPluginProcess) NameWithContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	pluginProcName.once.Do(func() {
		// One system library handle is retained for the lifetime of this process.
		// proc_name reads only the name; it never reads a command line or arguments.
		handle, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			pluginProcName.err = err
			return
		}
		symbol, err := purego.Dlsym(handle, "proc_name")
		if err != nil {
			_ = purego.Dlclose(handle)
			pluginProcName.err = err
			return
		}
		purego.RegisterFunc(&pluginProcName.read, symbol)
	})
	if pluginProcName.err != nil {
		return "", pluginProcName.err
	}
	var buffer [1024]byte
	n := pluginProcName.read(p.Pid, unsafe.Pointer(&buffer[0]), uint32(len(buffer)))
	runtime.KeepAlive(&buffer)
	if n <= 0 {
		return "", process.ErrorNotPermitted
	}
	if n > int32(len(buffer)) {
		return "", processReadFailed()
	}
	for i := 0; i < int(n); i++ {
		if buffer[i] == 0 {
			return string(buffer[:i]), nil
		}
	}
	return string(buffer[:n]), nil
}
