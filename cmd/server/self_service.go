package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"light-media/internal/app"
	"light-media/web"
)

func selfServiceOptions(config selfServiceConfiguration) (app.Options, error) {
	large, err := bigMemoryMode(os.LookupEnv)
	if err != nil {
		return app.Options{}, errors.New("invalid_big_memory_mode")
	}
	safeEnv, err := safeMode(os.LookupEnv)
	if err != nil {
		return app.Options{}, errors.New("invalid_safe_mode")
	}
	developer, err := startupBoolean(os.LookupEnv, "DEV")
	if err != nil {
		return app.Options{}, errors.New("invalid_plugin_developer_mode")
	}
	logRotation, err := app.PluginLogRotationEnvironment(os.LookupEnv)
	if err != nil {
		return app.Options{}, errors.New("invalid_plugin_log_rotation")
	}
	searchPages, normalizedSearchPages := app.ResourceSearchPagesEnvironment(os.LookupEnv)
	mediaSearch, normalizedMediaSearch := app.MediaSearchEnvironment(os.LookupEnv)
	if normalizedMediaSearch {
		fmt.Fprintln(os.Stderr, `{"level":"WARN","event":"media_search_environment_normalized"}`)
	}
	if normalizedSearchPages {
		fmt.Fprintln(os.Stderr, `{"level":"WARN","event":"resource_search_pages_normalized"}`)
	}
	return app.Options{DataDir: config.DataDir, Web: web.Handler(), BigMemoryMode: large, SafeMode: config.Safe || safeEnv, PluginMarketsOverride: pluginMarketEnvironment(os.LookupEnv), PluginDeveloperMode: developer, PluginLogRotation: logRotation, ResourceSearchPages: searchPages, MediaSearch: mediaSearch}, nil
}

// Only the exact supervisor-created extra descriptors 3/4 are recognized.
// Bare invocation of the private flag is not a working server mode.
func openSelfServicePipes() (*os.File, *os.File, error) {
	for _, fd := range []int{3, 4} {
		var info unix.Stat_t
		if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFIFO {
			return nil, nil, errors.New("invalid_self_service_pipes")
		}
	}
	// Make inherited pipes pollable before NewFile so Close reliably wakes the
	// control-reader goroutine; flags apply only to these private descriptors.
	for _, fd := range []int{3, 4} {
		unix.CloseOnExec(fd)
		if err := unix.SetNonblock(fd, true); err != nil {
			return nil, nil, errors.New("invalid_self_service_pipes")
		}
	}
	control := os.NewFile(3, "self-service-control")
	status := os.NewFile(4, "self-service-status")
	if control == nil || status == nil {
		return nil, nil, errors.New("missing_self_service_pipes")
	}
	return control, status, nil
}

func runSelfServiceChild(parent context.Context, config selfServiceConfiguration) error {
	control, status, err := openSelfServicePipes()
	if err != nil {
		return err
	}
	defer status.Close()
	defer control.Close()
	reader := bufio.NewReaderSize(control, 128)
	line, err := reader.ReadSlice('\n')
	if err != nil || string(line) != selfControlGreeting+"\n" {
		return errors.New("invalid_self_service_handshake")
	}
	lifetime, stop := context.WithCancel(parent)
	defer stop()
	controlDone := make(chan struct{})
	go func() {
		defer close(controlDone)
		// No further command or executable arguments are accepted. Any byte or
		// EOF requests orderly shutdown; the parent normally closes its pipe.
		_, _ = reader.ReadByte()
		stop()
	}()
	defer func() { _ = control.Close(); <-controlDone }()
	if err = lifetime.Err(); err != nil {
		return nil
	}
	options, err := selfServiceOptions(config)
	if err != nil {
		return err
	}
	if err = applyRestore(config.DataDir); err != nil {
		return err
	}
	a, err := app.New(options)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		_ = a.Close()
		return errors.New("http_listener_failed")
	}
	ctx, cancel := context.WithCancel(lifetime)
	intent, err := a.AttachSelfRestartController(ctx)
	if err != nil {
		cancel()
		_ = listener.Close()
		_ = a.Close()
		return err
	}
	a.Start(ctx)
	server := &http.Server{Addr: config.Listen, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	// A real bound listener and the Serve goroutine exist before opening the
	// private latch. Normal startup sync (or safe startup) is the other condition;
	// its own admitted task performs finish, never this server control goroutine.
	readyErr := a.MarkSelfServiceListenerReady(ctx)
	if readyErr == nil {
		fmt.Printf("light-media listening at %s\n", listener.Addr())
		_, readyErr = fmt.Fprintf(status, "ready %d\n", os.Getpid())
	}
	restart := false
	serveObserved := false
	var serveErr error
	if readyErr == nil {
		select {
		case <-lifetime.Done():
		case <-intent:
			restart = true
		case serveErr = <-errs:
			serveObserved = true
			if errors.Is(serveErr, http.ErrServerClosed) {
				serveErr = nil
			}
		}
	}
	closeErr := shutdownServerGeneration(cancel, server, a)
	// Shutdown completes Serve; observe its original goroutine, not a PID poll.
	if !serveObserved {
		serveErr = <-errs
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
	}
	reason := "stop"
	if closeErr != nil || serveErr != nil || readyErr != nil {
		reason = "error"
	} else if restart && lifetime.Err() == nil {
		reason = "restart"
	}
	_, statusErr := fmt.Fprintf(status, "closed %s\n", reason)
	return errors.Join(closeErr, serveErr, readyErr, statusErr)
}
