package main

// shutdown.go implements graceful shutdown wiring for the serve command. On a
// termination signal the panel must: stop accepting new HTTP connections and
// drain in-flight ones, stop background metrics collection, and wait for
// in-flight background SSH deploys to finish — instead of being killed
// mid-deploy on SIGTERM, which could leave a remote node half-configured and
// corrupt the rollback chain (CTO-review H7).

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// gracefulShutdown blocks until one of the signals in sigs is received, then:
//   - calls srv.Shutdown to stop accepting new connections and drain active
//     ones (10s budget);
//   - calls stopMetrics to halt background metrics collection;
//   - calls waitBackground to wait for in-flight background deploys to finish.
//
// It is split out of serveCmd so the sequence is unit-testable without booting
// the whole daemon. The signal channel is provided by the caller so tests can
// inject a synthetic signal.
func gracefulShutdown(srv *http.Server, stopMetrics, waitBackground func(), sigs <-chan os.Signal) {
	<-sigs
	fmt.Println("shutdown: signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown: http server: %v\n", err)
	}

	if stopMetrics != nil {
		stopMetrics()
	}
	if waitBackground != nil {
		waitBackground()
	}
	fmt.Println("shutdown: complete")
}

// installSignalHandler returns a channel that delivers SIGINT and SIGTERM.
// It is a small helper kept separate so the signal registration lives next to
// the shutdown sequence it feeds.
func installSignalHandler() chan os.Signal {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	return sigs
}