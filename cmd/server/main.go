package main

import (
	"context"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"light-media/internal/app"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	_ "time/tzdata"
)

func main() {
	var addr, dir string
	var bootstrap, safe, child bool
	flag.StringVar(&addr, "listen", "127.0.0.1:3120", "HTTP listen address")
	flag.StringVar(&dir, "data-dir", "./data", "local data directory")
	flag.BoolVar(&bootstrap, "bootstrap-stdin", false, "one-time encrypted configuration and administrator bootstrap from stdin")
	flag.BoolVar(&safe, "safe", false, "start management without plugins, schedulers, monitors or automatic synchronization")
	flag.BoolVar(&child, "internal-service-child", false, "private service child; requires inherited supervisor pipes")
	flag.Parse()
	absolute, err := filepath.Abs(dir)
	if err != nil {
		fatal("invalid_data_directory")
	}
	if bootstrap {
		if child {
			fatal("bootstrap_is_not_service_child")
		}
		if err = app.Bootstrap(absolute, os.Stdin); err != nil {
			fatal(err.Error())
		}
		fmt.Println("bootstrap_complete")
		return
	}
	if absolute == "/" {
		fatal("use_specific_data_directory")
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		fatal("data_directory_unavailable")
	}
	parent, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	config := selfServiceConfiguration{Listen: addr, DataDir: absolute, Safe: safe}
	if child {
		err = runSelfServiceChild(parent, config)
	} else {
		err = runSelfServerSupervisor(parent, config)
	}
	if err != nil {
		fatal(err.Error())
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
	return startupBoolean(lookup, "BIG_MEMORY_MODE")
}

func safeMode(lookup func(string) (string, bool)) (bool, error) {
	if _, present := lookup("LIGHT_MEDIA_SAFE_MODE"); present {
		return startupBoolean(lookup, "LIGHT_MEDIA_SAFE_MODE")
	}
	return startupBoolean(lookup, "MOVIEPILOT_SAFE_MODE")
}

func startupBoolean(lookup func(string) (string, bool), name string) (bool, error) {
	s, ok := lookup(name)
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

func pluginMarketEnvironment(lookup func(string) (string, bool)) *[]string {
	raw, present := lookup("PLUGIN_MARKET")
	if !present {
		return nil
	}
	sources := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			sources = append(sources, value)
		}
	}
	return &sources
}
