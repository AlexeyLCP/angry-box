package main

// shutdown_test.go pins the graceful-shutdown sequence: on a signal, the panel
// must stop the HTTP server, halt background metrics collection, and wait for
// in-flight background SSH deploys to finish — instead of being killed mid-
// deploy on SIGTERM (CTO-review H7).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestGracefulShutdown_RunsSequenceOnSignal(t *testing.T) {
	var metricsStopped, backgroundWaited int32

	// An http.Server that has never served a request: Shutdown returns nil
	// immediately, which is enough to exercise the call path.
	srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}

	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		gracefulShutdown(srv, func() { atomic.StoreInt32(&metricsStopped, 1) }, func() { atomic.StoreInt32(&backgroundWaited, 1) }, sigs)
		close(done)
	}()

	// Send the signal.
	sigs <- os.Interrupt

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("gracefulShutdown did not return after the signal")
	}

	if atomic.LoadInt32(&metricsStopped) != 1 {
		t.Error("metrics collection was not stopped on shutdown")
	}
	if atomic.LoadInt32(&backgroundWaited) != 1 {
		t.Error("background auto-apply was not waited on shutdown")
	}
}

func TestGracefulShutdown_StopsARealServer(t *testing.T) {
	// Spin up a real httptest server, wire gracefulShutdown against it, signal,
	// and assert the listener stops accepting new connections (Shutdown takes
	// effect). This guards the actual http.Server.Shutdown wiring.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	realSrv := srv.Config // the *http.Server behind the test server
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		gracefulShutdown(realSrv, func() {}, func() {}, sigs)
		close(done)
	}()
	sigs <- os.Interrupt
	<-done

	// After Shutdown the server is no longer listening; a fresh dial should
	// fail. Give it a brief moment to release the port.
	time.Sleep(50 * time.Millisecond)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if _, err := client.Get(srv.URL); err == nil {
		t.Error("server should have stopped accepting connections after shutdown")
	}
}