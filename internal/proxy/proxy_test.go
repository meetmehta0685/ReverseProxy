package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestProxyForwardsRequests(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(requestIDHeader); got != "trace-123" {
			t.Errorf("request ID = %q, want trace-123", got)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(r.Method + " " + r.URL.RequestURI() + " body=" + string(body)))
	}))
	defer backend.Close()

	p, err := New(Config{Backend: backend.URL, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://proxy.test/orders?id=42", strings.NewReader("payload"))
	request.Header.Set(requestIDHeader, "trace-123")
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("X-Upstream"); got != "ok" {
		t.Fatalf("header = %q", got)
	}
	if got := recorder.Header().Get(requestIDHeader); got != "trace-123" {
		t.Fatalf("response request ID = %q", got)
	}
	body, _ := io.ReadAll(recorder.Result().Body)
	if string(body) != "POST /orders?id=42 body=payload" {
		t.Fatalf("body = %q", body)
	}
}

func TestRequestIDIsGeneratedAndForwarded(t *testing.T) {
	var upstreamID atomic.Value
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamID.Store(r.Header.Get(requestIDHeader))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	p, err := New(Config{Backend: backend.URL, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	responseID := recorder.Header().Get(requestIDHeader)
	if !validRequestID(responseID) {
		t.Fatalf("generated request ID = %q", responseID)
	}
	if got := upstreamID.Load().(string); got != responseID {
		t.Fatalf("upstream request ID = %q, response ID = %q", got, responseID)
	}
}

func TestLocalEndpoints(t *testing.T) {
	p, err := New(Config{Backend: "http://localhost:1", Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"status":"ok"}` {
		t.Fatalf("health body = %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("health content type = %q", got)
	}

	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://proxy.test/healthz", nil)
	p.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("health POST status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("health Allow = %q", got)
	}

	recorder = httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", recorder.Code)
	}
	for _, metric := range []string{
		"# TYPE proxy_requests_total counter",
		"# TYPE proxy_in_flight gauge",
		"proxy_upstream_failures_total",
	} {
		if !strings.Contains(recorder.Body.String(), metric) {
			t.Fatalf("metrics missing %q", metric)
		}
	}
}

func TestRateLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	p, err := New(Config{Backend: backend.URL, Logger: testLogger(), RateLimitPerSecond: 1})
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	p.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	p.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d", second.Code)
	}
	if got := second.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	current := time.Unix(100, 0)
	limiter := newRateLimiter(1)
	limiter.now = func() time.Time { return current }
	limiter.window = current

	if !limiter.allow() {
		t.Fatal("first request should be allowed")
	}
	if limiter.allow() {
		t.Fatal("second request in window should be rejected")
	}
	current = current.Add(time.Second)
	if !limiter.allow() {
		t.Fatal("request in new window should be allowed")
	}
}

func TestCircuitBreakerOpensOnUpstreamFailures(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer backend.Close()

	p, err := New(Config{Backend: backend.URL, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < defaultCircuitFailureThreshold; i++ {
		recorder := httptest.NewRecorder()
		p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil))
		if recorder.Code != http.StatusBadGateway {
			t.Fatalf("failure %d status = %d", i+1, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("open circuit status = %d", recorder.Code)
	}
	if got := calls.Load(); got != defaultCircuitFailureThreshold {
		t.Fatalf("backend calls = %d, want %d", got, defaultCircuitFailureThreshold)
	}
}

func TestCircuitBreakerAllowsOneHalfOpenProbe(t *testing.T) {
	current := time.Unix(200, 0)
	breaker := newCircuitBreaker(1, time.Second)
	breaker.now = func() time.Time { return current }

	permit, allowed := breaker.acquire()
	if !allowed {
		t.Fatal("initial request should be allowed")
	}
	breaker.recordFailure(permit)
	if breaker.allow() {
		t.Fatal("open circuit should reject requests")
	}

	current = current.Add(time.Second)
	probe, allowed := breaker.acquire()
	if !allowed {
		t.Fatal("half-open probe should be allowed")
	}
	if breaker.allow() {
		t.Fatal("second half-open request should be rejected")
	}
	breaker.recordSuccess(permit) // stale result must not close the circuit
	if breaker.allow() {
		t.Fatal("stale success should not close the circuit")
	}
	breaker.recordSuccess(probe)
	if !breaker.allow() {
		t.Fatal("successful probe should close the circuit")
	}
}

func TestUpstreamTimeoutReturnsBadGateway(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer backend.Close()

	p, err := New(Config{Backend: backend.URL, Logger: testLogger(), UpstreamTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://proxy.test/slow", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("timeout status = %d", recorder.Code)
	}
}

func TestNewRejectsUnsafeOrInvalidConfiguration(t *testing.T) {
	validBackend := "http://localhost:9000"
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "missing backend", cfg: Config{}},
		{name: "unsupported scheme", cfg: Config{Backend: "ftp://localhost:9000"}},
		{name: "missing host", cfg: Config{Backend: "http:///path"}},
		{name: "credentials", cfg: Config{Backend: "http://user:pass@localhost:9000"}},
		{name: "fragment", cfg: Config{Backend: "http://localhost:9000/#fragment"}},
		{name: "negative rate limit", cfg: Config{Backend: validBackend, RateLimitPerSecond: -1}},
		{name: "negative timeout", cfg: Config{Backend: validBackend, UpstreamTimeout: -time.Second}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatal("New returned nil error")
			}
		})
	}
}

func TestResponseWriterTracksFinalStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &responseWriter{ResponseWriter: recorder}
	writer.WriteHeader(http.StatusEarlyHints)
	writer.WriteHeader(http.StatusCreated)
	if got := writer.Status(); got != http.StatusCreated {
		t.Fatalf("status = %d, want %d", got, http.StatusCreated)
	}
}
