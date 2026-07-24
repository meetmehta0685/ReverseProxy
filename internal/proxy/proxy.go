package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Config controls the proxy's upstream and observability behavior.
type Config struct {
	Backend            string
	Logger             *slog.Logger
	RateLimitPerSecond int
	UpstreamTimeout    time.Duration
}

// Proxy forwards HTTP requests to a configured upstream backend.
type Proxy struct {
	handler         http.Handler
	logger          *slog.Logger
	metrics         metrics
	limiter         *rateLimiter
	breaker         *circuitBreaker
	upstreamTimeout time.Duration
}

var requestSequence atomic.Uint64

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
	if cfg.UpstreamTimeout <= 0 {
		cfg.UpstreamTimeout = 15 * time.Second
	}

	upstream := httputil.NewSingleHostReverseProxy(target)
	upstream.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		p := r.Context().Value(breakerKey{})
		if b, ok := p.(*circuitBreaker); ok {
			b.recordFailure()
		}
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

	proxy := &Proxy{logger: cfg.Logger, limiter: newRateLimiter(cfg.RateLimitPerSecond), breaker: newCircuitBreaker(), upstreamTimeout: cfg.UpstreamTimeout}
	proxy.handler = loggingMiddleware(proxy.guard(upstream), cfg.Logger, &proxy.metrics)
	return proxy, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	if r.URL.Path == "/metrics" {
		p.metrics.write(w)
		return
	}
	p.handler.ServeHTTP(w, r)
}

type breakerKey struct{}

func (p *Proxy) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.limiter.allow() {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		if !p.breaker.allow() {
			http.Error(w, "upstream circuit open", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), p.upstreamTimeout)
		defer cancel()
		r = r.WithContext(context.WithValue(ctx, breakerKey{}, p.breaker))
		p.metrics.inFlight.Add(1)
		defer p.metrics.inFlight.Add(-1)
		bw := &breakerWriter{ResponseWriter: w}
		next.ServeHTTP(bw, r)
		if bw.status > 0 && bw.status < 500 {
			p.breaker.recordSuccess()
		}
	})
}

type breakerWriter struct {
	http.ResponseWriter
	status int
}

func (w *breakerWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *breakerWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
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

func loggingMiddleware(next http.Handler, logger *slog.Logger, metrics *metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = fmt.Sprintf("req-%d", requestSequence.Add(1))
		}
		w.Header().Set("X-Request-ID", requestID)
		started := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		metrics.requests.Add(1)
		if sw.status >= 500 {
			metrics.errors.Add(1)
		}
		logger.Info("request completed", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration_ms", time.Since(started).Milliseconds())
	})
}

type metrics struct{ requests, errors, inFlight atomic.Int64 }

func (m *metrics) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("proxy_requests_total " + strconv.FormatInt(m.requests.Load(), 10) + "\nproxy_errors_total " + strconv.FormatInt(m.errors.Load(), 10) + "\nproxy_in_flight " + strconv.FormatInt(m.inFlight.Load(), 10) + "\n"))
}

type rateLimiter struct {
	mu           sync.Mutex
	limit, count int
	window       time.Time
}

func newRateLimiter(limit int) *rateLimiter { return &rateLimiter{limit: limit, window: time.Now()} }
func (l *rateLimiter) allow() bool {
	if l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.window) >= time.Second {
		l.window, l.count = now, 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

type circuitBreaker struct {
	mu       sync.Mutex
	failures int
	openedAt time.Time
}

func newCircuitBreaker() *circuitBreaker { return &circuitBreaker{} }
func (b *circuitBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openedAt.IsZero() || time.Since(b.openedAt) >= 10*time.Second
}
func (b *circuitBreaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= 5 {
		b.openedAt = time.Now()
	}
}

func (b *circuitBreaker) recordSuccess() {
	b.mu.Lock()
	b.failures, b.openedAt = 0, time.Time{}
	b.mu.Unlock()
}
