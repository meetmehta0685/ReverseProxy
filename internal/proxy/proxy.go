package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Config controls the proxy's upstream and observability behavior.
type Config struct {
	Backend string
	Logger  *slog.Logger
}

// Proxy forwards HTTP requests to a configured upstream backend.
type Proxy struct {
	handler http.Handler
	logger  *slog.Logger
}

func New(cfg Config) (*Proxy, error) {
	if cfg.Backend == "" {
		return nil, fmt.Errorf("backend URL is required")
	}
	target, err := url.Parse(cfg.Backend)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid backend URL %q", cfg.Backend)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	upstream := httputil.NewSingleHostReverseProxy(target)
	upstream.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		cfg.Logger.Error("upstream request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	upstream.Transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &Proxy{handler: loggingMiddleware(upstream, cfg.Logger), logger: cfg.Logger}, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	p.handler.ServeHTTP(w, r)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func loggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		logger.Info("request completed", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration_ms", time.Since(started).Milliseconds())
	})
}
