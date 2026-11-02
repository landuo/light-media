package main

import (
	"context"
	"errors"
	"time"
)

type generationHTTPServer interface {
	Shutdown(context.Context) error
	Close() error
}

type generationApplication interface{ Close() error }

// Extract the existing ordinary server shutdown order without spawning,
// signalling or exiting anything. The actual own-child supervisor must wait
// for this complete result before considering the old generation closed.
// A caller must not hold the App's command/background admission token here.
func shutdownServerGeneration(cancel context.CancelFunc, server generationHTTPServer, application generationApplication) error {
	cancel()
	shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
	shutdownErr := server.Shutdown(shutdown)
	done()
	httpCloseErr := server.Close()
	appCloseErr := application.Close()
	return errors.Join(shutdownErr, httpCloseErr, appCloseErr)
}
