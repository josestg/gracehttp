package gracehttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	l := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	slog.SetDefault(l)
	os.Exit(m.Run())
}

func TestNewGracefulShutdownServer_DefaultOption(t *testing.T) {
	run := NewGracefulShutdownServer(&http.Server{})
	expectTrue(t, run.Server != nil)
	expectTrue(t, run.shutdownDone != nil)
	expectTrue(t, run.logger != nil)
	expectTrue(t, run.signalListener != nil)
	expectTrue(t, run.waitTimeout == 5*time.Second)
}

func TestNewGracefulShutdownServer_CustomOption(t *testing.T) {
	run := NewGracefulShutdownServer(&http.Server{}, WithWaitTimeout(10*time.Second))
	expectTrue(t, run.Server != nil)
	expectTrue(t, run.shutdownDone != nil)
	expectTrue(t, run.logger != nil)
	expectTrue(t, run.signalListener != nil)
	expectTrue(t, run.waitTimeout == 10*time.Second)
}

func TestGracefulShutdown_ListenAndServeListenFailed(t *testing.T) {
	anError := errors.New("an error")
	server := &serverMock{
		tracer:             visitedNone,
		ListenAndServeFunc: listener(0, anError),
	}

	run := NewGracefulShutdownServer(server)
	err := run.ListenAndServe()
	tracer := server.Tracer()
	expectTrue(t, errors.Is(err, anError))
	expectTrue(t, tracer.has(listenAndServeVisited))
	expectFalse(t, tracer.has(shutdownVisited))
	expectFalse(t, tracer.has(closeVisited))
}

func TestGracefulShutdown_ListenAndServeShutdownGracefully(t *testing.T) {
	server := &serverMock{
		tracer:             visitedNone,
		ListenAndServeFunc: listener(100*time.Millisecond, http.ErrServerClosed),
		ShutdownFunc:       shutdown(nil),
	}

	run := NewGracefulShutdownServer(server)
	time.AfterFunc(50*time.Millisecond, func() { run.signalListener <- os.Interrupt })
	err := run.ListenAndServe()
	tracer := server.Tracer()
	expectTrue(t, err == nil)
	expectTrue(t, tracer.has(listenAndServeVisited))
	expectTrue(t, tracer.has(shutdownVisited))
	expectFalse(t, tracer.has(closeVisited))
}

func TestGracefulShutdown_ListenAndServeShutdownGracefullyButFailedWithUnexpectedError(t *testing.T) {
	anError := errors.New("an error")
	server := &serverMock{
		tracer:             visitedNone,
		ListenAndServeFunc: listener(100*time.Millisecond, http.ErrServerClosed),
		ShutdownFunc:       shutdown(anError),
	}

	run := NewGracefulShutdownServer(server, WithWaitTimeout(100*time.Millisecond))
	time.AfterFunc(50*time.Millisecond, func() { run.signalListener <- os.Interrupt })

	err := run.ListenAndServe()
	tracer := server.Tracer()
	expectTrue(t, errors.Is(err, anError))
	expectTrue(t, tracer.has(listenAndServeVisited))
	expectTrue(t, tracer.has(shutdownVisited))
	expectFalse(t, tracer.has(closeVisited))
}

func TestGracefulShutdown_ListenAndServeShutdownGracefullyButFailed(t *testing.T) {
	anError := errors.New("an error")
	server := &serverMock{
		tracer:             visitedNone,
		ListenAndServeFunc: listener(100*time.Millisecond, http.ErrServerClosed),
		ShutdownFunc:       shutdown(context.DeadlineExceeded),
		CloseFunc:          func() error { return anError },
	}

	run := NewGracefulShutdownServer(server, WithWaitTimeout(100*time.Millisecond))
	time.AfterFunc(50*time.Millisecond, func() { run.signalListener <- os.Interrupt })

	err := run.ListenAndServe()
	tracer := server.Tracer()
	expectTrue(t, errors.Is(err, anError))
	expectTrue(t, tracer.has(listenAndServeVisited))
	expectTrue(t, tracer.has(shutdownVisited))
	expectTrue(t, tracer.has(closeVisited))
}

func TestGracefulShutdown_ListenAndServeShutdownForcefully(t *testing.T) {
	server := &serverMock{
		tracer:             visitedNone,
		ListenAndServeFunc: listener(100*time.Millisecond, http.ErrServerClosed),
		ShutdownFunc:       shutdown(context.DeadlineExceeded),
		CloseFunc:          func() error { return nil },
	}

	run := NewGracefulShutdownServer(server, WithWaitTimeout(100*time.Millisecond))
	time.AfterFunc(50*time.Millisecond, func() { run.signalListener <- os.Interrupt })

	err := run.ListenAndServe()
	tracer := server.Tracer()
	expectTrue(t, err == nil)
	expectTrue(t, tracer.has(listenAndServeVisited))
	expectTrue(t, tracer.has(shutdownVisited))
	expectTrue(t, tracer.has(closeVisited))
}

func TestGracefulShutdown_ListenAndServeShutdownForcefullyButFailed(t *testing.T) {
	anError := errors.New("an error")
	server := &serverMock{
		tracer:             visitedNone,
		ListenAndServeFunc: listener(100*time.Millisecond, http.ErrServerClosed),
		ShutdownFunc:       shutdown(context.DeadlineExceeded),
		CloseFunc:          func() error { return anError },
	}

	run := NewGracefulShutdownServer(server, WithWaitTimeout(100*time.Millisecond))
	time.AfterFunc(50*time.Millisecond, func() { run.signalListener <- os.Interrupt })

	err := run.ListenAndServe()
	tracer := server.Tracer()
	expectTrue(t, errors.Is(err, anError))
	expectTrue(t, tracer.has(listenAndServeVisited))
	expectTrue(t, tracer.has(shutdownVisited))
	expectTrue(t, tracer.has(closeVisited))
}

type serverMock struct {
	ListenAndServeFunc func() error
	ShutdownFunc       func(ctx context.Context) error
	CloseFunc          func() error
	tracer             visitedFlags
	sync.RWMutex
}

func (s *serverMock) ListenAndServe() error {
	s.Lock()
	s.tracer = s.tracer.visit(listenAndServeVisited)
	s.Unlock()
	return s.ListenAndServeFunc()
}

func (s *serverMock) Shutdown(ctx context.Context) error {
	s.Lock()
	s.tracer = s.tracer.visit(shutdownVisited)
	s.Unlock()
	return s.ShutdownFunc(ctx)
}

func (s *serverMock) Close() error {
	s.Lock()
	s.tracer = s.tracer.visit(closeVisited)
	s.Unlock()
	return s.CloseFunc()
}

func (s *serverMock) Tracer() visitedFlags {
	s.RLock()
	defer s.RUnlock()
	return s.tracer
}

type visitedFlags int

func (f visitedFlags) has(flag visitedFlags) bool           { return f&flag != 0 }
func (f visitedFlags) visit(flag visitedFlags) visitedFlags { return f | flag }

const (
	visitedNone visitedFlags = 1 << iota
	listenAndServeVisited
	shutdownVisited
	closeVisited
)

func listener(sleep time.Duration, err error) func() error {
	return func() error {
		<-time.After(sleep)
		return err
	}
}

func shutdown(err error) func(context.Context) error {
	return func(ctx context.Context) error {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			return errors.New("context should have deadline")
		}
		return err
	}
}

func expectTrue(t *testing.T, cond bool) {
	t.Helper()
	if !cond {
		t.Fatal("expected true")
	}
}

func expectFalse(t *testing.T, cond bool) {
	t.Helper()
	if cond {
		t.Fatal("expected false")
	}
}
