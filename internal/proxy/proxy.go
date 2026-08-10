package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultUpstreamTimeout         = 15 * time.Second
	defaultCircuitFailureThreshold = 5
	defaultCircuitCooldown         = 10 * time.Second
	maxUpstreamConnections         = 100
	maxRequestIDLength             = 128
	requestIDHeader                = "X-Request-ID"
)

// Config controls the proxy's upstream and operational behavior.
type Config struct {
	Backend            string
	Logger             *slog.Logger
	RateLimitPerSecond int
	UpstreamTimeout    time.Duration
}

// Proxy forwards HTTP requests to a configured upstream backend.
type Proxy struct {
	handler         http.Handler
	metrics         metrics
	limiter         *rateLimiter
	breaker         *circuitBreaker
	transport       *http.Transport
	upstreamTimeout time.Duration
}

var requestSequence atomic.Uint64

// New validates cfg and constructs a proxy. A zero UpstreamTimeout uses the
// default; negative values and negative rate limits are configuration errors.
func New(cfg Config) (*Proxy, error) {
	if cfg.Backend == "" {
		return nil, fmt.Errorf("backend URL is required")
	}
	if cfg.RateLimitPerSecond < 0 {
		return nil, fmt.Errorf("rate limit cannot be negative")
	}
	if cfg.UpstreamTimeout < 0 {
		return nil, fmt.Errorf("upstream timeout cannot be negative")
	}

	target, err := parseBackend(cfg.Backend)
	if err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.UpstreamTimeout == 0 {
		cfg.UpstreamTimeout = defaultUpstreamTimeout
	}

	transport := newUpstreamTransport(cfg.UpstreamTimeout)
	upstream := httputil.NewSingleHostReverseProxy(target)
	upstream.Transport = transport
	upstream.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		cfg.Logger.Error("upstream request failed",
			"request_id", r.Header.Get(requestIDHeader),
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	p := &Proxy{
		limiter:         newRateLimiter(cfg.RateLimitPerSecond),
		breaker:         newCircuitBreaker(defaultCircuitFailureThreshold, defaultCircuitCooldown),
		transport:       transport,
		upstreamTimeout: cfg.UpstreamTimeout,
	}
	forward := p.guard(upstream)
	p.handler = loggingMiddleware(p.routes(forward), cfg.Logger, &p.metrics)
	return p, nil
}

func parseBackend(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL %q: %w", raw, err)
	}
	if !strings.EqualFold(target.Scheme, "http") && !strings.EqualFold(target.Scheme, "https") {
		return nil, fmt.Errorf("backend URL must use http or https: %q", raw)
	}
	if target.Host == "" || target.Hostname() == "" {
		return nil, fmt.Errorf("backend URL must include a host: %q", raw)
	}
	if target.User != nil {
		return nil, fmt.Errorf("backend URL must not include credentials")
	}
	if target.Fragment != "" {
		return nil, fmt.Errorf("backend URL must not include a fragment")
	}
	target.Scheme = strings.ToLower(target.Scheme)
	return target, nil
}

func newUpstreamTransport(timeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxConnsPerHost:       maxUpstreamConnections,
		MaxIdleConns:          maxUpstreamConnections,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// CloseIdleConnections releases pooled upstream connections. It is safe to
// register this with http.Server.RegisterOnShutdown.
func (p *Proxy) CloseIdleConnections() {
	if p.transport != nil {
		p.transport.CloseIdleConnections()
	}
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.handler.ServeHTTP(w, r)
}

func (p *Proxy) routes(forward http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			p.health(w, r)
		case "/metrics":
			p.metricsHandler(w, r)
		default:
			forward.ServeHTTP(w, r)
		}
	})
}

func (p *Proxy) health(w http.ResponseWriter, r *http.Request) {
	if !allowGetOrHead(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

func (p *Proxy) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if !allowGetOrHead(w, r) {
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		return
	}
	p.metrics.write(w)
}

func allowGetOrHead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (p *Proxy) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.limiter.allow() {
			p.metrics.rateLimited.Add(1)
			w.Header().Set("Retry-After", retryAfterSeconds(p.limiter.retryAfter()))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		permit, allowed := p.breaker.acquire()
		if !allowed {
			p.metrics.circuitOpen.Add(1)
			w.Header().Set("Retry-After", retryAfterSeconds(p.breaker.retryAfter()))
			http.Error(w, "upstream circuit open", http.StatusServiceUnavailable)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), p.upstreamTimeout)
		defer cancel()
		r = r.WithContext(ctx)

		p.metrics.inFlight.Add(1)
		defer p.metrics.inFlight.Add(-1)

		bw := &responseWriter{ResponseWriter: w}
		defer func() {
			if bw.status == 0 {
				return
			}
			if bw.status >= http.StatusInternalServerError {
				p.metrics.upstreamFailures.Add(1)
				p.breaker.recordFailure(permit)
				return
			}
			p.breaker.recordSuccess(permit)
		}()

		next.ServeHTTP(bw, r)
	})
}

// responseWriter records the final response status while preserving the
// underlying writer through Unwrap for http.ResponseController (for example,
// streaming flushes from httputil.ReverseProxy).
type responseWriter struct {
	http.ResponseWriter
	status    int
	requestID string
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if w.requestID != "" {
		w.ResponseWriter.Header().Set(requestIDHeader, w.requestID)
	}
	// Informational responses do not finalize the response. HTTP 101 is the
	// protocol switch response and is treated as final for accounting.
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *responseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func loggingMiddleware(next http.Handler, logger *slog.Logger, m *metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := ensureRequestID(r)
		w.Header().Set(requestIDHeader, requestID)
		started := time.Now()
		sw := &responseWriter{ResponseWriter: w, requestID: requestID}

		defer func() {
			if recovered := recover(); recovered != nil {
				// ReverseProxy uses ErrAbortHandler to intentionally terminate a
				// connection after a streaming copy error. Do not turn that into
				// a normal, possibly truncated response.
				if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(recovered)
				}
				logger.Error("request handler panicked", "request_id", requestID, "panic", recovered)
				if sw.status == 0 {
					http.Error(sw, "internal server error", http.StatusInternalServerError)
				}
			}

			status := sw.Status()
			m.requests.Add(1)
			if status >= http.StatusInternalServerError {
				m.errors.Add(1)
			}
			logger.Info("request completed",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		}()

		next.ServeHTTP(sw, r)
	})
}

func ensureRequestID(r *http.Request) string {
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
	if !validRequestID(requestID) {
		requestID = fmt.Sprintf("req-%d", requestSequence.Add(1))
	}
	r.Header.Set(requestIDHeader, requestID)
	return requestID
}

func validRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > maxRequestIDLength {
		return false
	}
	for i := 0; i < len(requestID); i++ {
		if requestID[i] < 0x20 || requestID[i] == 0x7f {
			return false
		}
	}
	return true
}

func retryAfterSeconds(duration time.Duration) string {
	if duration <= 0 {
		duration = time.Second
	}
	seconds := int((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
