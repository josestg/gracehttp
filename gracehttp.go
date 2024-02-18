package gracehttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Server defines a minimum methods that required for graceful shutdown procedure.
type Server interface {
	// ListenAndServe starts listening and serving the server.
	// This method should block until shutdown signal received or failed to start.
	ListenAndServe() error

	// Shutdown gracefully shuts down the server, it will wait for all active connections to be closed.
	Shutdown(ctx context.Context) error

	// Close force closes the server.
	// Close is called when Shutdown timeout exceeded.
	Close() error
}

// GracefulShutdown is a wrapper of http.Server that can be shutdown gracefully.
type GracefulShutdown struct {
	Server
	signalListener chan os.Signal
	waitTimeout    time.Duration
	shutdownDone   chan struct{}
	logger         *slog.Logger
}

// Option is the type for GracefulShutdown options.
type Option func(*GracefulShutdown)

// applyOptions applies the given options to the GracefulShutdown.
func applyOptions(gs *GracefulShutdown, opts []Option) {
	for _, opt := range opts {
		opt(gs)
	}
}

// NewGracefulShutdownServer wraps the given server with graceful shutdown capability mechanism.
// By default, the server will listen to SIGINT and SIGTERM signals to initiate shutdown and wait for all active
// connections to be closed. If still active connections after wait timeout exceeded, it will force close the server.
// The default wait timeout is 5 seconds.
//
// References:
// - https://blog.stackademic.com/graceful-shutdown-in-go-820d28e1d1c4
func NewGracefulShutdownServer(srv Server, opts ...Option) *GracefulShutdown {
	gs := &GracefulShutdown{
		Server:         srv,
		shutdownDone:   make(chan struct{}),
		signalListener: nil,
	}
	applyOptions(gs, opts)
	gs.applyDefaults()
	return gs
}

func (gs *GracefulShutdown) applyDefaults() {
	opts := make([]Option, 0, 3)
	if gs.logger == nil {
		opts = append(opts, WithLogger(slog.Default()))
	}
	if gs.waitTimeout <= 0 {
		opts = append(opts, WithWaitTimeout(5*time.Second))
	}
	if gs.signalListener == nil {
		opts = append(opts, WithSignals(syscall.SIGINT, syscall.SIGTERM))
	}
	applyOptions(gs, opts)
}

// ListenAndServe starts listening and serving the server gracefully.
func (gs *GracefulShutdown) ListenAndServe() error {
	if std, ok := gs.Server.(*http.Server); ok {
		gs.logger.Info("server is listening", "addr", std.Addr)
	} else {
		gs.logger.Info("server is listening")
	}

	serverErr := make(chan error, 1)
	shutdownCompleted := make(chan struct{})
	// start the original server.
	go func() {
		err := gs.Server.ListenAndServe()
		// if shutdown succeeded, http.ErrServerClosed will be returned.
		if errors.Is(err, http.ErrServerClosed) {
			shutdownCompleted <- struct{}{}
		} else {
			// only send error if it's not http.ErrServerClosed.
			serverErr <- err
		}
	}()

	// block until signalListener received or mux failed to start.
	select {
	case sig := <-gs.signalListener:
		gs.logger.Info("graceful shutdown initiated", "received_signal", sig.String())

		ctx, cancel := context.WithTimeout(context.Background(), gs.waitTimeout)
		defer cancel()

		err := gs.Server.Shutdown(ctx)
		// only force shutdown if deadline exceeded.
		if errors.Is(err, context.DeadlineExceeded) {
			gs.logger.Info("deadline exceeded, force shutdown initiated")
			closeErr := gs.Server.Close()
			if closeErr != nil {
				return fmt.Errorf("deadline exceeded, force shutdown failed: %w", closeErr)
			}
			// force shutdown succeeded.
			gs.logger.Info("force shutdown completed")
			return nil
		}

		// unexpected error.
		if err != nil {
			return fmt.Errorf("shutdown failed, signal: %s: %w", sig, err)
		}

		// make sure shutdown completed.
		<-shutdownCompleted
		gs.logger.Info("graceful shutdown completed")
		return nil
	case err := <-serverErr:
		return fmt.Errorf("server failed to start: %w", err)
	}
}

// WithSignals sets the signals to listen for initiating graceful shutdown.
func WithSignals(signals ...os.Signal) Option {
	return func(gs *GracefulShutdown) {
		signalListener := make(chan os.Signal, 1)
		signal.Notify(signalListener, signals...)
		gs.signalListener = signalListener
	}
}

// WithWaitTimeout sets the wait timeout for graceful shutdown.
func WithWaitTimeout(d time.Duration) Option {
	return func(gs *GracefulShutdown) { gs.waitTimeout = d }
}

// WithLogger sets the logger for the GracefulShutdown.
func WithLogger(logger *slog.Logger) Option {
	return func(gs *GracefulShutdown) { gs.logger = logger }
}
