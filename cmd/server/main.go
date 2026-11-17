package main

import (
	"context"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"light-media/internal/app"
	"light-media/internal/selfupdate"
	"light-media/web"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
)

// startDebugListener exposes pprof on an explicitly configured address. It is
// off by default so the public listener never carries profiling endpoints.
func startDebugListener(addr string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	go func() {
		log.Printf("debug listener on %s", addr)
		_ = http.ListenAndServe(addr, mux)
	}()
}

func main() {
	var addr, dir string
	var bootstrap, safe bool
	flag.StringVar(&addr, "listen", "127.0.0.1:3120", "HTTP listen address")
	flag.StringVar(&dir, "data-dir", "./data", "local data directory")
	flag.BoolVar(&bootstrap, "bootstrap-stdin", false, "one-time encrypted configuration and administrator bootstrap from stdin")
	flag.BoolVar(&safe, "safe", false, "start management without plugins, schedulers, monitors or automatic synchronization")
	flag.Parse()
	startDebugListener(os.Getenv("LIGHT_MEDIA_DEBUG_ADDR"))
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
	// Source startup updates the install before the service binds. The check is
	// best effort: a stale feed or a rejected artifact must never block startup.
	// The container mounts /app read-only, so the artifact is installed under the
	// data directory and this process re-executes from there.
	if e = applyStartupUpdate(absolute); e != nil {
		fmt.Fprintf(os.Stderr, `{"level":"WARN","event":"startup_update_failed","reason":%q}`+"\n", e.Error())
	}
	large, e := bigMemoryMode(os.LookupEnv)
	if e != nil {
		fatal("invalid_big_memory_mode")
	}
	safeEnv, e := safeMode(os.LookupEnv)
	if e != nil {
		fatal("invalid_safe_mode")
	}
	developer, e := startupBoolean(os.LookupEnv, "DEV")
	if e != nil {
		fatal("invalid_plugin_developer_mode")
	}
	logRotation, e := app.PluginLogRotationEnvironment(os.LookupEnv)
	if e != nil {
		fatal("invalid_plugin_log_rotation")
	}
	searchPages, normalizedSearchPages := app.ResourceSearchPagesEnvironment(os.LookupEnv)
	mediaSearch, normalizedMediaSearch := app.MediaSearchEnvironment(os.LookupEnv)
	if normalizedMediaSearch {
		fmt.Fprintln(os.Stderr, `{"level":"WARN","event":"media_search_environment_normalized"}`)
	}
	if normalizedSearchPages {
		// Source Settings reports conversion/fallback. Keep the diagnostic while
		// withholding raw environment values from this host's structured output.
		fmt.Fprintln(os.Stderr, `{"level":"WARN","event":"resource_search_pages_normalized"}`)
	}
	safe = safe || safeEnv
	markets := pluginMarketEnvironment(os.LookupEnv)
	frontendVersion, _ := web.FrontendVersion()
	parent, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for {
		if e = applyRestore(absolute); e != nil {
			fatal(e.Error())
		}
		a, e := app.New(app.Options{DataDir: absolute, Web: web.Handler(), FrontendVersion: frontendVersion, BigMemoryMode: large, SafeMode: safe, PluginMarketsOverride: markets, PluginDeveloperMode: developer, PluginLogRotation: logRotation, ResourceSearchPages: searchPages, MediaSearch: mediaSearch})
		if e != nil {
			fatal(e.Error())
		}
		ctx, cancel := context.WithCancel(parent)
		// Bind before starting plugin or downloader side effects. A blocked
		// initializer must not hide the static shell and diagnostic endpoints.
		listener, listenErr := net.Listen("tcp", addr)
		if listenErr != nil {
			cancel()
			_ = a.Close()
			fatal("http_listener_failed")
		}
		started := make(chan struct{})
		httpContext, cancelHTTP := context.WithCancel(context.Background())
		server := &http.Server{Addr: addr, Handler: a.StartupHandler(started), BaseContext: func(net.Listener) context.Context { return httpContext }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
		errs := make(chan error, 1)
		go func() { errs <- server.Serve(listener) }()
		go func() {
			defer close(started)
			a.Start(ctx)
		}()
		fmt.Printf("light-media listening at %s\n", addr)
		restart := false
		select {
		case <-parent.Done():
		case <-a.RestartRequested:
			restart = true
		case err := <-errs:
			if err != http.ErrServerClosed {
				cancel()
				cancelHTTP()
				<-started
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
		shutdown, done := context.WithTimeout(context.Background(), 60*time.Second)
		shutdownError := server.Shutdown(shutdown)
		done()
		if shutdownError != nil {
			// Source cancels HTTP tasks at graceful expiry while synchronous
			// plugin functions may still run. Let their HTTP waiters send500
			// before forcing sockets closed; App.Close joins physical calls.
			cancelHTTP()
			flush, flushed := context.WithTimeout(context.Background(), 2*time.Second)
			_ = server.Shutdown(flush)
			flushed()
		}
		_ = server.Close()
		cancelHTTP()
		// Never close stores or sessions concurrently with startup registration.
		<-started
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

// applyStartupUpdate mirrors the source startup update: check the project's own
// release feed, install a verified newer build, then re-execute it. When it
// returns nil the process keeps running the current build.
// startupUpdateOptions maps the startup environment onto the updater. It is a
// pure function so the wiring stays testable without touching the network.
func startupUpdateOptions(dataDir string, lookup func(string) (string, bool)) selfupdate.Options {
	get := func(key string) string {
		value, _ := lookup(key)
		return value
	}
	mode := get("LIGHT_MEDIA_AUTO_UPDATE")
	if mode == "" {
		mode = get("MOVIEPILOT_AUTO_UPDATE")
	}
	token := get("LIGHT_MEDIA_UPDATE_TOKEN")
	if token == "" {
		token = get("GITHUB_TOKEN")
	}
	return selfupdate.Options{
		Mode:           mode,
		CurrentVersion: app.Version(),
		ReleasesURL:    get("LIGHT_MEDIA_UPDATE_RELEASES"),
		DataDir:        dataDir,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		Token:          token,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	}
}

func applyStartupUpdate(dataDir string) error {
	opts := startupUpdateOptions(dataDir, os.LookupEnv)
	if !selfupdate.ModeEnabled(opts.Mode) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	outcome, err := selfupdate.Run(ctx, opts)
	if err != nil {
		return err
	}
	if outcome.Checked && outcome.Installed == "" {
		fmt.Fprintf(os.Stderr, `{"level":"INFO","event":"startup_update_skipped","reason":%q}`+"\n", outcome.Reason)
	}
	return nil
}
