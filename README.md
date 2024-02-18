# gracehttp

A wrapper around the `net/http.Server` that can be shutdown gracefully.

To read more about graceful shutdowns, see [this blog post](https://blog.stackademic.com/graceful-shutdown-in-go-820d28e1d1c4).

## Installation

```shell
go get github.com/josestg/gracehttp
```

## Usage

```go
package main

import (
    "log/slog"
    "net/http"
    "os"
    "syscall"
    "time"

    "github.com/josestg/gracehttp"
)

func main() {
    log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))

    serv := http.Server{Addr: ":8081"}
    opts := []gracehttp.Option{
        gracehttp.WithLogger(log),
        gracehttp.WithSignals(syscall.SIGINT, syscall.SIGTERM), // default is syscall.SIGINT, syscall.SIGTERM
        gracehttp.WithWaitTimeout(10 * time.Second),            // default is 5 seconds
    }

    gs := gracehttp.NewGracefulShutdownServer(&serv, opts...)
    log.Info("server started")
    defer log.Info("server stopped")

    if err := gs.ListenAndServe(); err != nil {
        log.Error("server failed to start", "err", err)
        os.Exit(1)
    }
}
```