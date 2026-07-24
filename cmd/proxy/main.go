package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meetmehta0685/ReverseProxy/internal/proxy"
)

func main() {
	listen := flag.String("listen", ":8080", "address for the proxy to listen on")
	backend := flag.String("backend", "http://localhost:9000", "upstream backend URL")
	rateLimit := flag.Int("rate-limit", 0, "maximum requests per second (0 disables limiting)")
	upstreamTimeout := flag.Duration("upstream-timeout", 15*time.Second, "maximum duration for an upstream request")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	p, err := proxy.New(proxy.Config{Backend: *backend, Logger: logger, RateLimitPerSecond: *rateLimit, UpstreamTimeout: *upstreamTimeout})
	if err != nil {
		logger.Error("create proxy", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              *listen,
		Handler:           p,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown proxy", "error", err)
		}
	}()

	logger.Info("proxy listening", "address", *listen, "backend", *backend)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve proxy", "error", err)
		os.Exit(1)
	}
}
