package main

import (
	"context"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"light-media/internal/app"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/web"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	var addr, dir, pluginWorker string
	var bootstrap bool
	flag.StringVar(&addr, "listen", "127.0.0.1:3120", "HTTP listen address")
	flag.StringVar(&dir, "data-dir", "./data", "local data directory")
	flag.BoolVar(&bootstrap, "bootstrap-stdin", false, "one-time encrypted configuration and administrator bootstrap from stdin")
	flag.StringVar(&pluginWorker, "plugin-worker", "", "run a compiled plugin over the private stdio protocol")
	flag.Parse()
	if pluginWorker != "" {
		manifest, handle, exists := builtin.Lookup(pluginWorker)
		if !exists {
			fatal("unknown_plugin")
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if plugins.Serve(ctx, os.Stdin, os.Stdout, manifest, handle) != nil {
			fatal("plugin_worker_failed")
		}
		return
	}
	absolute, e := filepath.Abs(dir)
	if e != nil {
		fatal("invalid_data_directory")
	}
	if bootstrap {
		if e = app.Bootstrap(absolute, os.Stdin); e != nil {
			fatal(e.Error())
		}
		fmt.Println("bootstrap_complete")
		return
	}
	if absolute == "/" {
		fatal("use_specific_data_directory")
	}
	if e = os.MkdirAll(absolute, 0700); e != nil {
		fatal("data_directory_unavailable")
	}
	large, e := bigMemoryMode(os.LookupEnv)
	if e != nil {
		fatal("invalid_big_memory_mode")
	}
	parent, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for {
		if e = applyRestore(absolute); e != nil {
			fatal(e.Error())
		}
		a, e := app.New(app.Options{DataDir: absolute, Web: web.Handler(), BigMemoryMode: large})
		if e != nil {
			fatal(e.Error())
		}
		ctx, cancel := context.WithCancel(parent)
		a.Start(ctx)
		server := &http.Server{Addr: addr, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
		errs := make(chan error, 1)
		go func() { errs <- server.ListenAndServe() }()
		fmt.Printf("light-media listening at %s\n", addr)
		restart := false
		select {
		case <-parent.Done():
		case <-a.RestartRequested:
			restart = true
		case err := <-errs:
			if err != http.ErrServerClosed {
				cancel()
				_ = a.Close()
				fatal("http_listener_failed")
			}
		}
		if restart {
			timer := time.NewTimer(300 * time.Millisecond)
			select {
			case <-timer.C:
			case <-parent.Done():
				restart = false
			}
			timer.Stop()
		}
		cancel()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		_ = server.Shutdown(shutdown)
		done()
		_ = server.Close()
		_ = a.Close()
		if !restart || parent.Err() != nil {
			return
		}
	}
}
func applyRestore(dir string) error {
	lock, e := os.OpenFile(filepath.Join(dir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fmt.Errorf("data_directory_in_use")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	return app.ApplyPendingRestore(dir)
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }

// Pydantic bool strings accepted by the original core/config.py setting. An
// invalid startup value is rejected; plugin HTTP payloads cannot change pools.
func bigMemoryMode(lookup func(string) (string, bool)) (bool, error) {
	s, ok := lookup("BIG_MEMORY_MODE")
	if !ok {
		return false, nil
	}
	switch strings.ToLower(s) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	}
	return false, strconv.ErrSyntax
}
